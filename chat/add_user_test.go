package chat

import (
	"strings"
	"testing"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/alecthomas/assert/v2"
)

func makeMutuallySignedAddUser(t *testing.T, offerBounce, requesterBounce *Bounce) (addUser, []byte) {
	t.Helper()
	offerUser, ok := offerBounce.currentUser()
	assert.True(t, ok)
	requesterUser, ok := requesterBounce.currentUser()
	assert.True(t, ok)
	offerBytes, err := msgpack.Marshal(offerUser)
	assert.NoError(t, err)
	requesterBytes, err := msgpack.Marshal(requesterUser)
	assert.NoError(t, err)

	au := addUser{
		Nonce:           newNonce(),
		Xor:             xor(offerUser.ID, requesterUser.ID),
		Timestamp:       time.Now().Unix(),
		OfferUser:       offerBytes,
		RequesterUser:   requesterBytes,
		OfferDevice:     offerBounce.network.Address(),
		RequesterDevice: requesterBounce.network.Address(),
	}
	digest := addUserSignatureDigest(au.Nonce, au.Timestamp, au.OfferUser, au.RequesterUser, au.OfferDevice, au.RequesterDevice)
	au.OfferSignature = offerBounce.network.Sign(digest)
	au.RequesterSignature = requesterBounce.network.Sign(digest)
	au.ID = au.generateID()
	payload, err := msgpack.Marshal(&au)
	assert.NoError(t, err)
	return au, payload
}

func TestAddUserCannotBeReMintedOrDeliveredByUnauthorizedPeer(t *testing.T) {
	me := newBounceUser("Me")
	t.Cleanup(me.Shutdown)
	mallory := newBounceUser("Mallory")
	t.Cleanup(mallory.Shutdown)
	carol := newBounceUser("Carol")
	t.Cleanup(carol.Shutdown)

	// Complete a real friend-add handshake between me (offerer) and mallory (requester).
	assert.NoError(t, mallory.RequestToAddUser(me.GetNewAddUserString()))
	var origAU addUser
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := me.database.Where("xor = ?", xor(me.currentUserID(), mallory.currentUserID())).First(&origAU).Error; err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	assert.NotEqual(t, NilID, origAU.ID)

	// Mallory cannot unilaterally re-mint origAU with a new nonce (even with her own fresh signature).
	reminted := origAU
	reminted.Nonce = newNonce()
	remintedDigest := addUserSignatureDigest(reminted.Nonce, reminted.Timestamp, reminted.OfferUser, reminted.RequesterUser, reminted.OfferDevice, reminted.RequesterDevice)
	reminted.RequesterSignature = mallory.network.Sign(remintedDigest)
	reminted.ID = reminted.generateID()
	remintedBytes, err := msgpack.Marshal(&reminted)
	assert.NoError(t, err)
	br, accepted := me.handleAddUser(mallory.network.Address(), remintedBytes, false)
	assert.False(t, accepted)
	assert.True(t, br == nil)

	// Mallory cannot unilaterally re-mint origAU with a modified OfferUser record (e.g. padded Images).
	var paddedMe user
	assert.NoError(t, msgpack.Unmarshal(origAU.OfferUser, &paddedMe))
	paddedMe.Images = NewID().String()
	paddedBytes, err := msgpack.Marshal(paddedMe)
	assert.NoError(t, err)
	padded := origAU
	padded.Nonce = newNonce()
	padded.OfferUser = paddedBytes
	paddedDigest := addUserSignatureDigest(padded.Nonce, padded.Timestamp, padded.OfferUser, padded.RequesterUser, padded.OfferDevice, padded.RequesterDevice)
	padded.RequesterSignature = mallory.network.Sign(paddedDigest)
	padded.ID = padded.generateID()
	paddedBytesFrame, err := msgpack.Marshal(&padded)
	assert.NoError(t, err)
	br, accepted = me.handleAddUser(mallory.network.Address(), paddedBytesFrame, false)
	assert.False(t, accepted)
	assert.True(t, br == nil)

	// A completely valid mutually-signed addUser delivered by an unrelated stranger onion must be rejected.
	_, carolPayload := makeMutuallySignedAddUser(t, me, carol)
	strangerAddr := bnc01NewOnion().addr()
	br, accepted = me.handleAddUser(strangerAddr, carolPayload, false)
	assert.False(t, accepted)
	assert.True(t, br == nil)
	var carolRow user
	assert.Error(t, me.database.Where("id = ?", carol.currentUserID()).First(&carolRow).Error)

	// The exact same frame delivered by Carol's own device is accepted.
	br, accepted = me.handleAddUser(carol.network.Address(), carolPayload, false)
	assert.True(t, accepted)
	assert.True(t, br != nil)
	assert.NoError(t, me.database.Where("id = ?", carol.currentUserID()).First(&carolRow).Error)
}

func TestAddUserBlockedCounterpartyAndDuplicatePairSuppression(t *testing.T) {
	me := newBounceUser("Me")
	t.Cleanup(me.Shutdown)
	mallory := newBounceUser("Mallory")
	t.Cleanup(mallory.Shutdown)

	// Pair a sync device first, then take it offline so we can test sync-delivered addUser for a blocked contact.
	meSyncUI := &syncDeviceTestUI{testUI: newTestUI()}
	meSync := newBounceWithUI(meSyncUI)
	t.Cleanup(meSync.Shutdown)
	t.Cleanup(func() {
		setWaitingForInitialSyncFrom("")
		initialSyncAnnounced.Store(false)
	})
	assert.NoError(t, meSync.RequestToSync(me.GetNewSyncString()))
	awaitSyncDeviceCall(t, meSyncUI, "SyncDeviceRequestAccepted")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, found := me.getDeviceFromAddress(meSync.network.Address()); found {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	hostsMutex.Lock()
	delete(hosts, meSync.network.Address())
	hostsMutex.Unlock()
	sever(t, me, meSync)
	meSync.networkOffline()

	// Initial mutually-signed addUser between me and mallory.
	origAU, origPayload := makeMutuallySignedAddUser(t, me, mallory)
	br, accepted := me.handleAddUser(mallory.network.Address(), origPayload, false)
	assert.True(t, accepted)
	assert.True(t, br != nil)

	// A second distinct mutually-signed addUser for the same existing pair must not create a duplicate row.
	_, secondPayload := makeMutuallySignedAddUser(t, me, mallory)
	br, accepted = me.handleAddUser(mallory.network.Address(), secondPayload, false)
	assert.False(t, accepted)
	assert.True(t, br == nil)

	pairXor := xor(me.currentUserID(), mallory.currentUserID())
	var auCount int64
	assert.NoError(t, me.database.Model(&addUser{}).Where("xor = ?", pairXor).Count(&auCount).Error)
	assert.Equal(t, int64(1), auCount)

	// Block Mallory, wait for the async updateDM to persist, and remove the existing add_users row
	// so the block check itself is exercised.
	assert.NoError(t, me.BlockUser(mallory.currentUserID()))
	t.Cleanup(func() {
		blockedUsersMutex.Lock()
		delete(blockedUsers, mallory.currentUserID())
		blockedUsersMutex.Unlock()
	})
	var blockUD updateDM
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := me.database.Where("target = ? AND type = ?", pairXor, updateDMTypeSetBlocked).First(&blockUD).Error; err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	assert.NotEqual(t, NilID, blockUD.ID)
	assert.True(t, blockedUser(mallory.currentUserID()))
	assert.NoError(t, me.database.Delete(&origAU).Error)

	// Re-delivering from Mallory's device while Mallory is blocked must be dropped without storing.
	br, accepted = me.handleAddUser(mallory.network.Address(), origPayload, false)
	assert.False(t, accepted)
	assert.True(t, br == nil)
	assert.NoError(t, me.database.Model(&addUser{}).Where("xor = ?", pairXor).Count(&auCount).Error)
	assert.Equal(t, int64(0), auCount)

	// Delivering the historical addUser from our own sync device is allowed (initial sync) so the user row
	// exists for the block updateDM to apply, and never unblocks a blocked user.
	br, accepted = meSync.handleAddUser(me.network.Address(), origPayload, false)
	assert.True(t, accepted)
	assert.True(t, br != nil)
	_, accepted = meSync.handleUpdateDM(me.network.Address(), blockUD.getPayload(), false)
	assert.True(t, accepted)
	var syncedMallory user
	assert.NoError(t, meSync.database.Where("id = ?", mallory.currentUserID()).First(&syncedMallory).Error)
	assert.True(t, syncedMallory.Blocked, "a blocked counterparty learned via sync addUser + block updateDM must be blocked")

	// Even when our own sync device re-delivers addUser for an already-blocked user, they stay blocked.
	br, accepted = me.handleAddUser(meSync.network.Address(), origPayload, false)
	assert.True(t, accepted)
	assert.True(t, br != nil)
	var meMallory user
	assert.NoError(t, me.database.Where("id = ?", mallory.currentUserID()).First(&meMallory).Error)
	assert.True(t, meMallory.Blocked, "sync-delivered addUser must never unblock an already-blocked user")

	// getAddUsersToOffer must return the addUser ID without failing under Select("add_users.id").
	refs := meSync.getAddUsersToOffer("other-sync-device", meSync.currentUserID())
	foundOrig := false
	for _, ref := range refs {
		if ref.Type == typeAddUser && ref.FrameID == origAU.ID {
			foundOrig = true
			break
		}
	}
	assert.True(t, foundOrig)
}

func TestValidUserRecordBoundsImagesEncryptedDevicesAndSize(t *testing.T) {
	me := newBounceUser("Me")
	t.Cleanup(me.Shutdown)
	cu, ok := me.currentUser()
	assert.True(t, ok)

	// Images history cap
	imgIDs := make([]string, maximumUserImages)
	for i := range imgIDs {
		imgIDs[i] = NewID().String()
	}
	cu.Images = strings.Join(imgIDs, ",")
	assert.NoError(t, validUserRecord(cu))
	cu.Images = strings.Join(append(imgIDs, NewID().String()), ",")
	assert.Error(t, validUserRecord(cu))
	cu.Images = ""

	// EncryptedDevices cap
	addrs := make([]string, maximumEncryptedDevicesPerUser)
	for i := range addrs {
		addrs[i] = bnc01NewOnion().addr()
	}
	cu.EncryptedDevices = strings.Join(addrs, ",")
	assert.NoError(t, validUserRecord(cu))
	cu.EncryptedDevices = strings.Join(append(addrs, bnc01NewOnion().addr()), ",")
	assert.Error(t, validUserRecord(cu))
	cu.EncryptedDevices = ""

	// PublicECDHKey length bound
	cu.PublicECDHKey = make([]byte, 32)
	assert.NoError(t, validUserRecord(cu))
	cu.PublicECDHKey = make([]byte, 64)
	assert.Error(t, validUserRecord(cu))
	cu.PublicECDHKey = nil

	// Oversized OfferUser / RequesterUser rejected before unmarshalling in handleAddUser
	oversized := addUser{
		Nonce:           newNonce(),
		Xor:             NewID(),
		Timestamp:       time.Now().Unix(),
		OfferUser:       make([]byte, maximumAddUserRecordSize+1),
		RequesterUser:   []byte("small"),
		OfferDevice:     me.network.Address(),
		RequesterDevice: bnc01NewOnion().addr(),
	}
	oversized.ID = oversized.generateID()
	oversizedBytes, err := msgpack.Marshal(&oversized)
	assert.NoError(t, err)
	br, accepted := me.handleAddUser(oversized.RequesterDevice, oversizedBytes, false)
	assert.False(t, accepted)
	assert.True(t, br == nil)
}
