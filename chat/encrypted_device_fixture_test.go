package chat

import (
	"crypto/rand"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// newEncryptedDevice runs an encrypted device (a store-and-forward host that cannot read what it stores) on the
// test network, the way StartEncryptedDevice would minus the setup QR code.  It returns the engine and the setup
// key a user needs to claim it.
func newEncryptedDevice(t *testing.T) (*Bounce, string) {
	t.Helper()
	network := newTestnet()
	dir := os.TempDir() + "/bounce-encrypted-test-" + uuid.New().String()
	os.MkdirAll(dir+"/blobs/", 0700)
	t.Cleanup(func() { os.RemoveAll(dir) })

	b := &Bounce{
		encrypted:       true,
		configDirectory: dir,
		network:         network,
		ui:              newTestUI(),
		devicePool: &devicePool{
			deviceMutex:        sync.Mutex{},
			devices:            make(map[string]*remoteDevice),
			userOnlineStatus:   make(map[uuid.UUID]bool),
			deviceOnlineStatus: make(map[uuid.UUID]bool),
			lastDial:           make(map[string]time.Time),
			lastFailedDial:     make(map[string]time.Time),
			revokedDevices:     make(map[string]bool),
		},
		done:         make(chan struct{}),
		handlerSlots: make(chan struct{}, maximumRunningHandlersGlobal),
	}
	b.openEncryptedDatabase()
	b.network.Load(dir)
	b.openReferenceDatabase()
	b.recheckUntrustedEncryptedFrameStorage()

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
	if err := owner.RequestToManageEncryptedDevice(host.network.Address() + ":" + key); err != nil {
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

// waitForOwnerMailboxes waits until the host knows at least one of its owner's mailboxes, which is what lets it
// accept frames for the owner from anyone
func waitForOwnerMailboxes(t *testing.T, host *Bounce) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var n int64
		host.database.Model(&ownerMailbox{}).Count(&n)
		if n > 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("host never learned its owner's mailboxes")
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
func waitForUserToKnowEncryptedDevice(t *testing.T, b *Bounce, userID uuid.UUID, address string) {
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
