package chat

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/alecthomas/assert/v2"
	"github.com/zeebo/blake3"
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
	// A stranger posing a challenge learns nothing else: our mailboxes are only told to encrypted devices we know of
	conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	for {
		frameType, _, err := readFrame(conn, true)
		if err != nil {
			break
		}
		assert.NotEqual(t, typeMailboxRequest, frameType, "a stranger was told which mailboxes we can read")
	}

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

// Everything a host stores about a copy is of a known size, and a peer chooses how many mailboxes it asks for or
// clears.  None of that may grow past what the host will hold in a row or bind in a query: an oversized request is
// cut down or refused, never passed to the database to fail there.
func TestAHostBoundsWhatAPeerCanMakeItStoreOrLookUp(t *testing.T) {
	_, alice, bob, _ := createUsersAndGroups(t)
	host, key := newEncryptedDevice(t)
	claimEncryptedDevice(t, bob, host, key)
	waitForOwnerMailboxes(t, host)
	stranger := newTestnet().Address()

	manyLabels := func(n int) [][]byte {
		labels := make([][]byte, 0, n)
		for i := 0; i < n; i++ {
			label := make([]byte, mailboxLabelSize)
			rand.Read(label)
			labels = append(labels, label)
		}
		return labels
	}

	// More mailboxes than a query can bind: the request is cut down, and answering it still works
	request, _ := msgpack.Marshal(mailboxRequest{Labels: manyLabels(40000)})
	host.handleMailboxRequest(stranger, request, false)
	assert.Equal(t, maximumMailboxLabelsPerPeer, len(host.peerLabels(stranger)))
	assert.Equal(t, 0, len(host.hostOfferFor(stranger).References))

	// Clearing more mailboxes than a query can bind is done a batch at a time, and still clears what it names
	good := sealForHost(t, bob, signedDM(bob, alice.currentUserID(), "to be cleared"), host)
	payload, _ := msgpack.Marshal(good)
	host.handleEncryptedFrame(bob.network.Address(), payload, false)
	assert.Equal(t, int64(1), countWhere(t, host, &encryptedFrame{}, "id = ?", good.ID))
	clear, _ := msgpack.Marshal(encryptedClearBefore{ID: NewID(), Labels: append(manyLabels(40000), good.Mailboxes[0].Label)})
	host.handleEncryptedClearBefore(stranger, clear, false)
	assert.Equal(t, int64(0), countWhere(t, host, &encryptedFrame{}, "id = ?", good.ID))

	// A copy is refused when it is in more mailboxes than any frame belongs in, or carries a wrapped key that is
	// not the size of one
	refused := func(change func(*encryptedFrame)) bool {
		ef := sealForHost(t, bob, signedDM(bob, alice.currentUserID(), "malformed"), host)
		change(ef)
		payload, _ := msgpack.Marshal(ef)
		host.handleEncryptedFrame(bob.network.Address(), payload, false)
		return countWhere(t, host, &encryptedFrame{}, "id = ?", ef.ID) == 0
	}
	assert.False(t, refused(func(ef *encryptedFrame) {}), "an untouched copy is stored")
	assert.True(t, refused(func(ef *encryptedFrame) {
		for _, label := range manyLabels(maximumMailboxesPerCopy) {
			ef.Mailboxes = append(ef.Mailboxes, mailboxEntry{Label: label, Tag: ef.Mailboxes[0].Tag, WrappedDEK: ef.Mailboxes[0].WrappedDEK})
		}
	}))
	assert.True(t, refused(func(ef *encryptedFrame) {
		ef.Mailboxes[0].WrappedDEK = make([]byte, maximumWrappedDEKBytes+1)
	}))
	assert.True(t, refused(func(ef *encryptedFrame) { ef.Slot = []byte("short") }))
	assert.True(t, refused(func(ef *encryptedFrame) { ef.DeleteAt = -1 }))

	// And the same shape is required of a copy however it gets to the table
	direct := sealForHost(t, bob, signedDM(bob, alice.currentUserID(), "direct"), host)
	direct.Mailboxes[0].Label = []byte("not a label")
	assert.Error(t, host.database.Create(direct).Error)
}

// The keys a device is claimed with, and any it is later re-keyed to, are what every proof of ownership and every
// management action is checked against.  A key that could never check anything is refused, so a device cannot be
// left unmanageable by one, and every action is answered so the manager knows where it stands.
func TestManagementKeysAreValidatedAndEveryActionIsAnswered(t *testing.T) {
	host, setup := newEncryptedDevice(t)
	manager := newTestnet()
	conn, err := manager.Dial(host.network.Address())
	assert.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	signingPublic, signingPrivate, err := ed25519.GenerateKey(rand.Reader)
	assert.NoError(t, err)
	userKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	assert.NoError(t, err)

	claim := func(request encryptedDeviceManagementRequest) bool {
		assert.NoError(t, writeFrame(conn, typeEncryptedDeviceManagementRequest, request.getPayload()))
		var response encryptedDeviceManagementResponse
		assert.NoError(t, msgpack.Unmarshal(readUntil(t, conn, typeEncryptedDeviceManagementResponse), &response))
		return response.Accepted
	}
	assert.False(t, claim(encryptedDeviceManagementRequest{Secret: setup, SigningKey: signingPublic, Pubkey: []byte("not a key")}))
	assert.False(t, host.encryptedManagerProvisioned(), "a device was claimed with a key nobody could ever prove they hold")
	assert.True(t, claim(encryptedDeviceManagementRequest{Secret: setup, SigningKey: signingPublic, Pubkey: userKey.PublicKey().Bytes()}))

	act := func(action []byte) encryptedDeviceManagementActionResponse {
		med := manageEncryptedDevice{ID: NewID(), Action: action, Signature: ed25519.Sign(signingPrivate, action)}
		assert.NoError(t, writeFrame(conn, typeManageEncryptedDevice, med.getPayload()))
		var response encryptedDeviceManagementActionResponse
		assert.NoError(t, msgpack.Unmarshal(readUntil(t, conn, typeEncryptedDeviceManagementActionResponse), &response))
		assert.Equal(t, med.ID, response.ID)
		return response
	}
	changeKeyTo := func(keys keySet) []byte {
		data, err := msgpack.Marshal(&keys)
		assert.NoError(t, err)
		action, err := msgpack.Marshal(&encryptedDeviceManagementAction{ActionType: actionTypeChangeManagementKey, Data: data})
		assert.NoError(t, err)
		return action
	}
	storedKeys := func() authorizedUser {
		var au authorizedUser
		assert.NoError(t, host.database.First(&au).Error)
		return au
	}

	// An action that is not an action at all is answered, authorized by its signature and not applied
	response := act([]byte{0xc1, 0xc1, 0xc1})
	assert.True(t, response.Authorized)
	assert.False(t, response.Applied)

	// A change to keys that could verify nothing is refused, and the device stays on the keys it had
	newSigningPublic, _, err := ed25519.GenerateKey(rand.Reader)
	assert.NoError(t, err)
	newUserKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	assert.NoError(t, err)
	for _, unusable := range []keySet{
		{PublicECDSAKey: []byte("short"), PublicECDHKey: newUserKey.PublicKey().Bytes()},
		{PublicECDSAKey: newSigningPublic, PublicECDHKey: []byte("short")},
		{},
	} {
		response = act(changeKeyTo(unusable))
		assert.True(t, response.Authorized)
		assert.False(t, response.Applied)
		assert.Equal(t, []byte(signingPublic), storedKeys().SigningKey, "the device was re-keyed to a key that cannot manage it")
		assert.Equal(t, int64(0), rowsIn(t, host, "old_keys"))
	}

	// A real change is applied, and the old keys are remembered
	response = act(changeKeyTo(keySet{PublicECDSAKey: newSigningPublic, PublicECDHKey: newUserKey.PublicKey().Bytes()}))
	assert.True(t, response.Authorized)
	assert.True(t, response.Applied)
	assert.Equal(t, []byte(newSigningPublic), storedKeys().SigningKey)
	assert.Equal(t, newUserKey.PublicKey().Bytes(), storedKeys().PublicKey)
	assert.Equal(t, int64(1), rowsIn(t, host, "old_keys"))
}

func TestClaimingADeviceNeedsAProfileAndARecentRequest(t *testing.T) {
	host, setup := newEncryptedDevice(t)

	// Without a profile there are no keys to claim a device with
	nobody := newBounce()
	t.Cleanup(nobody.Shutdown)
	assert.Error(t, nobody.RequestToManageEncryptedDevice(host.network.Address()+":"+setup))
	assert.False(t, host.encryptedManagerProvisioned())

	// An acceptance is only taken from a device we asked recently
	bob := newBounceUser("Bob")
	t.Cleanup(bob.Shutdown)
	accepted, _ := msgpack.Marshal(encryptedDeviceManagementResponse{Accepted: true})
	wantToManageMutex.Lock()
	wantToManage[host.network.Address()] = time.Now().Add(-wantToManageWindow - time.Second)
	wantToManageMutex.Unlock()
	bob.handleEncryptedDeviceManagementResponse(host.network.Address(), accepted, false)
	assert.Equal(t, int64(0), rowsIn(t, bob, "encrypted_sync_devices"), "an acceptance was taken from a device asked too long ago")

	// Asking again starts a new window, and clears out requests that have run out
	stale := newTestnet().Address()
	wantToManageMutex.Lock()
	wantToManage[stale] = time.Now().Add(-wantToManageWindow - time.Second)
	wantToManageMutex.Unlock()
	claimEncryptedDevice(t, bob, host, setup)
	wantToManageMutex.Lock()
	_, kept := wantToManage[stale]
	wantToManageMutex.Unlock()
	assert.False(t, kept)
}

// What a host keeps in memory about a peer lasts as long as the peer's connection.  When the peer comes back it is
// challenged again and says again what it can read.
func TestAHostForgetsPeersThatHaveGone(t *testing.T) {
	bob := newBounceUser("Bob")
	t.Cleanup(bob.Shutdown)
	host, key := newEncryptedDevice(t)
	claimEncryptedDevice(t, bob, host, key)
	waitForOwnerMailboxes(t, host)
	assert.True(t, len(host.peerLabels(bob.network.Address())) > 0)

	// Still connected: housekeeping leaves it alone
	host.pruneEncryptedDevice()
	assert.True(t, host.peerIsOwner(bob.network.Address()))
	assert.True(t, len(host.peerLabels(bob.network.Address())) > 0)

	disconnectFrom(t, bob, host)
	host.pruneEncryptedDevice()
	assert.False(t, host.peerIsOwner(bob.network.Address()), "a peer that left is still taken for the owner")
	assert.Equal(t, 0, len(host.peerLabels(bob.network.Address())))
	referenceOfferChallengeMutex.Lock()
	_, challengeKept := referenceOfferKeyMap[bob.network.Address()]
	referenceOfferChallengeMutex.Unlock()
	assert.False(t, challengeKept, "the private key of a challenge outlived the peer it was posed to")

	// Back again, it proves itself and names its mailboxes afresh
	connectTo(t, bob, host.network.Address())
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !(host.peerIsOwner(bob.network.Address()) && len(host.peerLabels(bob.network.Address())) > 0) {
		time.Sleep(25 * time.Millisecond)
	}
	assert.True(t, host.peerIsOwner(bob.network.Address()))
	assert.True(t, len(host.peerLabels(bob.network.Address())) > 0)
}

// A frame that reaches a device live through an encrypted device is treated like one that came from any other peer:
// the host is recorded as holding it, so it is not offered straight back, and it is passed on.
func TestAFramePushedByAHostIsRecordedAndPassedOn(t *testing.T) {
	quickReferenceRetries(t)
	alice, bob := connectUsers(t)
	host, key := newEncryptedDevice(t)
	claimEncryptedDevice(t, bob, host, key)
	waitForOwnerMailboxes(t, host)
	waitForUserToKnowEncryptedDevice(t, alice, bob.currentUserID(), host.network.Address())
	connectTo(t, alice, host.network.Address())

	// Bob can only be reached through his host, which pushes him what Alice leaves there
	disconnectFrom(t, bob, alice)
	assert.NoError(t, alice.SendDirectMessage(bob.currentUserID(), "pushed through the host", nil, NilID))
	var dm directMessage
	assert.NoError(t, alice.database.First(&dm, "text = ?", "pushed through the host").Error)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !bob.frameIsDeliveredTo(dm.ID, typeDirectMessage, host.network.Address()) {
		time.Sleep(25 * time.Millisecond)
	}
	assert.Equal(t, int64(1), countWhere(t, bob, &directMessage{}, "id = ?", dm.ID))
	assert.True(t, bob.frameIsDeliveredTo(dm.ID, typeDirectMessage, host.network.Address()), "the host a frame came from was not recorded as holding it")

	// So the frame is not on offer to the host it came from
	for _, reference := range bob.getReferenceOfferFor(host.network.Address()).References {
		assert.NotEqual(t, dm.ID, reference.FrameID)
	}
}

// fileWithAnUnofferedChunk gives b a file that author sent to a group, with one chunk that no device has offered, and
// returns the file and the hash its chunk has on an encrypted device
func fileWithAnUnofferedChunk(t *testing.T, b *Bounce, author *Bounce, groupID ID) (*file, string) {
	t.Helper()
	plainHash := "1111111111111111111111111111111111111111111111111111111111111111"
	encryptedHash := "2222222222222222222222222222222222222222222222222222222222222222"
	f := &file{
		Nonce:              newNonce(),
		Name:               "holiday.jpg",
		Type:               fileTypeMessageAttachment,
		Hash:               "3333333333333333333333333333333333333333333333333333333333333333",
		Size:               10,
		HashList:           plainHash,
		EncryptedHashList:  encryptedHash,
		EncryptedDeviceKey: make([]byte, 32),
		Scope:              scopeGroup,
		Destination:        groupID,
		Author:             author.currentUserID(),
		Timestamp:          time.Now().Unix(),
	}
	f.ID = f.generateID()
	f.Path = b.platform.ConfigDirectory + "/blobs/" + f.ID.String()
	f.LocalKey = newLocalKey()
	var err error
	f.OriginalPayload, err = msgpack.Marshal(f)
	assert.NoError(t, err)
	signedFile := author.createSignedContainer(typeFile, f.OriginalPayload)
	f.Signature = signedFile.Signature
	f.Signer = signedFile.Signer
	assert.NoError(t, b.database.Create(f).Error)
	assert.NoError(t, b.database.Create(&chunk{ID: NewID(), FileID: f.ID, Hash: plainHash, EncryptedHash: encryptedHash}).Error)
	assert.Equal(t, int64(0), countWhere(t, b, &chunkOffer{}, "hash = ?", plainHash))
	return f, encryptedHash
}

// encryptedChunkOfferFrom builds the offer author's device passes on when author's encrypted device holds a chunk
func encryptedChunkOfferFrom(t *testing.T, author *Bounce, encryptedHash string) *encryptedChunkOffer {
	t.Helper()
	eco := &encryptedChunkOffer{
		ID:        NewID(),
		Hash:      encryptedHash,
		Location:  fakeEncryptedDeviceOf(t, author.currentUserID()).Address(),
		Timestamp: time.Now().Unix(),
	}
	var err error
	eco.OriginalPayload, err = msgpack.Marshal(eco)
	assert.NoError(t, err)
	sc := author.createSignedContainer(typeEncryptedChunkOffer, eco.OriginalPayload)
	eco.Signature = sc.Signature
	eco.Signer = sc.Signer
	return eco
}

// An encrypted device's offer of a chunk is kept on the strength of the file alone.  A plain offer for the same chunk
// would come from a device that holds the file, and an encrypted device is there for when none of those is online.
func TestAnEncryptedChunkOfferIsKeptWithoutAPlainOfferForItsChunk(t *testing.T) {
	me, alice, _, groupID := createUsersAndGroups(t)

	// A file Alice sent to the group, with one chunk that no device has offered us, and the news from Alice's device
	// that her encrypted device holds the chunk
	f, encryptedHash := fileWithAnUnofferedChunk(t, me, alice, groupID)
	eco := encryptedChunkOfferFrom(t, alice, encryptedHash)
	handled, firstTimeSeen := me.handleEncryptedChunkOffer(alice.network.Address(), eco.getPayload(), false)

	// It is kept, and handed back to be acked and passed on to whoever may have the file
	var kept encryptedChunkOffer
	assert.NoError(t, me.database.Take(&kept, "id = ?", eco.ID).Error, "the offer was dropped for want of a plain offer")
	assert.Equal(t, f.ID, kept.FileID)
	assert.Equal(t, f.Author, kept.Author)
	assert.Equal(t, f.Scope, kept.Scope)
	assert.Equal(t, f.Destination, kept.Destination)
	assert.True(t, handled != nil && handled.getID() == eco.ID)
	assert.True(t, firstTimeSeen)
}

// An encrypted chunk offer is acked once each time it arrives, and whoever sent it is recorded as holding it from the
// first time, so that it is not offered back to them
func TestAnEncryptedChunkOfferIsAckedOnceAndItsSenderIsRecorded(t *testing.T) {
	me, alice, _, groupID := createUsersAndGroups(t)
	_, encryptedHash := fileWithAnUnofferedChunk(t, me, alice, groupID)
	eco := encryptedChunkOfferFrom(t, alice, encryptedHash)

	// A device relays the offer to us
	relay := newTestnet()
	conn, err := relay.Dial(me.network.Address())
	assert.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	acksOfTheOffer := func() int {
		acks := 0
		conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		for {
			frameType, payload, err := readFrame(conn, true)
			if err != nil {
				return acks
			}
			if frameType != typeAck {
				continue
			}
			var a ack
			assert.NoError(t, msgpack.Unmarshal(payload, &a))
			for _, reference := range a.References {
				if reference.FrameID == eco.ID && reference.Type == typeEncryptedChunkOffer {
					acks++
				}
			}
		}
	}

	assert.NoError(t, writeFrame(conn, typeEncryptedChunkOffer, eco.getPayload()))
	assert.Equal(t, 1, acksOfTheOffer())
	assert.Equal(t, int64(1), countWhere(t, me, &encryptedChunkOffer{}, "id = ?", eco.ID))
	assert.True(t, me.frameIsDeliveredTo(eco.ID, typeEncryptedChunkOffer, relay.Address()), "the device that sent the offer was not recorded as holding it")

	// The same offer again is acked again, once
	assert.NoError(t, writeFrame(conn, typeEncryptedChunkOffer, eco.getPayload()))
	assert.Equal(t, 1, acksOfTheOffer(), "an offer we already hold was acked more than once")
}

// An encrypted device fetches the chunks a peer asked it to store one at a time: a storage request starts it, and
// each chunk that arrives is what has it ask for the next.
func TestAHostFetchesAPeersChunksOneAfterAnother(t *testing.T) {
	quickReferenceRetries(t)
	_, bob := connectUsers(t)
	host, key := newEncryptedDevice(t)
	claimEncryptedDevice(t, bob, host, key)
	waitForOwnerMailboxes(t, host)
	var owners ownerMailbox
	assert.NoError(t, host.database.First(&owners).Error)

	// A contact's device leaves three chunks for Bob
	source := newTestnet()
	conn, err := source.Dial(host.network.Address())
	assert.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	chunks := map[string][]byte{}
	for _, data := range [][]byte{[]byte("the first chunk"), []byte("the second chunk"), []byte("the third chunk")} {
		hash := hashString(blake3.Sum256(data))
		chunks[hash] = data
		payload, err := msgpack.Marshal(encryptedChunkStorageRequest{Hash: hash, Labels: [][]byte{owners.Label}})
		assert.NoError(t, err)
		assert.NoError(t, writeFrame(conn, typeEncryptedChunkStorageRequest, payload))
	}
	nextChunkAskedFor := func() []byte {
		var asked chunkRequest
		assert.NoError(t, msgpack.Unmarshal(readUntil(t, conn, typeChunkRequest), &asked))
		data, waiting := chunks[asked.Hash]
		assert.True(t, waiting, "asked for a chunk that is not waiting to be fetched")
		delete(chunks, asked.Hash)
		return data
	}

	// With all three requests in, the device has asked for one chunk and asks for no other while it is on its way
	first := nextChunkAskedFor()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && countWhere(t, host, &encryptedChunkStorageRequest{}, "source = ?", source.Address()) < 3 {
		time.Sleep(25 * time.Millisecond)
	}
	assert.Equal(t, int64(3), countWhere(t, host, &encryptedChunkStorageRequest{}, "source = ?", source.Address()))
	conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	for {
		frameType, _, err := readFrame(conn, true)
		if err != nil {
			break
		}
		assert.NotEqual(t, typeChunkRequest, frameType, "a second chunk was asked for before the first arrived")
	}

	assert.NoError(t, writeFrame(conn, typeChunk, first))
	assert.NoError(t, writeFrame(conn, typeChunk, nextChunkAskedFor()))
	assert.NoError(t, writeFrame(conn, typeChunk, nextChunkAskedFor()))

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && countWhere(t, host, &encryptedChunkStorageRequest{}, "downloaded = ?", true) < 3 {
		time.Sleep(25 * time.Millisecond)
	}
	assert.Equal(t, int64(3), countWhere(t, host, &encryptedChunkStorageRequest{}, "downloaded = ?", true))
}

// A handler does not wait on the peer it is answering.  A peer that has stopped reading has a full queue, a send to
// it waits for as long as the queue stays full, and a handler that waited with it would hold its slot, and with
// enough of them everyone else's, for the whole of that time.
func TestHandlersDoNotWaitOnAPeerThatHasStoppedReading(t *testing.T) {
	_, bob := connectUsers(t)
	host, key := newEncryptedDevice(t)
	claimEncryptedDevice(t, bob, host, key)

	// stoppedReading gives an engine a connected peer whose queue is full and that nothing is writing to
	stoppedReading := func(b *Bounce) string {
		peer := newTestnet().Address()
		ours, _ := net.Pipe()
		rd := b.getRemoteDevice(peer)
		rd.Lock()
		rd.sockets = []*socket{{underlying: ours, alive: true, lastRead: time.Now(), closer: make(chan struct{})}}
		rd.Unlock()
		for len(rd.messages) < cap(rd.messages) {
			rd.messages <- &keepAlive{}
		}
		t.Cleanup(func() { drain(rd.messages) })
		return peer
	}
	hostPeer := stoppedReading(host)
	bobPeer := stoppedReading(bob)

	clear, err := msgpack.Marshal(encryptedClearBefore{ID: NewID(), Labels: [][]byte{randomTestBytes(mailboxLabelSize)}})
	assert.NoError(t, err)
	_, challengeKey := eroChallengeKey()
	// In this order: the mailbox request is what gives the device something to answer the request after it with
	handlers := []struct {
		of     string
		handle func()
	}{
		{"a clear before", func() {
			host.handleEncryptedClearBefore(hostPeer, clear, false)
		}},
		{"a mailbox request", func() {
			host.handleMailboxRequest(hostPeer, (&mailboxRequest{Labels: [][]byte{randomTestBytes(mailboxLabelSize)}}).getPayload(), false)
		}},
		{"a request for a storage reference offer", func() {
			host.handleRequestECRO(hostPeer, nil, false)
		}},
		{"a request for the management key hash", func() {
			host.handleGetManagementKeyHash(hostPeer, nil, false)
		}},
		{"a reference offer challenge", func() {
			bob.handleEncryptedReferenceOfferChallenge(bobPeer, (&encryptedReferenceOfferChallenge{Key: challengeKey, Challenge: randomTestBytes(32)}).getPayload(), false)
		}},
		{"an encrypted catch up", func() {
			bob.handleEncryptedCatchUp(bobPeer, (&encryptedCatchUp{}).getPayload(), false)
		}},
		{"an encrypted chunk offer for a chunk we do not know", func() {
			unknown := encryptedChunkOfferFrom(t, bob, "4444444444444444444444444444444444444444444444444444444444444444")
			bob.handleEncryptedChunkOffer(bobPeer, unknown.getPayload(), false)
		}},
	}
	for _, handler := range handlers {
		returned := make(chan struct{})
		go func() {
			handler.handle()
			close(returned)
		}()
		select {
		case <-returned:
		case <-time.After(2 * time.Second):
			t.Errorf("the handler for %s waited on a peer that is not reading", handler.of)
		}
	}
}

// An encrypted device is renamed by an update that carries its address and its new name with a colon between them.
// The address ends at the first colon, so the name is free to have colons of its own.
func TestAnEncryptedDeviceIsGivenANameWithAColonInIt(t *testing.T) {
	_, bob := connectUsers(t)
	host, key := newEncryptedDevice(t)
	claimEncryptedDevice(t, bob, host, key)

	var esd encryptedSyncDevice
	assert.NoError(t, bob.database.First(&esd, "address = ?", host.network.Address()).Error)
	assert.NoError(t, bob.RenameDevice(esd.ID, "attic: the old laptop"))

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		assert.NoError(t, bob.database.First(&esd, "address = ?", host.network.Address()).Error)
		if esd.Name == "attic: the old laptop" {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	assert.Equal(t, "attic: the old laptop", esd.Name)
}
