package chat

import (
	"testing"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/alecthomas/assert/v2"
)

func TestOverlapUsersInScopeAreCorrect(t *testing.T) {
	me, alice, bob, _ := createUsersAndGroups(t)

	// Create a new user Carol who only I know
	carol := newBounceUser("Carol")

	meUser, _ := me.currentUser()
	meBytes, _ := msgpack.Marshal(meUser)
	carolUser, _ := carol.currentUser()
	carolBytes, _ := msgpack.Marshal(carolUser)

	au := addUser{
		Nonce:           newNonce(),
		Xor:             xor(carol.currentUserID(), me.currentUserID()),
		Timestamp:       time.Now().Unix(),
		OfferUser:       meBytes,
		RequesterUser:   carolBytes,
		OfferDevice:     me.network.Address(),
		RequesterDevice: carol.network.Address(),
	}
	digest := addUserSignatureDigest(au.Nonce, au.Timestamp, au.OfferUser, au.RequesterUser, au.OfferDevice, au.RequesterDevice)
	au.OfferSignature = me.network.Sign(digest)
	au.RequesterSignature = carol.network.Sign(digest)
	au.ID = au.generateID()
	auBytes, _ := msgpack.Marshal(&au)
	myBr, _ := me.handleAddUser(carol.network.Address(), auBytes, false)
	assert.True(t, myBr != nil)
	carolBr, _ := carol.handleAddUser(me.network.Address(), auBytes, false)
	assert.True(t, carolBr != nil)

	// Make a global update from Alice
	aliceRename := updateUser{
		Nonce:     newNonce(),
		Target:    alice.currentUserID(),
		Timestamp: time.Now().Unix(),
		Type:      updateUserTypeUpdateName,
		Data:      []byte("Alice 2"),
	}
	aliceRename.ID = aliceRename.generateID()

	// Get the users who should receive this update from me, make sure
	// all who share a group with alice are in there and carol is not
	aliceOverlap := me.getUsersInScope(&aliceRename)
	assert.True(t, len(aliceOverlap) == 3)

	foundMe := false
	foundAlice := false
	foundBob := false
	foundCarol := false
	for _, u := range aliceOverlap {
		if u.ID == me.currentUserID() {
			foundMe = true
		}
		if u.ID == alice.currentUserID() {
			foundAlice = true
		}
		if u.ID == bob.currentUserID() {
			foundBob = true
		}
		if u.ID == carol.currentUserID() {
			foundCarol = true
		}
	}
	assert.True(t, foundMe)
	assert.True(t, foundAlice)
	assert.True(t, foundBob)
	assert.False(t, foundCarol)
}
