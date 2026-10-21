package chat

import (
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
	assert.Equal(t, updateGroupID(departure), departure.ID)
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
