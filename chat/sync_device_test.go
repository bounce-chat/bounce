package chat

import (
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/alecthomas/assert/v2"
)

type syncDeviceTestUI struct {
	*testUI
}

func (u *syncDeviceTestUI) SyncDeviceRequestAccepted(User, []Device, bool) {
	u.called <- call{function: "SyncDeviceRequestAccepted"}
}

func (u *syncDeviceTestUI) InitialSyncComplete() {
	u.called <- call{function: "InitialSyncComplete"}
}

func (u *syncDeviceTestUI) SyncDeviceProfileRefused(reason string) {
	u.called <- call{function: "SyncDeviceProfileRefused", args: []interface{}{reason}}
}

func awaitSyncDeviceCall(t *testing.T, ui *syncDeviceTestUI, function string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ui.Lock()
		found := false
		for _, c := range ui.calls {
			if c.function == function {
				found = true
				break
			}
		}
		ui.Unlock()
		if found {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timeout waiting for " + function)
}

func TestSyncDeviceOfferDoesNotSaveProvisionalDevice(t *testing.T) {
	offer := newBounceUser("Offer")
	t.Cleanup(offer.Shutdown)
	assert.NoError(t, offer.database.Create(&syncDeviceOffer{Timestamp: time.Now().Unix(), Secret: "pair"}).Error)

	newKey := bnc01NewOnion()
	request, err := msgpack.Marshal(&syncDeviceRequest{
		Secret:    "pair",
		Signature: newKey.sign([]byte(offer.network.Address())),
	})
	assert.NoError(t, err)
	offer.handleSyncDeviceRequest(newKey.addr(), request, false)

	_, exists := offer.getDeviceFromAddress(newKey.addr())
	assert.False(t, exists, "only the requester can produce the self-signature that completes pairing")
}

func TestSyncDevicePairingSelfSignsAndCompletesInitialSync(t *testing.T) {
	offer := newBounceUser("Offer")
	t.Cleanup(offer.Shutdown)
	ui := &syncDeviceTestUI{testUI: newTestUI()}
	requester := newBounceWithUI(ui)
	t.Cleanup(requester.Shutdown)
	t.Cleanup(func() {
		setWaitingForInitialSyncFrom("")
		initialSyncAnnounced.Store(false)
	})

	assert.NoError(t, requester.RequestToSync(offer.GetNewSyncString()))
	awaitSyncDeviceCall(t, ui, "SyncDeviceRequestAccepted")

	profile, exists := requester.currentUser()
	assert.True(t, exists)
	assert.Equal(t, offer.currentUserID(), profile.ID)
	localDeviceIndex := -1
	for i, dev := range profile.Devices {
		if dev.Address == requester.network.Address() {
			localDeviceIndex = i
			assert.True(t, requester.hasValidSelfSignature(dev))
		}
	}
	assert.NotEqual(t, -1, localDeviceIndex)
	localDevice := profile.Devices[localDeviceIndex]

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		stored, found := offer.getDeviceFromAddress(requester.network.Address())
		if found {
			assert.Equal(t, localDevice.ID, stored.ID)
			assert.True(t, offer.hasValidSelfSignature(stored))
			awaitSyncDeviceCall(t, ui, "InitialSyncComplete")
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("offer device did not accept the requester's signed device frame")
}

// An install whose own device record was written before device IDs changed still answers a pairing request, and the
// device asking refuses what it is sent.  The user is waiting on that answer, so the refusal has to reach the UI.
func TestSyncDeviceProfileThatIsRefusedIsReportedToTheUI(t *testing.T) {
	offer := newBounceUser("Offer")
	t.Cleanup(offer.Shutdown)
	ui := &syncDeviceTestUI{testUI: newTestUI()}
	requester := newBounceWithUI(ui)
	t.Cleanup(requester.Shutdown)

	// The offer device's own record no longer matches its ID, the way a record from an older version does not
	assert.NoError(t, offer.database.Model(&device{}).Where("address = ?", offer.network.Address()).Update("network", "a network from before").Error)

	assert.NoError(t, requester.RequestToSync(offer.GetNewSyncString()))
	awaitSyncDeviceCall(t, ui, "SyncDeviceProfileRefused")

	ui.Lock()
	for _, c := range ui.calls {
		assert.NotEqual(t, "SyncDeviceRequestAccepted", c.function)
		if c.function == "SyncDeviceProfileRefused" {
			assert.Equal(t, []interface{}{"a device in its profile is not valid"}, c.args)
		}
	}
	ui.Unlock()
	_, exists := requester.currentUser()
	assert.False(t, exists, "a refused profile was saved")
}

// A heldNetwork is the test network with two places a test can stop it at.  One is a dial, which on a real network
// takes its time.  The other is a signature, which the handler of a sync answer makes after it has decided to take
// the answer and before it saves the profile.  A stop that is switched on reports that it was reached, and then
// waits to be let through.
type heldNetwork struct {
	*testnet
	holdDials atomic.Bool
	holdSigns atomic.Bool
	reached   chan string
	proceed   chan struct{}
}

func newHeldNetwork() *heldNetwork {
	return &heldNetwork{testnet: newTestnet(), reached: make(chan string), proceed: make(chan struct{})}
}

func (n *heldNetwork) Dial(address string) (net.Conn, error) {
	if n.holdDials.Load() {
		n.reached <- "dial"
		<-n.proceed
	}
	return n.testnet.Dial(address)
}

func (n *heldNetwork) Sign(data []byte) []byte {
	if n.holdSigns.Load() {
		n.reached <- "sign"
		<-n.proceed
	}
	return n.testnet.Sign(data)
}

func awaitHeld(t *testing.T, n *heldNetwork, place string) {
	t.Helper()
	select {
	case reached := <-n.reached:
		assert.Equal(t, place, reached)
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for the network to be stopped at a " + place)
	}
}

func syncDeviceCalls(ui *syncDeviceTestUI, function string) int {
	ui.Lock()
	defer ui.Unlock()
	count := 0
	for _, c := range ui.calls {
		if c.function == function {
			count++
		}
	}
	return count
}

// A request that is cancelled while it is still dialling sends nothing: the offer it was for is still there to be
// used, which it would not be had the request gone out.  Once an answer has been accepted there is nothing to cancel.
func TestSyncRequestCancelledWhileDiallingSendsNothing(t *testing.T) {
	offer := newBounceUser("Offer")
	t.Cleanup(offer.Shutdown)
	ui := &syncDeviceTestUI{testUI: newTestUI()}
	network := newHeldNetwork()
	requester := newBounceOnNetwork(ui, network)
	t.Cleanup(requester.Shutdown)
	t.Cleanup(func() {
		setWaitingForInitialSyncFrom("")
		initialSyncAnnounced.Store(false)
	})
	code := offer.GetNewSyncString()

	network.holdDials.Store(true)
	result := make(chan error, 1)
	go func() { result <- requester.RequestToSync(code) }()
	awaitHeld(t, network, "dial")
	assert.True(t, requester.CancelSyncRequest())
	network.holdDials.Store(false)
	network.proceed <- struct{}{}
	assert.IsError(t, <-result, ErrSyncRequestCancelled)

	// The same offer still works, so the cancelled request never reached the device that made it
	assert.NoError(t, requester.RequestToSync(code))
	awaitSyncDeviceCall(t, ui, "SyncDeviceRequestAccepted")
	_, exists := requester.currentUser()
	assert.True(t, exists)
	assert.False(t, requester.CancelSyncRequest(), "a request whose answer was accepted was reported as cancelled")
	_, exists = requester.currentUser()
	assert.True(t, exists)
}

// A request that is cancelled after it was sent has its answer ignored, and asking again afterwards still works
func TestSyncRequestCancelledAfterSendingIgnoresTheAnswer(t *testing.T) {
	offer := newBounceUser("Offer")
	t.Cleanup(offer.Shutdown)
	ui := &syncDeviceTestUI{testUI: newTestUI()}
	requester := newBounceWithUI(ui)
	t.Cleanup(requester.Shutdown)
	t.Cleanup(func() {
		setWaitingForInitialSyncFrom("")
		initialSyncAnnounced.Store(false)
	})
	code := offer.GetNewSyncString()
	_, secret, err := offer.parseSyncString(code)
	assert.NoError(t, err)

	// The offer device is kept from answering until the request has been cancelled
	syncDeviceRequestMutex.Lock()
	err = requester.RequestToSync(code)
	cancelled := err == nil && requester.CancelSyncRequest()
	syncDeviceRequestMutex.Unlock()
	assert.NoError(t, err)
	assert.True(t, cancelled)

	// Its answer goes out as soon as it has claimed the offer, and is given a moment to be handled
	deadline := time.Now().Add(3 * time.Second)
	for !secretRecentlyUsed("sync-device-request"+secret) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	assert.True(t, secretRecentlyUsed("sync-device-request"+secret), "the offer device never handled the request")
	time.Sleep(200 * time.Millisecond)
	_, exists := requester.currentUser()
	assert.False(t, exists, "the answer to a cancelled request was accepted")
	assert.Equal(t, 0, syncDeviceCalls(ui, "SyncDeviceRequestAccepted"))

	assert.NoError(t, requester.RequestToSync(offer.GetNewSyncString()))
	awaitSyncDeviceCall(t, ui, "SyncDeviceRequestAccepted")
	assert.Equal(t, 1, syncDeviceCalls(ui, "SyncDeviceRequestAccepted"))
}

// A cancel that arrives while the answer is being checked wins: the handler has already decided the answer is one it
// asked for, and must still not save the profile
func TestSyncRequestCancelledWhileItsAnswerIsCheckedSavesNothing(t *testing.T) {
	offer := newBounceUser("Offer")
	t.Cleanup(offer.Shutdown)
	ui := &syncDeviceTestUI{testUI: newTestUI()}
	network := newHeldNetwork()
	requester := newBounceOnNetwork(ui, network)
	t.Cleanup(requester.Shutdown)

	assert.NoError(t, requester.RequestToSync(offer.GetNewSyncString()))
	network.holdSigns.Store(true)
	awaitHeld(t, network, "sign")
	assert.True(t, requester.CancelSyncRequest())
	network.holdSigns.Store(false)
	network.proceed <- struct{}{}

	time.Sleep(200 * time.Millisecond)
	_, exists := requester.currentUser()
	assert.False(t, exists, "a profile was saved for a request that had been cancelled")
	assert.Equal(t, 0, syncDeviceCalls(ui, "SyncDeviceRequestAccepted"))
	_, found := offer.getDeviceFromAddress(requester.network.Address())
	assert.False(t, found, "the offer device saved a device that never accepted its answer")
}
