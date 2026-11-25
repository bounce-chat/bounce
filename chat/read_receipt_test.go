package chat

import (
	"testing"
	"time"

	"github.com/alecthomas/assert/v2"
)

func TestDuplicateReadReceiptsForSameTargetAreIgnored(t *testing.T) {
	me, alice, _, groupID := createUsersAndGroups(t)

	me.SendGroupMessage(groupID, "read me once", nil, NilID)
	var sent groupMessage
	assert.NoError(t, me.database.Where("text = ?", "read me once").First(&sent).Error)
	awaitAck(t, me, alice, typeGroupMessage, sent.ID)

	first := readReceipt{
		Nonce:      newNonce(),
		Actor:      alice.currentUserID(),
		Target:     sent.ID,
		TargetType: typeGroupMessage,
		Timestamp:  time.Now().Unix(),
	}
	first.ID = first.generateID()
	br, accepted := me.handleReadReceipt(alice.network.Address(), signedFrame(t, alice, typeReadReceipt, first), false)
	assert.True(t, accepted)
	assert.True(t, br != nil)

	// Re-sending read receipts for the same (actor, target, target_type) with fresh nonces must be
	// ignored (no additional rows, no broadcast) rather than accumulating unboundedly.
	for i := 0; i < 5; i++ {
		dup := readReceipt{
			Nonce:      newNonce(),
			Actor:      alice.currentUserID(),
			Target:     sent.ID,
			TargetType: typeGroupMessage,
			Timestamp:  time.Now().Unix() + int64(i+1),
		}
		dup.ID = dup.generateID()
		br, accepted = me.handleReadReceipt(alice.network.Address(), signedFrame(t, alice, typeReadReceipt, dup), false)
		assert.False(t, accepted)
		assert.True(t, br == nil)
	}

	var count int64
	assert.NoError(t, me.database.Model(&readReceipt{}).
		Where("actor = ? AND target = ? AND target_type = ?", alice.currentUserID(), sent.ID, typeGroupMessage).
		Count(&count).Error)
	assert.Equal(t, int64(1), count)

	state := me.GetInitialState()
	var receiptCount int
	for _, gm := range state.GroupMessages {
		if gm.ID == sent.ID {
			receiptCount = len(gm.ReadReceipts)
		}
	}
	assert.Equal(t, 1, receiptCount)

	// Local MarkAsRead repeated on the same message must also remain idempotent under the unique constraint.
	alice.MarkAsRead(sent.ID, TypeGroupMessage)
	alice.MarkAsRead(sent.ID, TypeGroupMessage)
	assert.NoError(t, alice.database.Model(&readReceipt{}).
		Where("actor = ? AND target = ? AND target_type = ?", alice.currentUserID(), sent.ID, typeGroupMessage).
		Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestEarlyReadReceiptsAreCappedPerActorAndGlobally(t *testing.T) {
	origPerActor := maximumEarlyReadReceiptsPerActor
	origTotal := maximumEarlyReadReceipts
	maximumEarlyReadReceiptsPerActor = 3
	maximumEarlyReadReceipts = 5
	t.Cleanup(func() {
		maximumEarlyReadReceiptsPerActor = origPerActor
		maximumEarlyReadReceipts = origTotal
	})

	me, alice, bob, _ := createUsersAndGroups(t)

	// Alice sends early receipts for nonexistent targets up to and past the per-actor cap.
	var repeatedTarget ID
	for i := 0; i < 5; i++ {
		target := NewID()
		if i == 0 {
			repeatedTarget = target
		}
		rr := readReceipt{
			Nonce:      newNonce(),
			Actor:      alice.currentUserID(),
			Target:     target,
			TargetType: typeGroupMessage,
			Timestamp:  time.Now().Unix(),
		}
		rr.ID = rr.generateID()
		me.handleReadReceipt(alice.network.Address(), signedFrame(t, alice, typeReadReceipt, rr), false)
	}

	// A duplicate early receipt for an already-stored early target must not add another row.
	dupEarly := readReceipt{
		Nonce:      newNonce(),
		Actor:      alice.currentUserID(),
		Target:     repeatedTarget,
		TargetType: typeGroupMessage,
		Timestamp:  time.Now().Unix() + 1,
	}
	dupEarly.ID = dupEarly.generateID()
	me.handleReadReceipt(alice.network.Address(), signedFrame(t, alice, typeReadReceipt, dupEarly), false)

	var aliceEarly int64
	assert.NoError(t, me.database.Model(&readReceipt{}).
		Where("destination = ? AND actor = ?", NilID, alice.currentUserID()).
		Count(&aliceEarly).Error)
	assert.Equal(t, int64(3), aliceEarly)

	// Bob can only store 2 more before hitting the global cap of 5.
	for i := 0; i < 4; i++ {
		rr := readReceipt{
			Nonce:      newNonce(),
			Actor:      bob.currentUserID(),
			Target:     NewID(),
			TargetType: typeDirectMessage,
			Timestamp:  time.Now().Unix(),
		}
		rr.ID = rr.generateID()
		me.handleReadReceipt(bob.network.Address(), signedFrame(t, bob, typeReadReceipt, rr), false)
	}

	var bobEarly, totalEarly int64
	assert.NoError(t, me.database.Model(&readReceipt{}).
		Where("destination = ? AND actor = ?", NilID, bob.currentUserID()).
		Count(&bobEarly).Error)
	assert.Equal(t, int64(2), bobEarly)
	assert.NoError(t, me.database.Model(&readReceipt{}).
		Where("destination = ?", NilID).
		Count(&totalEarly).Error)
	assert.Equal(t, int64(5), totalEarly)

	// Unresolved early receipts must not be offered to our own sync devices.
	refs := me.getReadReceiptsToOffer("sync-peer-placeholder", me.currentUserID())
	for _, ref := range refs {
		var rr readReceipt
		assert.NoError(t, me.database.Select("destination").Where("id = ?", ref.FrameID).First(&rr).Error)
		assert.NotEqual(t, NilID, rr.Destination)
	}
}
