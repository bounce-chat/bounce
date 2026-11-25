package chat

import (
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
