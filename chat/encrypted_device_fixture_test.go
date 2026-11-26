package chat

import (
	"crypto/rand"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

// newEncryptedDevice runs an encrypted device (a store-and-forward host that cannot read what it stores) on the
// test network, the way StartEncryptedDevice would minus the setup QR code.  It returns the engine and the setup
// key a user needs to claim it.
func newEncryptedDevice(t *testing.T) (*Bounce, string) {
	t.Helper()
	network := newTestnet()
	dir := os.TempDir() + "/bounce-encrypted-test-" + NewID().String()
	os.MkdirAll(dir+"/blobs/", 0700)
	t.Cleanup(func() { os.RemoveAll(dir) })

	platform := Platform{ConfigDirectory: dir, Secrets: &testSecretStore{}, Files: &testFileStore{}}
	b := &Bounce{
		encrypted:      true,
		platform:       platform,
		network:        network,
		ui:             newTestUI(),
		referenceState: newReferenceState(),
		devicePool: &devicePool{
			deviceMutex:        sync.Mutex{},
			devices:            make(map[string]*remoteDevice),
			userOnlineStatus:   make(map[ID]bool),
			deviceOnlineStatus: make(map[ID]bool),
			lastDial:           make(map[string]time.Time),
			lastFailedDial:     make(map[string]time.Time),
			revokedDevices:     make(map[string]bool),
		},
		done:                      make(chan struct{}),
		handlerSlots:              make(chan struct{}, maximumRunningHandlersGlobal),
		encryptedChunkRequestWake: make(chan struct{}, 1),
	}
	installLogHooks(dir)
	runningEngines.Add(1)
	_, keys, err := unlock(platform, dir+"/bounce.enc.db", "")
	if err != nil {
		t.Fatalf("unlocking the encrypted device: %v", err)
	}
	b.openEncryptedDatabase(keys.databaseKey())
	b.network.Load(dir, keys.networkKey())
	b.openReferenceDatabase()
	b.recheckUntrustedEncryptedFrameStorage()
	b.background(b.keepEncryptedChunksRequested)

	secretBytes := make([]byte, 16)
	rand.Read(secretBytes)
	key := fmt.Sprintf("%x", secretBytes)
	setupKey = key

	go b.network.Start(NetworkCallbacks{
		NetworkOnline:  b.networkOnline,
		NetworkOffline: b.networkOffline,
	})
	t.Cleanup(b.Shutdown)
	return b, key
}

// claimEncryptedDevice has a user take ownership of an encrypted device and waits until the user records it as one
// of its devices and has proven itself to the host
func claimEncryptedDevice(t *testing.T, owner *Bounce, host *Bounce, key string) {
	t.Helper()
	if err := owner.RequestToManageEncryptedDevice(host.buildSyncString(key)); err != nil {
		t.Fatalf("claiming encrypted device: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var esd encryptedSyncDevice
		recorded := owner.database.First(&esd, "address = ?", host.network.Address()).Error == nil
		if recorded && host.peerIsOwner(owner.network.Address()) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("%s never finished claiming encrypted device %s", owner.network.Address()[:8], host.network.Address()[:8])
}

// waitForOwnerMailboxes waits until the host has recorded every mailbox its owner's device asked for, which is what
// lets it accept frames for the owner from anyone.  The host records them one at a time, so having some of them is
// not yet having the one a test is about to use.
func waitForOwnerMailboxes(t *testing.T, host *Bounce) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ownerMailboxesRecorded(host) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("host never learned its owner's mailboxes")
}

// ownerMailboxesRecorded reports whether an owner's device has asked the host for its mailboxes and the host has
// recorded all of them
func ownerMailboxesRecorded(host *Bounce) bool {
	peerMailboxLabelsMutex.Lock()
	peers := []string{}
	for peer := range peerMailboxLabels {
		peers = append(peers, peer)
	}
	peerMailboxLabelsMutex.Unlock()

	for _, peer := range peers {
		if !host.peerIsOwner(peer) {
			continue
		}
		asked := [][]byte{}
		for label := range host.peerLabels(peer) {
			asked = append(asked, []byte(label))
		}
		if len(asked) == 0 {
			continue
		}
		recorded := int64(0)
		for start := 0; start < len(asked); start += 500 {
			var n int64
			host.database.Model(&ownerMailbox{}).Where("label IN ?", asked[start:min(start+500, len(asked))]).Count(&n)
			recorded += n
		}
		return recorded == int64(len(asked))
	}
	return false
}

// waitForStoredFrames waits until the host holds n frames
func waitForStoredFrames(t *testing.T, host *Bounce, n int64) []encryptedFrame {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var frames []encryptedFrame
		host.database.Preload("Mailboxes").Find(&frames)
		if int64(len(frames)) >= n {
			return frames
		}
		time.Sleep(25 * time.Millisecond)
	}
	var frames []encryptedFrame
	host.database.Preload("Mailboxes").Find(&frames)
	t.Fatalf("host holds %d frames, wanted %d", len(frames), n)
	return frames
}

// connectTo dials a peer, forgetting any dial cooldown first, and waits for the socket
func connectTo(t *testing.T, b *Bounce, address string) {
	t.Helper()
	b.devicePool.lastDialMutex.Lock()
	delete(b.devicePool.lastDial, address)
	delete(b.devicePool.lastFailedDial, address)
	b.devicePool.lastDialMutex.Unlock()
	go b.tryDialing(address)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b.getRemoteDevice(address).connectedSockets() > 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("%s never connected to %s", b.network.Address()[:8], address[:8])
}

// disconnectFrom closes every socket between two peers and waits until both sides see none
func disconnectFrom(t *testing.T, a *Bounce, b *Bounce) {
	t.Helper()
	a.getRemoteDevice(b.network.Address()).shutdown()
	b.getRemoteDevice(a.network.Address()).shutdown()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if a.getRemoteDevice(b.network.Address()).connectedSockets() == 0 && b.getRemoteDevice(a.network.Address()).connectedSockets() == 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("%s and %s never disconnected", a.network.Address()[:8], b.network.Address()[:8])
}

// waitForUserToKnowEncryptedDevice waits until b has learned that user has the encrypted device at address
func waitForUserToKnowEncryptedDevice(t *testing.T, b *Bounce, userID ID, address string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var u user
		if b.database.Select("encrypted_devices").First(&u, "id = ?", userID).Error == nil && len(u.EncryptedDevices) > 0 {
			encryptedDeviceCacheMutex.Lock()
			_, cached := encryptedDeviceCache[address]
			encryptedDeviceCacheMutex.Unlock()
			if cached {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("%s never learned about encrypted device %s", b.network.Address()[:8], address[:8])
}
