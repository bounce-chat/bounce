package chat

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"net"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/alecthomas/assert/v2"
)

// readUntil reads frames from a connection until one of the wanted type arrives, and returns its payload
func readUntil(t *testing.T, conn net.Conn, wanted uint16) []byte {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		frameType, payload, err := readFrame(conn, true)
		if err != nil {
			t.Fatalf("no frame of type %d arrived: %v", wanted, err)
		}
		if frameType == wanted {
			return payload
		}
	}
}

func opens(key []byte, ciphertext []byte, additionalData []byte) bool {
	block, err := aes.NewCipher(key)
	if err != nil {
		return false
	}
	gcm, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return false
	}
	_, err = gcm.Open(nil, []byte{}, ciphertext, additionalData)
	return err == nil
}

// Anyone can pose a reference offer challenge, choosing both the public key the user agrees with and the bytes the
// user seals.  The answer is sealed under a key derived for that purpose alone, so posing one with a contact's public
// key gets nothing sealed under the agreement itself or under the keys that wrap the conversation with that contact.
func TestAChallengeIsAnsweredUnderAKeyOfItsOwn(t *testing.T) {
	me, alice, _, _ := createUsersAndGroups(t)
	meUser := mustCurrentUser(me)
	aliceUser := mustCurrentUser(alice)

	stranger := newTestnet()
	conn, err := stranger.Dial(me.network.Address())
	assert.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	chosen := make([]byte, 32)
	rand.Read(chosen)
	challenge := &encryptedReferenceOfferChallenge{Key: aliceUser.PublicECDHKey, Challenge: chosen}
	assert.NoError(t, writeFrame(conn, typeEncryptedReferenceOfferChallenge, challenge.getPayload()))

	var response encryptedReferenceOfferResponse
	assert.NoError(t, msgpack.Unmarshal(readUntil(t, conn, typeEncryptedReferenceOfferResponse), &response))
	session := slices.Concat([]byte(stranger.Address()), []byte(me.network.Address()))

	agreement, err := ecdhSharedSecret(meUser.PrivateECDHKey, aliceUser.PublicECDHKey)
	assert.NoError(t, err)
	conversation, err := me.dmThreadSecret(meUser.PrivateECDHKey, aliceUser.PublicECDHKey)
	assert.NoError(t, err)
	assert.False(t, opens(agreement, response.Response, session), "the answer was sealed under the bare agreement between two user keys")
	assert.False(t, opens(conversation, response.Response, session))
	assert.False(t, opens(mailboxWrapKey(conversation, currentMailboxEpoch()), response.Response, session))
	assert.True(t, opens(referenceOfferChallengeKey(agreement), response.Response, session))

	// A host, which holds the private half of the key it posed, derives the same key from its side
	hostKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	assert.NoError(t, err)
	fromUser, err := ecdhSharedSecret(meUser.PrivateECDHKey, hostKey.PublicKey().Bytes())
	assert.NoError(t, err)
	fromHost, err := ecdhSharedSecret(hostKey.Bytes(), meUser.PublicECDHKey)
	assert.NoError(t, err)
	assert.Equal(t, referenceOfferChallengeKey(fromUser), referenceOfferChallengeKey(fromHost))
}

// newEncryptedDeviceDatabase opens the database an encrypted device runs on, in a temporary directory with a blobs
// directory beside it, so the device's queries and hooks can be exercised without a network or a managing user.
func newEncryptedDeviceDatabase(t *testing.T) *Bounce {
	t.Helper()
	b := &Bounce{platform: Platform{ConfigDirectory: t.TempDir()}, encrypted: true}
	assert.NoError(t, os.MkdirAll(b.platform.ConfigDirectory+"/blobs", 0700))
	b.openEncryptedDatabase(randomTestBytes(masterKeySize))
	t.Cleanup(func() {
		if db, err := b.database.DB(); err == nil {
			db.Close()
		}
	})
	return b
}

// storedCopy puts a copy on a host directly, in one mailbox, delivered to one peer
func storedCopy(t *testing.T, host *Bounce, peer string, ef encryptedFrame) encryptedFrame {
	t.Helper()
	ef.ID = NewID()
	if len(ef.Slot) == 0 {
		ef.Slot = randomSlot()
	}
	if len(ef.Payload) == 0 {
		ef.Payload = []byte("sealed")
	}
	label := make([]byte, mailboxLabelSize)
	rand.Read(label)
	tag := make([]byte, mailboxTagSize)
	rand.Read(tag)
	ef.Mailboxes = []mailboxEntry{{Label: label, Tag: tag, WrappedDEK: []byte("wrapped")}}
	assert.NoError(t, host.database.Create(&ef).Error)
	host.markFrameDelivered(ef.ID, typeEncryptedFrame, peer)
	return ef
}

func rowsIn(t *testing.T, b *Bounce, table string) int64 {
	t.Helper()
	var n int64
	assert.NoError(t, b.database.Table(table).Count(&n).Error)
	return n
}

// Deleting stored copies takes their mailbox entries and delivery records with them, however many copies the delete
// matches and whichever way they go: expiry, a cleared mailbox, eviction, or a newer draft taking the slot.
func TestDeletingStoredCopiesRemovesTheirMailboxEntriesAndDeliveryRecords(t *testing.T) {
	ed, _ := newEncryptedDevice(t)
	peer := newTestnet().Address()

	// Expiry
	for i := 0; i < 3; i++ {
		storedCopy(t, ed, peer, encryptedFrame{DeleteAt: 1})
	}
	kept := storedCopy(t, ed, peer, encryptedFrame{})
	assert.Equal(t, int64(4), rowsIn(t, ed, "mailbox_entries"))
	assert.Equal(t, int64(4), rowsIn(t, ed, "delivery_records"))
	assert.NoError(t, deleteEach[encryptedFrame](ed.database, "delete_at != 0 AND delete_at <= ?", 1))
	assert.Equal(t, int64(1), rowsIn(t, ed, "encrypted_frames"))
	assert.Equal(t, int64(1), rowsIn(t, ed, "mailbox_entries"))
	assert.Equal(t, int64(1), rowsIn(t, ed, "delivery_records"))

	// A cleared mailbox
	clear := encryptedClearBefore{ID: NewID(), Labels: [][]byte{kept.Mailboxes[0].Label}}
	payload, _ := msgpack.Marshal(clear)
	ed.handleEncryptedClearBefore(peer, payload, false)
	assert.Equal(t, int64(0), rowsIn(t, ed, "encrypted_frames"))
	assert.Equal(t, int64(0), rowsIn(t, ed, "mailbox_entries"))
	assert.Equal(t, int64(0), rowsIn(t, ed, "delivery_records"))

	// Eviction of the untrusted tier
	for i := 0; i < 3; i++ {
		storedCopy(t, ed, peer, encryptedFrame{})
	}
	assert.NoError(t, ed.database.Model(&encryptedFrame{}).Where("1 = 1").Update("saved_at", 1).Error)
	_, evicted := ed.evictUntrustedEncryptedFramesSavedBefore(2)
	assert.Equal(t, int64(3), evicted)
	assert.Equal(t, int64(0), rowsIn(t, ed, "mailbox_entries"))
	assert.Equal(t, int64(0), rowsIn(t, ed, "delivery_records"))
}

// A copy that could not be stored is not acked, so its sender keeps it on offer and a later attempt can succeed
func TestACopyThatCouldNotBeStoredIsNotAcked(t *testing.T) {
	_, alice, bob, _ := createUsersAndGroups(t)
	host, key := newEncryptedDevice(t)
	claimEncryptedDevice(t, bob, host, key)
	waitForOwnerMailboxes(t, host)

	ef := sealForHost(t, bob, signedDM(bob, alice.currentUserID(), "the disk is full"), host)
	payload, _ := msgpack.Marshal(ef)

	assert.NoError(t, host.database.Exec("CREATE TRIGGER refuse_copies BEFORE INSERT ON encrypted_frames BEGIN SELECT RAISE(ABORT, 'disk full'); END").Error)
	host.handleEncryptedFrame(bob.network.Address(), payload, false)
	assert.False(t, host.frameIsDeliveredTo(ef.ID, typeEncryptedFrame, bob.network.Address()), "a copy that was never stored was recorded as held by its sender")
	assert.Equal(t, int64(0), countWhere(t, host, &mailboxEntry{}, "tag = ?", ef.Mailboxes[0].Tag))

	// Once the host can write again the same copy is taken
	assert.NoError(t, host.database.Exec("DROP TRIGGER refuse_copies").Error)
	host.handleEncryptedFrame(bob.network.Address(), payload, false)
	assert.True(t, host.frameIsDeliveredTo(ef.ID, typeEncryptedFrame, bob.network.Address()))
	assert.Equal(t, int64(1), countWhere(t, host, &encryptedFrame{}, "id = ?", ef.ID))
}

func countWhere(t *testing.T, b *Bounce, model interface{}, query string, args ...interface{}) int64 {
	t.Helper()
	var n int64
	assert.NoError(t, b.database.Model(model).Where(query, args...).Count(&n).Error)
	return n
}

// A chunk's blob is shared by every storage request for its hash.  It stays, and the storage counter stays, while
// any of them remains, and goes exactly once with the last of them.
func TestASharedChunkBlobGoesWithItsLastStorageRequest(t *testing.T) {
	ed := newEncryptedDeviceDatabase(t)
	hash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	path := ed.platform.ConfigDirectory + "/blobs/" + hash
	data := []byte("chunk data, eight bytes long... and a bit more")
	assert.NoError(t, os.WriteFile(path, data, 0600))
	encryptedBlobStorageLimitMutex.Lock()
	encryptedBlobStorageSize = int64(len(data))
	encryptedBlobStorageLimitMutex.Unlock()

	first := encryptedChunkStorageRequest{ID: NewID(), Hash: hash, Source: "a", Downloaded: true, FullPath: path}
	second := encryptedChunkStorageRequest{ID: NewID(), Hash: hash, Source: "b", Downloaded: true, FullPath: path}
	assert.NoError(t, ed.database.Create(&first).Error)
	assert.NoError(t, ed.database.Create(&second).Error)

	// One source says it no longer has the chunk: its row goes, the blob does not
	assert.NoError(t, deleteEach[encryptedChunkStorageRequest](ed.database, "hash = ? AND source = ?", hash, "a"))
	_, err := os.Stat(path)
	assert.NoError(t, err, "the blob was removed while another storage request still needed it")
	assert.Equal(t, int64(len(data)), ed.getEncryptedBlobStorageSize())

	// The last row goes, and with it the blob and its share of the counter
	assert.NoError(t, deleteEach[encryptedChunkStorageRequest](ed.database, "hash = ?", hash))
	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err), "the blob outlived its last storage request")
	assert.Equal(t, int64(0), ed.getEncryptedBlobStorageSize())
}

// Evicting the oldest chunk removes all of its storage requests in one go, and the blob and counter follow once
func TestEvictingTheOldestChunkFreesItsBlobOnce(t *testing.T) {
	ed := newEncryptedDeviceDatabase(t)
	older := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	newer := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	total := int64(0)
	for _, hash := range []string{older, newer} {
		path := ed.platform.ConfigDirectory + "/blobs/" + hash
		assert.NoError(t, os.WriteFile(path, []byte(hash[:10]), 0600))
		total += 10
		for _, source := range []string{"a", "b"} {
			at := int64(100)
			if hash == newer {
				at = 200
			}
			assert.NoError(t, ed.database.Create(&encryptedChunkStorageRequest{ID: NewID(), Hash: hash, Source: source, Downloaded: true, DownloadedAt: at, FullPath: path}).Error)
		}
	}
	encryptedBlobStorageLimitMutex.Lock()
	encryptedBlobStorageSize = total
	encryptedBlobStorageLimitMutex.Unlock()

	assert.NoError(t, ed.deleteOldestChunk())
	_, err := os.Stat(ed.platform.ConfigDirectory + "/blobs/" + older)
	assert.True(t, os.IsNotExist(err), "the oldest blob was not removed")
	_, err = os.Stat(ed.platform.ConfigDirectory + "/blobs/" + newer)
	assert.NoError(t, err, "the newer blob was removed")
	assert.Equal(t, int64(10), ed.getEncryptedBlobStorageSize(), "the counter dropped by other than the one blob")
	assert.Equal(t, int64(0), countWhere(t, ed, &encryptedChunkStorageRequest{}, "hash = ?", older))
}

// A request to store a chunk is taken on the same terms whether it arrives on its own or inside a storage reference
// request: from the owner, or addressed to one of the owner's mailboxes, and with everything the delete hook needs.
func TestAChunkStorageRequestIsCheckedTheSameOnBothPaths(t *testing.T) {
	_, _, bob, _ := createUsersAndGroups(t)
	host, key := newEncryptedDevice(t)
	claimEncryptedDevice(t, bob, host, key)
	waitForOwnerMailboxes(t, host)
	var owners ownerMailbox
	assert.NoError(t, host.database.First(&owners).Error)
	stranger := newTestnet().Address()
	elsewhere := make([]byte, mailboxLabelSize)
	rand.Read(elsewhere)

	inside := func(peer string, sr encryptedChunkStorageRequest) {
		payload, _ := msgpack.Marshal(encryptedStorageReferenceRequest{StorageRequests: []encryptedChunkStorageRequest{sr}})
		host.handleEncryptedStorageReferenceRequest(peer, payload, false)
	}
	alone := func(peer string, sr encryptedChunkStorageRequest) {
		payload, _ := msgpack.Marshal(sr)
		host.handleEncryptedChunkStorageRequest(peer, payload, false)
	}
	hashes := []string{
		"1111111111111111111111111111111111111111111111111111111111111111",
		"2222222222222222222222222222222222222222222222222222222222222222",
	}

	// The owner's requests come from a device of his that has proven his key to the host and is not connected now.
	// The host asks a connected source for a chunk as soon as its request is stored, and drops the request when the
	// source answers that it has no such chunk, which is what Bob's own device would answer here.
	var owner authorizedUser
	assert.NoError(t, host.database.First(&owner).Error)
	ownersDevice := newTestnet().Address()
	peerUserKeyMutex.Lock()
	peerUserKeys[ownersDevice] = owner.PublicKey
	peerUserKeyMutex.Unlock()
	t.Cleanup(func() {
		peerUserKeyMutex.Lock()
		delete(peerUserKeys, ownersDevice)
		peerUserKeyMutex.Unlock()
	})
	assert.True(t, host.peerIsOwner(ownersDevice))
	for i, deliver := range []func(string, encryptedChunkStorageRequest){inside, alone} {
		hash := hashes[i]

		// A stranger may not park a chunk in a mailbox the owner cannot read
		deliver(stranger, encryptedChunkStorageRequest{Hash: hash, Labels: [][]byte{elsewhere}})
		assert.Equal(t, int64(0), countWhere(t, host, &encryptedChunkStorageRequest{}, "hash = ?", hash))

		// Nor is a hash that is not a hash ever made into a path
		deliver(ownersDevice, encryptedChunkStorageRequest{Hash: "../" + hash[3:], Labels: [][]byte{owners.Label}})
		assert.Equal(t, int64(0), countWhere(t, host, &encryptedChunkStorageRequest{}, "source = ?", ownersDevice))

		// A stranger may leave one for the owner, and it is not the owner's own
		deliver(stranger, encryptedChunkStorageRequest{Hash: hash, Labels: [][]byte{owners.Label}})
		var fromStranger encryptedChunkStorageRequest
		assert.NoError(t, host.database.First(&fromStranger, "hash = ? AND source = ?", hash, stranger).Error)
		assert.False(t, fromStranger.OwnerRequested)
		assert.Equal(t, host.platform.ConfigDirectory+"/blobs/"+hash, fromStranger.FullPath)

		// The owner's request is marked as such, which is what lets it evict other people's chunks to make room
		deliver(ownersDevice, encryptedChunkStorageRequest{Hash: hash, Labels: [][]byte{elsewhere}})
		var fromOwner encryptedChunkStorageRequest
		assert.NoError(t, host.database.First(&fromOwner, "hash = ? AND source = ?", hash, ownersDevice).Error)
		assert.True(t, fromOwner.OwnerRequested)
		assert.Equal(t, host.platform.ConfigDirectory+"/blobs/"+hash, fromOwner.FullPath)
		assert.NoError(t, deleteEach[encryptedChunkStorageRequest](host.database, "hash = ?", hash))
	}
}
