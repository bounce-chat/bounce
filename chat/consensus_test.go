package chat

import (
	"testing"
	"time"

	"github.com/alecthomas/assert/v2"
	"github.com/google/uuid"
	"github.com/vmihailenco/msgpack/v5"
)

func TestMessagesAreValidIfCatchUpGivesPermission(t *testing.T) {
	b, alice, bob, groupID := createUsersAndGroups(t)

	// Restrict posting to only admins
	err := b.restrictPosting(groupID)
	assert.NoError(t, err)
	var restriction updateGroup
	err = b.database.Select("id").First(&restriction, "target = ?", groupID).Error
	assert.NoError(t, err)

	// Make sure that Alice and Bob's confirmations have been handeled so that we make no further updates to the group state
	awaitAck(t, b, alice, typeUpdateGroup, restriction.ID)
	var aliceConfirmation confirmation
	err = alice.database.First(&aliceConfirmation, "update_group_id = ? AND author = ?", restriction.ID, alice.currentUserID()).Error
	assert.NoError(t, err)
	awaitAck(t, alice, bob, typeConfirmation, aliceConfirmation.ID)

	awaitAck(t, b, bob, typeUpdateGroup, restriction.ID)
	var bobConfirmation confirmation
	err = bob.database.First(&bobConfirmation, "update_group_id = ? AND author = ?", restriction.ID, bob.currentUserID()).Error
	assert.NoError(t, err)
	awaitAck(t, bob, alice, typeConfirmation, bobConfirmation.ID)

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

func TestMessagesAreInvalidIfCatchUpRemovesPermission(t *testing.T) {
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

	// Make sure the message was not saved
	var delivered groupMessage
	err = b.database.First(&delivered, "id = ?", alicePost.ID).Error
	assert.Error(t, err)
}

func TestMessagesAreValidIfUserBecomesAdminWhenRequired(t *testing.T) {
	b, alice, bob, groupID := createUsersAndGroups(t)

	// Restrict posting to only admins
	err := b.restrictPosting(groupID)
	assert.NoError(t, err)
	var restriction updateGroup
	err = b.database.Select("id").First(&restriction, "target = ?", groupID).Error
	assert.NoError(t, err)

	// Make sure that Alice and Bob's confirmations have been handeled so that we make no further updates to the group state
	awaitAck(t, b, alice, typeUpdateGroup, restriction.ID)
	var aliceConfirmation confirmation
	err = alice.database.First(&aliceConfirmation, "update_group_id = ? AND author = ?", restriction.ID, alice.currentUserID()).Error
	assert.NoError(t, err)
	awaitAck(t, alice, bob, typeConfirmation, aliceConfirmation.ID)

	awaitAck(t, b, bob, typeUpdateGroup, restriction.ID)
	var bobConfirmation confirmation
	err = bob.database.First(&bobConfirmation, "update_group_id = ? AND author = ?", restriction.ID, bob.currentUserID()).Error
	assert.NoError(t, err)
	awaitAck(t, bob, alice, typeConfirmation, bobConfirmation.ID)

	// Confirm that Alice is not an admin
	aliceID := alice.currentUserID()
	gs, err := b.currentGroupState(groupID)
	assert.NoError(t, err)
	assert.False(t, gs.isAdmin(aliceID))

	// Create an update group to make Alice an admin
	promotion := &updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypePromoteAdmin,
		Data:      aliceID[:],
	}
	promotion.OriginalPayload, err = msgpack.Marshal(promotion)
	assert.NoError(t, err)
	sc := b.createSignedContainer(promotion.OriginalPayload)
	promotion.Signature = sc.Signature
	promotion.Signer = sc.Signer

	// Create a post from Alice
	alicePost := &groupMessage{
		ID:          uuid.New(),
		WrittenAt:   time.Now().Unix(),
		Author:      alice.currentUserID(),
		Destination: groupID,
		Text:        "I am now an admin",
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
				ID:      promotion.ID,
				Type:    typeUpdateGroup,
				Payload: promotion.getPayload(),
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

	// Handle the admin granting and message in the catch up handler
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

func TestMessagesAreInvalidIfUserLoosesAdminWhenRequired(t *testing.T) {
	b, alice, bob, groupID := createUsersAndGroups(t)

	// Restrict posting to only admins
	err := b.restrictPosting(groupID)
	assert.NoError(t, err)
	var restriction updateGroup
	err = b.database.Select("id").First(&restriction, "target = ?", groupID).Error
	assert.NoError(t, err)

	// Make sure restriiction is applied
	awaitAck(t, b, alice, typeUpdateGroup, restriction.ID)
	var aliceConfirmationRestriction confirmation
	err = alice.database.First(&aliceConfirmationRestriction, "update_group_id = ? AND author = ?", restriction.ID, alice.currentUserID()).Error
	assert.NoError(t, err)
	awaitAck(t, alice, bob, typeConfirmation, aliceConfirmationRestriction.ID)
	gs, err := alice.currentGroupState(groupID)
	assert.NoError(t, err)
	assert.True(t, gs.postingRestricted)

	awaitAck(t, b, bob, typeUpdateGroup, restriction.ID)
	var bobConfirmationRestriction confirmation
	err = bob.database.First(&bobConfirmationRestriction, "update_group_id = ? AND author = ?", restriction.ID, bob.currentUserID()).Error
	assert.NoError(t, err)
	awaitAck(t, bob, alice, typeConfirmation, bobConfirmationRestriction.ID)
	gs, err = bob.currentGroupState(groupID)
	assert.NoError(t, err)
	assert.True(t, gs.postingRestricted)

	// Make Alice an admin
	aliceID := alice.currentUserID()
	err = b.promoteAdmin(groupID, aliceID)
	assert.NoError(t, err)
	var promotion updateGroup
	err = b.database.Select("id").First(&promotion, "target = ?", groupID).Error
	assert.NoError(t, err)
	gs, err = b.currentGroupState(groupID)
	assert.NoError(t, err)
	assert.True(t, gs.isAdmin(aliceID))
	assert.True(t, gs.postingRestricted)

	// Make sure that Alice and Bob's confirmations have been handeled so that we make no further updates to the group state
	awaitAck(t, b, alice, typeUpdateGroup, promotion.ID)
	var aliceConfirmationPromotion confirmation
	err = alice.database.First(&aliceConfirmationPromotion, "update_group_id = ? AND author = ?", promotion.ID, alice.currentUserID()).Error
	assert.NoError(t, err)
	awaitAck(t, alice, bob, typeConfirmation, aliceConfirmationPromotion.ID)
	//gs, err = alice.currentGroupState(groupID)
	//assert.NoError(t, err)
	//assert.True(t, gs.isAdmin(aliceID)) // TODO: why flaky?

	awaitAck(t, b, bob, typeUpdateGroup, promotion.ID)
	var bobConfirmationPromotion confirmation
	err = bob.database.First(&bobConfirmationPromotion, "update_group_id = ? AND author = ?", promotion.ID, bob.currentUserID()).Error
	assert.NoError(t, err)
	awaitAck(t, bob, alice, typeConfirmation, bobConfirmationPromotion.ID)
	//gs, err = bob.currentGroupState(groupID)
	//assert.NoError(t, err)
	//assert.True(t, gs.isAdmin(aliceID))

	// Create an update group to demote Alice
	demotion := &updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeDemoteAdmin,
		Data:      aliceID[:],
	}
	demotion.OriginalPayload, err = msgpack.Marshal(demotion)
	assert.NoError(t, err)
	sc := b.createSignedContainer(demotion.OriginalPayload)
	demotion.Signature = sc.Signature
	demotion.Signer = sc.Signer

	// Create a post from Alice
	alicePost := &groupMessage{
		ID:          uuid.New(),
		WrittenAt:   time.Now().Unix(),
		Author:      alice.currentUserID(),
		Destination: groupID,
		Text:        "I am not an admin now",
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
				ID:      demotion.ID,
				Type:    typeUpdateGroup,
				Payload: demotion.getPayload(),
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

	// Handle the demotion and message in the catch up handler
	var aliceDev device
	err = alice.database.First(&aliceDev, "user_id = ?", alice.currentUserID()).Error
	assert.NoError(t, err)
	b.handleCatchUp(aliceDev.Address, cuPayload, false)

	// Make sure the message was not saved
	var delivered groupMessage
	err = b.database.First(&delivered, "id = ?", alicePost.ID).Error
	assert.Error(t, err)
}
