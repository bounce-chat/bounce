package chat

import (
	"encoding/binary"
	"sync"
	"testing"
	"time"

	"github.com/alecthomas/assert/v2"
)

// Blocking a user is meant to be a hard boundary: nothing we write reaches any device of theirs, and a device of
// theirs that dials in gets no socket to sit on.  Their encrypted devices count, even though those are not device
// rows but addresses we learned from their user record.

// quickReferenceRetries keeps the reference offer loop from sleeping half a minute per retry while a peer that
// will never ack is being tested
func quickReferenceRetries(t *testing.T) {
	t.Helper()
	old := referenceRetrySeconds.Load()
	referenceRetrySeconds.Store(1)
	t.Cleanup(func() { referenceRetrySeconds.Store(old) })
}

// fakeEncryptedDeviceOf registers a bare test network node as an encrypted device belonging to a user, the way a
// user record announcing it would
func fakeEncryptedDeviceOf(t *testing.T, owner ID) *testnet {
	t.Helper()
	host := newTestnet()
	encryptedDeviceCacheMutex.Lock()
	encryptedDeviceCache[host.Address()] = owner
	encryptedDeviceCacheMutex.Unlock()
	t.Cleanup(func() {
		encryptedDeviceCacheMutex.Lock()
		delete(encryptedDeviceCache, host.Address())
		encryptedDeviceCacheMutex.Unlock()
	})
	return host
}

// dialAndCount has a node dial b and reports how many sockets b keeps for it afterwards
func dialAndCount(t *testing.T, node *testnet, b *Bounce) int {
	t.Helper()
	conn, err := node.Dial(b.network.Address())
	assert.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if b.getRemoteDevice(node.Address()).connectedSockets() > 0 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	return b.getRemoteDevice(node.Address()).connectedSockets()
}

func TestABlockedUsersEncryptedDeviceGetsNoSocket(t *testing.T) {
	quickReferenceRetries(t)
	me, _, mallory, _ := createUsersAndGroups(t)
	malloryHost := fakeEncryptedDeviceOf(t, mallory.currentUserID())

	// Before the block the host is welcome, the way any contact's encrypted device is
	assert.Equal(t, 1, dialAndCount(t, malloryHost, me))
	me.getRemoteDevice(malloryHost.Address()).shutdown()

	assert.NoError(t, me.BlockUser(mallory.currentUserID()))
	assert.True(t, me.peerIsBlocked(malloryHost.Address()))
	assert.True(t, me.peerIsBlocked(mallory.network.Address()))

	// Afterwards the connection is closed before it is ever tracked, so there is nothing to broadcast to
	assert.Equal(t, 0, dialAndCount(t, malloryHost, me))
}

func TestBlockingACoMemberLeavesTheSharedGroup(t *testing.T) {
	quickReferenceRetries(t)
	me, alice, mallory, groupID := createUsersAndGroups(t)
	gs, err := me.currentGroupState(groupID)
	assert.NoError(t, err)
	assert.True(t, gs.isMember(me.currentUserID()))

	// The block is in force the moment the call returns, so the consensus pass it triggers sees it and injects
	// our departure
	assert.NoError(t, me.BlockUser(mallory.currentUserID()))
	gs, _ = me.currentGroupState(groupID)
	assert.False(t, gs.isMember(me.currentUserID()))

	// The departure is a well-formed update group, with the content-derived ID every update group must have, and
	// the remaining member acknowledges it.  (The block cache is process-wide, so in this harness Alice blocks
	// Mallory too and leaves as well; her side of the group cannot be inspected here.)
	var departure updateGroup
	assert.NoError(t, me.database.Where("target = ? AND type = ? AND actor = ?", groupID, updateGroupTypeRemoveUser, me.currentUserID()).First(&departure).Error)
	assert.Equal(t, departure.generateID(), departure.ID)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var n int64
		me.database.Model(&deliveryRecord{}).Where("frame_id = ? AND destination = ?", departure.ID, alice.network.Address()).Count(&n)
		if n == 1 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("alice never acknowledged the departure")
}

func TestNothingIsSealedForABlockedUser(t *testing.T) {
	quickReferenceRetries(t)
	me, _, mallory, _ := createUsersAndGroups(t)
	profileUpdate := &file{ID: NewID(), Scope: scopeGlobal, Author: me.currentUserID(), Destination: me.currentUserID()}

	recipients := func() map[ID]bool {
		ids := map[ID]bool{}
		for _, u := range me.getUsersInScope(profileUpdate) {
			ids[u.ID] = true
		}
		return ids
	}
	assert.True(t, recipients()[mallory.currentUserID()])

	assert.NoError(t, me.BlockUser(mallory.currentUserID()))
	assert.False(t, recipients()[mallory.currentUserID()], "no copy on any encrypted device is wrapped for her")
	for _, addr := range me.getBroadcastScope(profileUpdate, false) {
		assert.NotEqual(t, mallory.network.Address(), addr)
	}
}

// A contact can send as many retention changes or history clears as they like.  Only the newest hundred of each kind
// from them are kept, and the ones that fall off the end leave the UI too, so a chatty or hostile contact cannot
// fill the disk one small row at a time.

// deletionRecordingUI is the fixture UI plus a record of which items it was told to delete
type deletionRecordingUI struct {
	*testUI
	sync.Mutex
	deleted []ID
}

func (ui *deletionRecordingUI) DeleteItem(id ID) {
	ui.Lock()
	ui.deleted = append(ui.deleted, id)
	ui.Unlock()
}

func TestOnlyTheNewestUpdateDMsOfEachKindFromAContactAreKept(t *testing.T) {
	recorder := &deletionRecordingUI{testUI: newTestUI()}
	me, alice, _, _ := createUsersAndGroupsFor(t, newBounceUserWithUI("Me", recorder))
	target := xor(alice.currentUserID(), me.currentUserID())
	retention := func(seconds uint64) []byte {
		data := make([]byte, 8)
		binary.LittleEndian.PutUint64(data, seconds)
		return data
	}
	extra := 30

	// Alice changes the DM retention far more times than we keep, oldest first
	sent := []ID{}
	for i := 0; i < maximumUpdateDMHistoryPerType+extra; i++ {
		ud := updateDM{
			Nonce:     newNonce(),
			Actor:     alice.currentUserID(),
			Target:    target,
			Timestamp: int64(1_000_000 + i),
			Type:      updateDMTypeChangeRetention,
			Data:      retention(uint64(3600 + i)),
		}
		ud.ID = ud.generateID()
		_, accepted := me.handleUpdateDM(alice.network.Address(), signedFrame(t, alice, typeUpdateDM, ud), false)
		assert.True(t, accepted)
		sent = append(sent, ud.ID)
	}

	// Exactly the bound remain, and they are the newest
	var kept []updateDM
	assert.NoError(t, me.database.Where("actor = ? AND type = ?", alice.currentUserID(), updateDMTypeChangeRetention).Order("timestamp ASC").Find(&kept).Error)
	assert.Equal(t, maximumUpdateDMHistoryPerType, len(kept))
	assert.Equal(t, sent[extra], kept[0].ID, "the oldest kept is the first one past the ones pruned")
	assert.Equal(t, sent[len(sent)-1], kept[len(kept)-1].ID)

	// The pruned ones were removed from the UI, in order, and nothing else was
	recorder.Lock()
	deleted := append([]ID{}, recorder.deleted...)
	recorder.Unlock()
	assert.Equal(t, sent[:extra], deleted)

	// The bound is per kind: a single clear from Alice sits alongside the hundred retention changes untouched
	clear := updateDM{
		Nonce:     newNonce(),
		Actor:     alice.currentUserID(),
		Target:    target,
		Timestamp: int64(2_000_000),
		Type:      updateDMTypeSetClearBefore,
	}
	clear.ID = clear.generateID()
	_, accepted := me.handleUpdateDM(alice.network.Address(), signedFrame(t, alice, typeUpdateDM, clear), false)
	assert.True(t, accepted)
	var total int64
	assert.NoError(t, me.database.Model(&updateDM{}).Where("actor = ?", alice.currentUserID()).Count(&total).Error)
	assert.Equal(t, int64(maximumUpdateDMHistoryPerType+1), total)
}

func TestBlockedUserCannotUpdateDM(t *testing.T) {
	quickReferenceRetries(t)
	me, _, mallory, _ := createUsersAndGroups(t)
	malloryID := mallory.currentUserID()
	target := xor(me.currentUserID(), malloryID)

	// Send a pre-block DM so we can verify a blocked SetClearBefore does not wipe history.
	assert.NoError(t, me.SendDirectMessage(malloryID, "keep this message", nil, NilID))
	var preBlockDM directMessage
	assert.NoError(t, me.database.Where("xor = ?", target).First(&preBlockDM).Error)

	assert.NoError(t, me.BlockUser(malloryID))
	t.Cleanup(func() {
		blockedUsersMutex.Lock()
		delete(blockedUsers, malloryID)
		blockedUsersMutex.Unlock()
	})

	var beforeUser user
	assert.NoError(t, me.database.First(&beforeUser, "id = ?", malloryID).Error)

	freshPeer := bnc01NewOnion().addr()
	now := time.Now().Unix()
	retentionBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(retentionBytes, 77777)

	attempts := []updateDM{
		{
			Nonce:     newNonce(),
			Actor:     malloryID,
			Target:    target,
			Timestamp: now + 60,
			Type:      updateDMTypeSetClearBefore,
		},
		{
			Nonce:     newNonce(),
			Actor:     malloryID,
			Target:    target,
			Timestamp: now + 61,
			Type:      updateDMTypeChangeRetention,
			Data:      retentionBytes,
		},
		{
			Nonce:     newNonce(),
			Actor:     malloryID,
			Target:    target,
			Timestamp: now + 62,
			Type:      updateDMTypeOfferRetention,
			Data:      retentionBytes,
		},
	}

	for _, ud := range attempts {
		ud.ID = ud.generateID()
		_, accepted := me.handleUpdateDM(freshPeer, signedFrame(t, mallory, typeUpdateDM, ud), false)
		assert.False(t, accepted, "blocked user's updateDM type %d must be rejected", ud.Type)

		var stored int64
		assert.NoError(t, me.database.Model(&updateDM{}).Where("id = ?", ud.ID).Count(&stored).Error)
		assert.Equal(t, int64(0), stored, "blocked user's updateDM must not be saved")
	}

	var dmCount int64
	assert.NoError(t, me.database.Model(&directMessage{}).Where("id = ?", preBlockDM.ID).Count(&dmCount).Error)
	assert.Equal(t, int64(1), dmCount, "pre-block DM must survive blocked user's SetClearBefore")

	var afterUser user
	assert.NoError(t, me.database.First(&afterUser, "id = ?", malloryID).Error)
	assert.Equal(t, beforeUser.ClearBefore, afterUser.ClearBefore)
	assert.Equal(t, beforeUser.Retention, afterUser.Retention)

	// Our own updates targeting the blocked thread must still apply (e.g., notes and unblock).
	notes := updateDM{
		Nonce:     newNonce(),
		Actor:     me.currentUserID(),
		Target:    target,
		Timestamp: now + 63,
		Type:      updateDMTypeSetNotes,
		Data:      []byte("blocked note"),
	}
	notes.ID = notes.generateID()
	_, accepted := me.handleUpdateDM(me.network.Address(), signedFrame(t, me, typeUpdateDM, notes), false)
	assert.True(t, accepted)
	assert.NoError(t, me.database.First(&afterUser, "id = ?", malloryID).Error)
	assert.Equal(t, "blocked note", afterUser.Notes)
}

func TestBlockListNotLeakedByUnauthenticatedAcks(t *testing.T) {
	me, alice, bob, _ := createUsersAndGroups(t)

	bobDev, found := me.getDeviceFromAddress(bob.network.Address())
	assert.True(t, found)

	// Block Bob on Me while keeping Alice as an unblocked contact
	assert.NoError(t, me.BlockUser(bob.currentUserID()))
	assert.True(t, blockedUser(bob.currentUserID()))

	wasAcked := func(frameType uint16, frameID ID) bool {
		flow := me.network.Address() + alice.network.Address()
		networkTracking.Lock()
		defer networkTracking.Unlock()
		for _, ref := range acked[flow] {
			if ref.Type == frameType && ref.FrameID == frameID {
				return true
			}
		}
		return false
	}

	now := time.Now().Unix()

	// 1. Forged device probe from Alice claiming UserID=Bob without a valid introduction from Bob
	probeKey := bnc01NewOnion()
	forgedDevice := &device{
		Nonce:     newNonce(),
		UserID:    bob.currentUserID(),
		Address:   probeKey.addr(),
		Network:   me.network.Name(),
		Timestamp: now,
	}
	forgedDevice.ID = forgedDevice.generateID()
	forgedDevice.SelfSignature = probeKey.sign(deviceSelfSignatureHash(forgedDevice.ID))
	alice.sendDirect(me.network.Address(), forgedDevice)

	// 2. Forged updateUser probe signed by Alice targeting blocked Bob
	forgedUU := updateUser{
		Nonce:     newNonce(),
		Target:    bob.currentUserID(),
		Type:      updateUserTypeUpdateName,
		Data:      []byte("probe"),
		Timestamp: now,
	}
	forgedUU.ID = forgedUU.generateID()
	alice.sendDirect(me.network.Address(), encodedSendable{
		frameType: typeUpdateUser,
		payload:   signedFrame(t, alice, typeUpdateUser, forgedUU),
	})

	// 3. Forged updateDevice probe signed by Alice targeting blocked Bob's device
	forgedUD := updateDevice{
		Nonce:     newNonce(),
		Target:    bobDev.ID,
		Type:      updateDeviceTypeUpdateName,
		Data:      []byte("probe"),
		Timestamp: now,
	}
	forgedUD.ID = forgedUD.generateID()
	alice.sendDirect(me.network.Address(), encodedSendable{
		frameType: typeUpdateDevice,
		payload:   signedFrame(t, alice, typeUpdateDevice, forgedUD),
	})

	// Send a legitimate frame from Alice on the same connection so we know all three probes were processed
	sentinel := updateUser{
		Nonce:     newNonce(),
		Target:    alice.currentUserID(),
		Type:      updateUserTypeUpdateName,
		Data:      []byte("Alice Updated"),
		Timestamp: now + 1,
	}
	sentinel.ID = sentinel.generateID()
	alice.sendDirect(me.network.Address(), encodedSendable{
		frameType: typeUpdateUser,
		payload:   signedFrame(t, alice, typeUpdateUser, sentinel),
	})
	awaitAck(t, alice, me, typeUpdateUser, sentinel.ID)

	time.Sleep(50 * time.Millisecond)
	assert.False(t, wasAcked(typeDevice, forgedDevice.ID), "forged device frame naming blocked user must not be acked")
	assert.False(t, wasAcked(typeUpdateUser, forgedUU.ID), "forged updateUser naming blocked user must not be acked")
	assert.False(t, wasAcked(typeUpdateDevice, forgedUD.ID), "forged updateDevice naming blocked user's device must not be acked")

	// Control: genuine frames authored by blocked Bob and relayed by honest Alice ARE still acked (and dropped)
	genuineBobSecond := introducedByBounce(bob.currentUserID(), bob, bnc01NewOnion(), now+2)
	alice.sendDirect(me.network.Address(), &genuineBobSecond)
	awaitAck(t, alice, me, typeDevice, genuineBobSecond.ID)
	_, storedDev := me.getDeviceFromAddress(genuineBobSecond.Address)
	assert.False(t, storedDev, "genuine device from blocked user must not be saved")

	genuineUU := updateUser{
		Nonce:     newNonce(),
		Target:    bob.currentUserID(),
		Type:      updateUserTypeUpdateName,
		Data:      []byte("Bob New Name"),
		Timestamp: now + 3,
	}
	genuineUU.ID = genuineUU.generateID()
	alice.sendDirect(me.network.Address(), encodedSendable{
		frameType: typeUpdateUser,
		payload:   signedFrame(t, bob, typeUpdateUser, genuineUU),
	})
	awaitAck(t, alice, me, typeUpdateUser, genuineUU.ID)
	var uuCount int64
	assert.NoError(t, me.database.Model(&updateUser{}).Where("id = ?", genuineUU.ID).Count(&uuCount).Error)
	assert.Equal(t, int64(0), uuCount, "genuine updateUser from blocked user must not be saved")

	genuineUD := updateDevice{
		Nonce:     newNonce(),
		Target:    bobDev.ID,
		Type:      updateDeviceTypeUpdateName,
		Data:      []byte("Bob New Device"),
		Timestamp: now + 4,
	}
	genuineUD.ID = genuineUD.generateID()
	alice.sendDirect(me.network.Address(), encodedSendable{
		frameType: typeUpdateDevice,
		payload:   signedFrame(t, bob, typeUpdateDevice, genuineUD),
	})
	awaitAck(t, alice, me, typeUpdateDevice, genuineUD.ID)
	var udCount int64
	assert.NoError(t, me.database.Model(&updateDevice{}).Where("id = ?", genuineUD.ID).Count(&udCount).Error)
	assert.Equal(t, int64(0), udCount, "genuine updateDevice from blocked user must not be saved")
}
