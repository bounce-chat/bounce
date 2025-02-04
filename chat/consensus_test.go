package chat

import (
	"testing"
	"time"

	"github.com/alecthomas/assert/v2"
	"github.com/google/uuid"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm/clause"
)

func TestCanRenameGroupAndConfirm(t *testing.T) {
	b, alice, bob, groupID := createUsersAndGroups(t)

	newName := "New Name"
	b.renameGroup(groupID, newName)
	var ug updateGroup
	err := b.database.Select("id").First(&ug, "target = ?", groupID).Error
	assert.NoError(t, err)

	var g group
	err = b.database.First(&g, "id = ?", groupID).Error
	assert.NoError(t, err)
	assert.Equal(t, newName, g.Name)

	await(alice, "RenameGroup")
	err = alice.database.First(&g, "id = ?", groupID).Error
	assert.NoError(t, err)
	assert.Equal(t, newName, g.Name)

	await(bob, "RenameGroup")
	err = bob.database.First(&g, "id = ?", groupID).Error
	assert.NoError(t, err)
	assert.Equal(t, newName, g.Name)

	// I didn't confirm it, because I wrote it
	var c confirmation
	err = b.database.First(&c, "update_group_id = ? AND author = ?", ug.ID, b.currentUserID()).Error
	assert.Error(t, err)

	// Alice confirmed it
	var ac confirmation
	err = alice.database.First(&ac, "update_group_id = ? AND author = ?", ug.ID, alice.currentUserID()).Error
	assert.NoError(t, err)

	// Bob confirmed it
	var bc confirmation
	err = bob.database.First(&bc, "update_group_id = ? AND author = ?", ug.ID, bob.currentUserID()).Error
	assert.NoError(t, err)

	// I see three confirmations
	awaitDeliveryTo(t, alice, typeConfirmation, ac.ID, firstAddress(b))
	awaitDeliveryTo(t, bob, typeConfirmation, bc.ID, firstAddress(b))
	err = b.database.Preload(clause.Associations).First(&ug, "target = ?", groupID).Error
	assert.NoError(t, err)
	assert.Equal(t, 3, ug.confirmingUsers())

	// Alice sees three confirmations
	awaitDeliveryTo(t, bob, typeConfirmation, bc.ID, firstAddress(alice))
	var aug updateGroup
	err = alice.database.Preload(clause.Associations).First(&aug, "target = ?", groupID).Error
	assert.NoError(t, err)
	assert.Equal(t, 3, aug.confirmingUsers())

	// Bob sees three confirmations
	awaitDeliveryTo(t, alice, typeConfirmation, ac.ID, firstAddress(bob))
	var bug updateGroup
	err = bob.database.Preload(clause.Associations).First(&bug, "target = ?", groupID).Error
	assert.NoError(t, err)
	assert.Equal(t, 3, bug.confirmingUsers())
}

func TestMessagesAreValidIfInCatchUpThatGivesPermission(t *testing.T) {
	b, alice, bob, groupID := createUsersAndGroups(t)

	err := b.restrictPosting(groupID)
	assert.NoError(t, err)
	var restriction updateGroup
	err = b.database.Select("id").First(&restriction, "target = ?", groupID).Error
	assert.NoError(t, err)

	// Make sure that Alice and Bob's confirmations have been handeled so that we make no further updates to the group state
	await(alice, "SetGroupState")
	var aliceConfirmation confirmation
	err = alice.database.First(&aliceConfirmation, "update_group_id = ? AND author = ?", restriction.ID, alice.currentUserID()).Error
	assert.NoError(t, err)
	awaitDeliveryTo(t, alice, typeConfirmation, aliceConfirmation.ID, firstAddress(bob))

	await(bob, "SetGroupState")
	var bobConfirmation confirmation
	err = bob.database.First(&bobConfirmation, "update_group_id = ? AND author = ?", restriction.ID, bob.currentUserID()).Error
	assert.NoError(t, err)
	awaitDeliveryTo(t, bob, typeConfirmation, bobConfirmation.ID, firstAddress(alice))

	// Create an update group to unrestrict posting
	unrestriction := &updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeChangePostingPermission,
		Data:      []byte{permissionUnrestricted},
	}
	unrestriction.OriginalPayload, err = msgpack.Marshal(unrestriction)
	assert.NoError(t, err)
	sc := b.createSignedContainer(unrestriction.OriginalPayload)
	unrestriction.Signature = sc.Signature
	unrestriction.Signer = sc.Signer

	// Create a post from a non-admin
	alicePost := &groupMessage{
		ID:          uuid.New(),
		WrittenAt:   time.Now().Unix(),
		Author:      alice.currentUserID(),
		Destination: groupID,
		Text:        "I am allowed to post again",
	}
	alicePost.OriginalPayload, err = msgpack.Marshal(alicePost)
	assert.NoError(t, err)
	sc = alice.createSignedContainer(alicePost.OriginalPayload)
	alicePost.Signature = sc.Signature
	alicePost.Signer = sc.Signer

	// Create a catch up with both
	cu := catchUp{
		Frames: []frame{
			frame{
				ID:      unrestriction.ID,
				Type:    typeUpdateGroup,
				Payload: unrestriction.getPayload(),
			},
			frame{
				ID:      alicePost.ID,
				Type:    typeGroupMessage,
				Payload: alicePost.getPayload(),
			},
		},
	}
	cuPayload, err := msgpack.Marshal(cu)
	assert.NoError(t, err)

	// Handle the permission granting and message in the catch up handler
	var aliceDev device
	err = alice.database.First(&aliceDev, "user_id = ?", alice.currentUserID()).Error
	assert.NoError(t, err)
	b.handleCatchUp(aliceDev.Address, cuPayload, false)

	// Make sure the message was saved
	var delivered groupMessage
	err = b.database.First(&delivered, "id = ?", alicePost.ID).Error
	assert.NoError(t, err)
	assert.Equal(t, alicePost.Text, delivered.Text)
}

func TestMessagesAreInvalidIfInCatchUpThatRemovesPermission(t *testing.T) {
	b, alice, _, groupID := createUsersAndGroups(t)

	// Create an update group to unrestrict posting
	restriction := &updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeChangePostingPermission,
		Data:      []byte{permissionRestricted},
	}
	var err error
	restriction.OriginalPayload, err = msgpack.Marshal(restriction)
	assert.NoError(t, err)
	sc := b.createSignedContainer(restriction.OriginalPayload)
	restriction.Signature = sc.Signature
	restriction.Signer = sc.Signer

	// Create a post from a non-admin
	alicePost := &groupMessage{
		ID:          uuid.New(),
		WrittenAt:   time.Now().Unix(),
		Author:      alice.currentUserID(),
		Destination: groupID,
		Text:        "I am not allowed to post now",
	}
	alicePost.OriginalPayload, err = msgpack.Marshal(alicePost)
	assert.NoError(t, err)
	sc = alice.createSignedContainer(alicePost.OriginalPayload)
	alicePost.Signature = sc.Signature
	alicePost.Signer = sc.Signer

	// Create a catch up with both
	cu := catchUp{
		Frames: []frame{
			frame{
				ID:      restriction.ID,
				Type:    typeUpdateGroup,
				Payload: restriction.getPayload(),
			},
			frame{
				ID:      alicePost.ID,
				Type:    typeGroupMessage,
				Payload: alicePost.getPayload(),
			},
		},
	}
	cuPayload, err := msgpack.Marshal(cu)
	assert.NoError(t, err)

	// Handle the permission granting and message in the catch up handler
	var aliceDev device
	err = alice.database.First(&aliceDev, "user_id = ?", alice.currentUserID()).Error
	assert.NoError(t, err)
	b.handleCatchUp(aliceDev.Address, cuPayload, false)

	// Make sure the message was saved
	var delivered groupMessage
	err = b.database.First(&delivered, "id = ?", alicePost.ID).Error
	assert.Error(t, err)
}
