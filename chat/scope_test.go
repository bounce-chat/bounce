package chat

import (
	"testing"
	"time"

	"github.com/alecthomas/assert/v2"
	"github.com/google/uuid"
	"github.com/vmihailenco/msgpack/v5"
	"github.com/zeebo/blake3"
)

func TestOverlapUsersInScopeAreCorrect(t *testing.T) {
	me, alice, bob, _ := createUsersAndGroups(t)

	// Create a new user Carol who only I know
	carol := newBounceUser("Carol")

	meUser, _ := me.currentUser()
	meBytes, _ := msgpack.Marshal(meUser)
	meBytesHash := blake3.Sum256(meBytes)
	carolUser, _ := carol.currentUser()
	carolBytes, _ := msgpack.Marshal(carolUser)
	carolBytesHash := blake3.Sum256(carolBytes)

	au := addUser{
		ID:                 uuid.New(),
		Xor:                xor(carol.currentUserID(), me.currentUserID()),
		Timestamp:          time.Now().Unix(),
		OfferUser:          meBytes,
		RequesterUser:      carolBytes,
		OfferDevice:        me.network.Address(),
		RequesterDevice:    carol.network.Address(),
		OfferSignature:     me.network.Sign(carolBytesHash[:]),
		RequesterSignature: carol.network.Sign(meBytesHash[:]),
	}
	auBytes, _ := msgpack.Marshal(&au)
	myBr, _ := me.handleAddUser(carol.network.Address(), auBytes, false)
	assert.True(t, myBr != nil)
	carolBr, _ := carol.handleAddUser(me.network.Address(), auBytes, false)
	assert.True(t, carolBr != nil)

	// Make a global update from Alice
	aliceRename := updateUser{
		ID:        uuid.New(),
		Target:    alice.currentUserID(),
		Timestamp: time.Now().Unix(),
		Type:      updateUserTypeUpdateName,
		Data:      []byte("Alice 2"),
	}

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
