package chat

import (
	"testing"

	"github.com/alecthomas/assert/v2"
)

// Frames an encrypted device must drop when a chat's history is cleared carry that chat as a batch delete key, and
// say so, whether they reach the device by live push or by reference request; everything else is kept until it
// expires.
func TestBatchDeleteKeysFollowTheThread(t *testing.T) {
	me, alice, _, groupID := createUsersAndGroups(t)
	thread := xor(me.currentUserID(), alice.currentUserID())

	key, clearable := me.batchDeleteKeyFor(&directMessage{Xor: thread})
	assert.True(t, clearable)
	assert.Equal(t, thread, key)

	key, clearable = me.batchDeleteKeyFor(&groupMessage{Destination: groupID})
	assert.True(t, clearable)
	assert.Equal(t, groupID, key)

	key, clearable = me.batchDeleteKeyFor(&file{Type: fileTypeMessageAttachment, Destination: groupID})
	assert.True(t, clearable)
	assert.Equal(t, groupID, key)

	key, clearable = me.batchDeleteKeyFor(&readReceipt{Destination: thread})
	assert.True(t, clearable)
	assert.Equal(t, thread, key)

	// A profile picture outlives any chat, as does anything that is not part of a conversation
	_, clearable = me.batchDeleteKeyFor(&file{Type: fileTypeUserImage, Destination: me.currentUserID()})
	assert.False(t, clearable)
	_, clearable = me.batchDeleteKeyFor(&updateUser{Target: me.currentUserID()})
	assert.False(t, clearable)
}
