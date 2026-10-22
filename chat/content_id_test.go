package chat

import (
	"testing"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/alecthomas/assert/v2"
)

// A content addressed frame's ID commits its author to one definition of the frame.  These tests pin the packing,
// which is what makes two nodes agree on an ID, and show that a frame carrying someone else's content under a known
// ID is refused.

func TestThePackingSeparatesTypesFieldsAndNonces(t *testing.T) {
	n := newNonce()

	// The same bytes under two frame types are two IDs
	assert.NotEqual(t,
		newContentHasher(typeGroupCreation, n).bytes([]byte("x")).generate(),
		newContentHasher(typeUpdateGroup, n).bytes([]byte("x")).generate())

	// So are the same bytes under two nonces: identical content sent twice is two frames
	assert.NotEqual(t,
		newContentHasher(typeUpdateGroup, n).bytes([]byte("x")).generate(),
		newContentHasher(typeUpdateGroup, newNonce()).bytes([]byte("x")).generate())

	// Moving a byte across a field boundary is a different frame, so the boundary is part of the preimage
	assert.NotEqual(t,
		newContentHasher(typeUpdateGroup, n).bytes([]byte("ab")).bytes([]byte("c")).generate(),
		newContentHasher(typeUpdateGroup, n).bytes([]byte("a")).bytes([]byte("bc")).generate())

	// A nil blob and an empty one are the same content, which is where hashing an encoder's output went wrong
	assert.Equal(t,
		newContentHasher(typeUpdateGroup, n).bytes(nil).generate(),
		newContentHasher(typeUpdateGroup, n).bytes([]byte{}).generate())

	// And the packing is a pure function of its inputs
	one, two := NewID(), NewID()
	assert.Equal(t,
		newContentHasher(typeUpdateGroup, n).id(one).id(two).int64(7).uint16(3).bytes([]byte("d")).generate(),
		newContentHasher(typeUpdateGroup, n).id(one).id(two).int64(7).uint16(3).bytes([]byte("d")).generate())
}

func TestAFrameWithoutANonceIsRefused(t *testing.T) {
	me, alice, _, groupID := createUsersAndGroups(t)
	bare := updateGroup{
		Actor:     alice.currentUserID(),
		Target:    groupID,
		Type:      updateGroupTypeChangeName,
		Data:      []byte("No nonce"),
		Timestamp: time.Now().Unix(),
	}
	bare.ID = bare.generateID()
	assert.False(t, idMatchesContent(&bare))
	_, accepted := me.handleUpdateGroup(alice.network.Address(), signedBy(t, alice, bare), false)
	assert.False(t, accepted, "an update whose author left out the nonce")
	assert.Error(t, me.database.Create(&bare).Error)

	// The sending path supplies one, so locally authored updates never hit this
	assert.NoError(t, alice.applyAndBroadcastUpdateGroup(&bare))
	assert.NotEqual(t, noNonce, bare.Nonce)
	assert.True(t, idMatchesContent(&bare))
}

// signedBy wraps an update group the way the sending engine does, with b's device signing it
func signedBy(t *testing.T, b *Bounce, ug updateGroup) []byte {
	t.Helper()
	payload, err := msgpack.Marshal(ug)
	assert.NoError(t, err)
	frame, err := msgpack.Marshal(b.createSignedContainer(typeUpdateGroup, payload))
	assert.NoError(t, err)
	return frame
}

func TestAnUpdateGroupCannotBeRedefinedUnderItsID(t *testing.T) {
	me, alice, _, groupID := createUsersAndGroups(t)
	rename := updateGroup{
		Nonce:     newNonce(),
		Actor:     alice.currentUserID(),
		Target:    groupID,
		Type:      updateGroupTypeChangeName,
		Data:      []byte("Book club"),
		Timestamp: time.Now().Unix(),
	}
	rename.ID = rename.generateID()

	// Alice's real rename is accepted
	_, accepted := me.handleUpdateGroup(alice.network.Address(), signedBy(t, alice, rename), false)
	assert.True(t, accepted)

	// The same frame with different content under the same ID is refused, even though Alice signed it too
	other := rename
	other.Data = []byte("Fight club")
	_, accepted = me.handleUpdateGroup(alice.network.Address(), signedBy(t, alice, other), false)
	assert.False(t, accepted, "a second definition of the update")

	// And the one we hold is the first definition
	var stored updateGroup
	assert.NoError(t, me.database.First(&stored, "id = ?", rename.ID).Error)
	assert.Equal(t, "Book club", string(stored.Data))

	// A locally built update with an ID that is not its content cannot even be saved
	wrong := rename
	wrong.Data = []byte("Chess club")
	assert.Error(t, me.database.Create(&wrong).Error)
}

func TestAGroupCreationCannotBeRedefinedUnderItsID(t *testing.T) {
	me, alice, _, groupID := createUsersAndGroups(t)
	var original groupCreation
	assert.NoError(t, me.database.First(&original, "id = ?", groupID).Error)

	// Re-encode the founding group with a different name but the same ID, signed by the real creator
	var g group
	assert.NoError(t, msgpack.Unmarshal(original.Data, &g))
	g.Name = "Not the group you joined"
	tampered := groupCreation{ID: original.ID, Nonce: original.Nonce, Timestamp: original.Timestamp}
	var err error
	tampered.Data, err = msgpack.Marshal(g)
	assert.NoError(t, err)
	tampered.OriginalPayload, err = msgpack.Marshal(tampered)
	assert.NoError(t, err)
	frame, err := msgpack.Marshal(me.createSignedContainer(typeGroupCreation, tampered.OriginalPayload))
	assert.NoError(t, err)

	_, accepted := alice.handleGroupCreation(me.network.Address(), frame, false)
	assert.False(t, accepted, "a creation whose content is not what its ID names")
	assert.NotEqual(t, tampered.ID, tampered.generateID())
	assert.Equal(t, original.ID, original.generateID(), "the genuine creation does carry its content's ID")
}
