package chat

import (
	"testing"
	"time"

	"github.com/alecthomas/assert/v2"
	"github.com/google/uuid"
	"github.com/Basekick-Labs/msgpack/v6"
	"gorm.io/gorm/clause"
)

func TestMessagesAreValidIfCatchUpGivesPermission(t *testing.T) {
	b, alice, _, groupID := createUsersAndGroups(t)

	// Restrict posting to only admins
	err := b.RestrictPosting(groupID)
	assert.NoError(t, err)

	// Create an update group to unrestrict posting
	unrestriction := &updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix() + 1,
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
		WrittenAt:   time.Now().Unix() + 1,
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
	b.handleCatchUp(b.network.Address(), cuPayload, false)

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
	b, alice, _, groupID := createUsersAndGroups(t)

	// Restrict posting to only admins
	err := b.RestrictPosting(groupID)
	assert.NoError(t, err)

	// Create an update group to make Alice an admin
	aliceID := alice.currentUserID()
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
	b.handleCatchUp(b.network.Address(), cuPayload, false)

	// Make sure the message was saved
	var delivered groupMessage
	err = b.database.First(&delivered, "id = ?", alicePost.ID).Error
	assert.NoError(t, err)
	assert.Equal(t, alicePost.Text, delivered.Text)
}

func TestMessagesAreInvalidIfUserLoosesAdminWhenRequired(t *testing.T) {
	b, alice, _, groupID := createUsersAndGroups(t)

	// Restrict posting to only admins
	err := b.RestrictPosting(groupID)
	assert.NoError(t, err)

	// Make Alice an admin
	aliceID := alice.currentUserID()
	err = b.PromoteGroupAdmin(groupID, aliceID)
	assert.NoError(t, err)

	// Create an update group to demote Alice
	demotion := &updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix() + 1,
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
		WrittenAt:   time.Now().Unix() + 1,
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
	b.handleCatchUp(b.network.Address(), cuPayload, false)

	// Make sure the message was not saved
	var delivered groupMessage
	err = b.database.First(&delivered, "id = ?", alicePost.ID).Error
	assert.Error(t, err)
}

func TestAddingConflictToHistoryStackIsIgnored(t *testing.T) {
	b, alice, _, groupID := createUsersAndGroups(t)

	// Create a canonical stack
	initialState, addressMap, revokedMap, err := b.createInitialGroupState(groupID)
	assert.NoError(t, err)
	stack := newCanonicalStack(initialState, addressMap, revokedMap, b.currentUserID())

	// Add a restriction to editing permissions to the stack
	restrictEditing := updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix() + 1,
		Type:      updateGroupTypeChangeGroupEditsPermission,
		Data:      []byte{permissionRestricted},
	}
	restrictEditing.OriginalPayload, err = msgpack.Marshal(restrictEditing)
	assert.NoError(t, err)
	sc := b.createSignedContainer(restrictEditing.OriginalPayload)
	restrictEditing.Signature = sc.Signature
	restrictEditing.Signer = sc.Signer
	b.insertUpdateGroupIntoStack(stack, restrictEditing)

	// Make sure history only has the group creation at the beginning and the restriction on edits
	top, err := stack.top()
	assert.NoError(t, err)
	assert.Equal(t, true, top.editingRestricted)
	assert.Equal(t, 2, len(stack.history))

	// Try to insert an update group that isn't allowed
	unauthorizedEdit := updateGroup{
		ID:        uuid.New(),
		Actor:     alice.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix() + 1,
		Type:      updateGroupTypeChangeName,
		Data:      []byte("New Name"),
	}
	unauthorizedEdit.OriginalPayload, err = msgpack.Marshal(unauthorizedEdit)
	assert.NoError(t, err)
	sc = b.createSignedContainer(unauthorizedEdit.OriginalPayload)
	unauthorizedEdit.Signature = sc.Signature
	unauthorizedEdit.Signer = sc.Signer
	b.insertUpdateGroupIntoStack(stack, unauthorizedEdit)

	// Ensure the size of the history stack didn't change and Alice's update didn't apply
	top, err = stack.top()
	assert.NoError(t, err)
	assert.Equal(t, "Test Group", top.name)
	assert.Equal(t, 2, len(stack.history))
}

func TestUnconfirmedOldChangesCanBeOverwritten(t *testing.T) {
	b, alice, bob, groupID := createUsersAndGroups(t)

	// Make sure history only has the group creation at the beginning
	stack, err := b.currentGroupStack(groupID)
	assert.NoError(t, err)
	b.consensusStore.Lock()
	stackLen := len(stack.history)
	b.consensusStore.Unlock()
	assert.Equal(t, 5, stackLen)

	// Create an update group that restricts edits and insert
	restrictEdits := updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeChangeGroupEditsPermission,
		Data:      []byte{permissionRestricted},
	}
	restrictEdits.OriginalPayload, err = msgpack.Marshal(restrictEdits)
	assert.NoError(t, err)
	sc := b.createSignedContainer(restrictEdits.OriginalPayload)
	restrictEdits.Signature = sc.Signature
	restrictEdits.Signer = sc.Signer
	assert.NoError(t, b.database.Create(&restrictEdits).Error)

	// Create the update in the database and reload the stack, see that it was added to history
	b.reloadGroupConsensus(groupID)
	stack, err = b.currentGroupStack(groupID)
	assert.NoError(t, err)
	b.consensusStore.Lock()
	stackLen = len(stack.history)
	b.consensusStore.Unlock()
	assert.Equal(t, 6, stackLen)

	// Create an edit from Alice that happens later
	unauthorizedEdit := updateGroup{
		ID:        uuid.New(),
		Actor:     alice.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix() + 2,
		Type:      updateGroupTypeChangeName,
		Data:      []byte("New Name"),
	}
	unauthorizedEdit.OriginalPayload, err = msgpack.Marshal(unauthorizedEdit)
	assert.NoError(t, err)
	sc = alice.createSignedContainer(unauthorizedEdit.OriginalPayload)
	unauthorizedEdit.Signature = sc.Signature
	unauthorizedEdit.Signer = sc.Signer
	assert.NoError(t, b.database.Create(&unauthorizedEdit).Error)

	// Add confirmations to this later update
	myConfirmation := confirmation{
		ID:            uuid.New(),
		UpdateGroupID: unauthorizedEdit.ID,
		Destination:   groupID,
		Author:        b.currentUserID(),
		SigningDevice: b.network.Address(),
		Signature:     b.network.Sign(unauthorizedEdit.ID[:]),
		Timestamp:     time.Now().Unix() + 2,
	}
	assert.NoError(t, b.database.Create(&myConfirmation).Error)

	bobConfirmation := confirmation{
		ID:            uuid.New(),
		UpdateGroupID: unauthorizedEdit.ID,
		Destination:   groupID,
		Author:        bob.currentUserID(),
		SigningDevice: bob.network.Address(),
		Signature:     bob.network.Sign(unauthorizedEdit.ID[:]),
		Timestamp:     time.Now().Unix() + 2,
	}
	assert.NoError(t, b.database.Create(&bobConfirmation).Error)

	// Reload the stack, ensure there are the same number of changes
	b.reloadGroupConsensus(groupID)
	stack, err = b.currentGroupStack(groupID)
	assert.NoError(t, err)
	b.consensusStore.Lock()
	stackLen = len(stack.history)
	b.consensusStore.Unlock()
	assert.Equal(t, 6, stackLen)

	// Get the final state and check that the name edit applied and the restriction did not
	gs, err := stack.top()
	assert.NoError(t, err)
	assert.Equal(t, "New Name", gs.name)
	assert.Equal(t, false, gs.editingRestricted)
}

func TestTimestampWinsWhenBothConfirmed(t *testing.T) {
	b, alice, bob, groupID := createUsersAndGroups(t)

	// Make sure history only has the group creation at the beginning and the restriction on edits
	stack, err := b.currentGroupStack(groupID)
	assert.NoError(t, err)
	b.consensusStore.Lock()
	stackLen := len(stack.history)
	b.consensusStore.Unlock()
	assert.Equal(t, 5, stackLen)

	// Create an update group that restricts edits and insert
	restrictEdits := updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeChangeGroupEditsPermission,
		Data:      []byte{permissionRestricted},
	}
	restrictEdits.OriginalPayload, err = msgpack.Marshal(restrictEdits)
	assert.NoError(t, err)
	sc := b.createSignedContainer(restrictEdits.OriginalPayload)
	restrictEdits.Signature = sc.Signature
	restrictEdits.Signer = sc.Signer
	assert.NoError(t, b.database.Create(&restrictEdits).Error)

	// Create the update in the database and reload the stack, see that it was added to history
	b.reloadGroupConsensus(groupID)
	stack, err = b.currentGroupStack(groupID)
	assert.NoError(t, err)
	b.consensusStore.Lock()
	stackLen = len(stack.history)
	b.consensusStore.Unlock()
	assert.Equal(t, 6, stackLen)

	// Add confirmations for it
	aliceConfirmation := confirmation{
		ID:            uuid.New(),
		UpdateGroupID: restrictEdits.ID,
		Destination:   groupID,
		Author:        alice.currentUserID(),
		SigningDevice: alice.network.Address(),
		Signature:     alice.network.Sign(restrictEdits.ID[:]),
		Timestamp:     time.Now().Unix(),
	}
	assert.NoError(t, b.database.Create(&aliceConfirmation).Error)

	bobConfirmation := confirmation{
		ID:            uuid.New(),
		UpdateGroupID: restrictEdits.ID,
		Destination:   groupID,
		Author:        bob.currentUserID(),
		SigningDevice: bob.network.Address(),
		Signature:     bob.network.Sign(restrictEdits.ID[:]),
		Timestamp:     time.Now().Unix(),
	}
	assert.NoError(t, b.database.Create(&bobConfirmation).Error)

	// Create an edit from Alice that happens later
	unauthorizedEdit := updateGroup{
		ID:        uuid.New(),
		Actor:     alice.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix() + 2,
		Type:      updateGroupTypeChangeName,
		Data:      []byte("New Name"),
	}
	unauthorizedEdit.OriginalPayload, err = msgpack.Marshal(unauthorizedEdit)
	assert.NoError(t, err)
	sc = b.createSignedContainer(unauthorizedEdit.OriginalPayload)
	unauthorizedEdit.Signature = sc.Signature
	unauthorizedEdit.Signer = sc.Signer
	assert.NoError(t, b.database.Create(&unauthorizedEdit).Error)

	// Add confirmations to this later update
	myConfirmation := confirmation{
		ID:            uuid.New(),
		UpdateGroupID: unauthorizedEdit.ID,
		Destination:   groupID,
		Author:        b.currentUserID(),
		SigningDevice: b.network.Address(),
		Signature:     b.network.Sign(unauthorizedEdit.ID[:]),
		Timestamp:     time.Now().Unix() + 2,
	}
	assert.NoError(t, b.database.Create(&myConfirmation).Error)

	bobConfirmation = confirmation{
		ID:            uuid.New(),
		UpdateGroupID: unauthorizedEdit.ID,
		Destination:   groupID,
		Author:        bob.currentUserID(),
		SigningDevice: bob.network.Address(),
		Signature:     bob.network.Sign(unauthorizedEdit.ID[:]),
		Timestamp:     time.Now().Unix() + 2,
	}
	assert.NoError(t, b.database.Create(&bobConfirmation).Error)

	// Reload the stack, ensure there are the same number of changes
	b.reloadGroupConsensus(groupID)
	stack, err = b.currentGroupStack(groupID)
	assert.NoError(t, err)
	b.consensusStore.Lock()
	stackLen = len(stack.history)
	b.consensusStore.Unlock()
	assert.Equal(t, 6, stackLen)

	// Get the final state and check that the name edit applied and the restriction did not
	gs, err := stack.top()
	assert.NoError(t, err)
	assert.Equal(t, "Test Group", gs.name)
	assert.Equal(t, true, gs.editingRestricted)
}

func TestUpdatesWithCustomScopesGetDeletedWhenAllDelivered(t *testing.T) {
	b, alice, bob, groupID := createUsersAndGroups(t)

	// Create an update for Alice to remove herself from the group, ensure that is gets custom scoped
	myID := alice.currentUserID()
	removal := &updateGroup{
		ID:        uuid.New(),
		Actor:     alice.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeRemoveUser,
		Data:      myID[:],
	}
	var err error
	removal.OriginalPayload, err = msgpack.Marshal(removal)
	assert.NoError(t, err)
	sc := alice.createSignedContainer(removal.OriginalPayload)
	removal.Signature = sc.Signature
	removal.Signer = sc.Signer

	assert.NoError(t, alice.database.Create(&removal).Error)
	alice.reloadGroupConsensus(groupID)
	alice.writeGroupConsensus(groupID)
	assert.NoError(t, alice.database.First(&removal, "id = ?", removal.ID).Error)
	assert.False(t, removal.CustomScope == uuid.Nil)

	// Create an ack for this update
	a := &ack{
		References: []frameReference{
			frameReference{
				FrameID: removal.ID,
				Type:    typeUpdateGroup,
			},
		},
	}

	// With one user acking, the update and custom scope should still exist
	alice.handleAck(b.network.Address(), a.getPayload(), false)
	assert.NoError(t, alice.database.First(&removal, "id = ?", removal.ID).Error)
	var cs customScope
	assert.NoError(t, alice.database.First(&cs, "id = ?", groupID).Error)

	// After both users ack, the update and custom scope should be deleted
	alice.handleAck(bob.network.Address(), a.getPayload(), false)
	assert.Error(t, alice.database.First(&removal, "id = ?", removal.ID).Error)
	assert.Error(t, alice.database.First(&cs, "id = ?", groupID).Error)
}

func TestCustomScopesGetRemovedWhenReAddedToGroup(t *testing.T) {
	b, alice, _, groupID := createUsersAndGroups(t)

	// Create an update to remove myself from the group, ensure that is gets custom scoped
	myID := alice.currentUserID()
	removal := &updateGroup{
		ID:        uuid.New(),
		Actor:     alice.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeRemoveUser,
		Data:      myID[:],
	}
	var err error
	removal.OriginalPayload, err = msgpack.Marshal(removal)
	assert.NoError(t, err)
	sc := alice.createSignedContainer(removal.OriginalPayload)
	removal.Signature = sc.Signature
	removal.Signer = sc.Signer

	assert.NoError(t, alice.database.Create(&removal).Error)
	alice.reloadGroupConsensus(groupID)
	alice.writeGroupConsensus(groupID)
	assert.NoError(t, alice.database.First(&removal, "id = ?", removal.ID).Error)
	assert.False(t, removal.CustomScope == uuid.Nil)

	// Create an update adding alice back to the group
	var newUser user
	err = alice.database.
		Preload("Devices.Signature").
		Preload(clause.Associations).
		Where("profile = ?", true).First(&newUser).Error
	assert.NoError(t, err)
	newUserBytes, err := msgpack.Marshal(newUser)
	assert.NoError(t, err)
	add := &updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix() + 1,
		Type:      updateGroupTypeInviteUser,
		Data:      newUserBytes,
	}
	add.OriginalPayload, err = msgpack.Marshal(add)
	assert.NoError(t, err)
	sc = b.createSignedContainer(add.OriginalPayload)
	add.Signature = sc.Signature
	add.Signer = sc.Signer

	accept := &updateGroup{
		ID:        uuid.New(),
		Actor:     alice.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix() + 2,
		Type:      updateGroupTypeRespondToInvite,
		Data:      []byte{acceptInvite},
	}
	accept.OriginalPayload, err = msgpack.Marshal(accept)
	assert.NoError(t, err)
	sc = alice.createSignedContainer(accept.OriginalPayload)
	accept.Signature = sc.Signature
	accept.Signer = sc.Signer

	// Re-add Alice to the group via a catch up that contains all of the group history and an add frame
	var gc groupCreation
	assert.NoError(t, b.database.First(&gc, "id = ?", groupID).Error)
	cu := &catchUp{
		Frames: []frame{
			frame{
				ID:      gc.ID,
				Type:    typeGroupCreation,
				Payload: gc.getPayload(),
			},
			frame{
				ID:      removal.ID,
				Type:    typeUpdateGroup,
				Payload: removal.getPayload(),
			},
			frame{
				ID:      add.ID,
				Type:    typeUpdateGroup,
				Payload: add.getPayload(),
			},
			frame{
				ID:      accept.ID,
				Type:    typeUpdateGroup,
				Payload: accept.getPayload(),
			},
		},
	}
	alice.handleCatchUp(b.network.Address(), cu.getPayload(), false)

	// Make sure Alice is now a member of the group again
	state, err := alice.currentGroupState(groupID)
	assert.NoError(t, err)
	assert.True(t, state.isMember(alice.currentUserID()))

	// Ensure the custom scope has been removed from Alice's removal frame
	assert.NoError(t, alice.database.First(&removal, "id = ?", removal.ID).Error)
	assert.True(t, removal.CustomScope == uuid.Nil)
	var cs customScope
	assert.Error(t, alice.database.First(&cs, "id = ?", groupID).Error)

	// Make sure all of the group history is re-shown to the UI
	//await(t, alice, "NewGroupChat") TODO: flaky
	//await(t, alice, "RemoveUser")
	//await(t, alice, "AddUser")
}

func TestCannotBeAddedToGroupByUnknownUser(t *testing.T) {
	b, _, _, _ := createUsersAndGroups(t)

	carol := newBounceUser("Carol")
	assert.NoError(t, carol.CreateGroup(NewGroup{
		Name: "Test Group",
	}))

	var gc groupCreation
	assert.NoError(t, carol.database.First(&gc).Error)

	meUser, _ := b.currentUser()
	var mu user
	mb, _ := msgpack.Marshal(meUser)
	msgpack.Unmarshal(mb, &mu)

	add := &updateGroup{
		ID:        uuid.New(),
		Actor:     carol.currentUserID(),
		Target:    gc.ID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeInviteUser,
		Data:      mb,
	}
	var err error
	add.OriginalPayload, err = msgpack.Marshal(add)
	assert.NoError(t, err)
	sc := carol.createSignedContainer(add.OriginalPayload)
	add.Signature = sc.Signature
	add.Signer = sc.Signer

	cu := &catchUp{
		Frames: []frame{
			frame{
				ID:      gc.ID,
				Type:    typeGroupCreation,
				Payload: gc.getPayload(),
			},
			frame{
				ID:      add.ID,
				Type:    typeUpdateGroup,
				Payload: add.getPayload(),
			},
		},
	}
	b.handleCatchUp(carol.network.Address(), cu.getPayload(), false)

	// I should not have saved any information from this exchange because I don't know Carol
	var g group
	var ug updateGroup
	var u user
	assert.Error(t, b.database.First(&u, "id = ?", carol.currentUserID()).Error)
	assert.Error(t, b.database.First(&gc, "id = ?", gc.ID).Error)
	assert.Error(t, b.database.First(&g, "id = ?", gc.ID).Error)
	assert.Error(t, b.database.First(&ug, "id = ?", add.ID).Error)
}

func TestCanLearnAboutCreatingUserFromGroupInvite(t *testing.T) {
	b, alice, _, _ := createUsersAndGroups(t)
	var err error

	// Carol exists and created a group
	carol := newBounceUser("Carol")
	assert.NoError(t, carol.CreateGroup(NewGroup{
		Name: "Carol Group",
	}))

	var gc groupCreation
	assert.NoError(t, carol.database.First(&gc).Error)
	ts := gc.Timestamp
	ts += 1

	// Carol and Alice are friends
	aliceUser, _ := alice.currentUser()
	var au user
	ab, _ := msgpack.Marshal(aliceUser)
	msgpack.Unmarshal(ab, &au)

	carolUser, _ := carol.currentUser()
	var cu user
	cb, _ := msgpack.Marshal(carolUser)
	msgpack.Unmarshal(cb, &cu)

	assert.NoError(t, alice.database.Create(&cu).Error)
	assert.NoError(t, carol.database.Create(&au).Error)

	// Carol adds alice to the group
	addAlice := &updateGroup{
		ID:        uuid.New(),
		Actor:     carol.currentUserID(),
		Target:    gc.ID,
		Timestamp: ts,
		Type:      updateGroupTypeInviteUser,
		Data:      ab,
	}
	ts += 1
	addAlice.OriginalPayload, err = msgpack.Marshal(addAlice)
	assert.NoError(t, err)
	sc := carol.createSignedContainer(addAlice.OriginalPayload)
	addAlice.Signature = sc.Signature
	addAlice.Signer = sc.Signer

	// Alice accepts the invite to the group
	accept := &updateGroup{
		ID:        uuid.New(),
		Actor:     alice.currentUserID(),
		Target:    gc.ID,
		Timestamp: ts,
		Type:      updateGroupTypeRespondToInvite,
		Data:      []byte{acceptInvite},
	}
	ts += 1
	accept.OriginalPayload, err = msgpack.Marshal(accept)
	assert.NoError(t, err)
	sc = alice.createSignedContainer(accept.OriginalPayload)
	accept.Signature = sc.Signature
	accept.Signer = sc.Signer

	// Carol makes Alice an admin
	aliceID := alice.currentUserID()
	promote := &updateGroup{
		ID:        uuid.New(),
		Actor:     carol.currentUserID(),
		Target:    gc.ID,
		Timestamp: ts,
		Type:      updateGroupTypePromoteAdmin,
		Data:      aliceID[:],
	}
	ts += 1
	promote.OriginalPayload, err = msgpack.Marshal(promote)
	assert.NoError(t, err)
	sc = carol.createSignedContainer(promote.OriginalPayload)
	promote.Signature = sc.Signature
	promote.Signer = sc.Signer

	// Alice adds me to a group Carol created
	meUser, _ := b.currentUser()
	var mu user
	mb, _ := msgpack.Marshal(meUser)
	msgpack.Unmarshal(mb, &mu)

	addMe := &updateGroup{
		ID:        uuid.New(),
		Actor:     alice.currentUserID(),
		Target:    gc.ID,
		Timestamp: ts,
		Type:      updateGroupTypeInviteUser,
		Data:      mb,
	}
	addMe.OriginalPayload, err = msgpack.Marshal(addMe)
	assert.NoError(t, err)
	sc = alice.createSignedContainer(addMe.OriginalPayload)
	addMe.Signature = sc.Signature
	addMe.Signer = sc.Signer

	history := &catchUp{
		Frames: []frame{
			frame{
				ID:      gc.ID,
				Type:    typeGroupCreation,
				Payload: gc.getPayload(),
			},
			frame{
				ID:      addAlice.ID,
				Type:    typeUpdateGroup,
				Payload: addAlice.getPayload(),
			},
			frame{
				ID:      accept.ID,
				Type:    typeUpdateGroup,
				Payload: accept.getPayload(),
			},
			frame{
				ID:      promote.ID,
				Type:    typeUpdateGroup,
				Payload: promote.getPayload(),
			},
			frame{
				ID:      addMe.ID,
				Type:    typeUpdateGroup,
				Payload: addMe.getPayload(),
			},
		},
	}
	b.handleCatchUp(alice.network.Address(), history.getPayload(), false)

	// I should now have this group saved as well as Carol's user
	var g group
	var ug updateGroup
	var u user
	assert.NoError(t, b.database.First(&u, "id = ?", carol.currentUserID()).Error)
	assert.NoError(t, b.database.First(&gc, "id = ?", gc.ID).Error)
	assert.NoError(t, b.database.First(&g, "id = ?", gc.ID).Error)
	assert.NoError(t, b.database.First(&ug, "id = ?", addMe.ID).Error)
}

func TestCanLearnAboutInvitedUserFromGroupInvite(t *testing.T) {
	b, alice, _, groupID := createUsersAndGroups(t)
	var err error

	// Carol exists
	carol := newBounceUser("Carol")
	ts := time.Now().Unix() + 1

	// Carol and Alice are friends
	aliceUser, _ := alice.currentUser()
	var au user
	ab, _ := msgpack.Marshal(aliceUser)
	msgpack.Unmarshal(ab, &au)

	carolUser, _ := carol.currentUser()
	var cu user
	cb, _ := msgpack.Marshal(carolUser)
	msgpack.Unmarshal(cb, &cu)

	assert.NoError(t, alice.database.Create(&cu).Error)
	assert.NoError(t, carol.database.Create(&au).Error)

	// Alice invites Carol to the group
	addCarol := &updateGroup{
		ID:        uuid.New(),
		Actor:     alice.currentUserID(),
		Target:    groupID,
		Timestamp: ts,
		Type:      updateGroupTypeInviteUser,
		Data:      cb,
	}
	ts += 1
	addCarol.OriginalPayload, err = msgpack.Marshal(addCarol)
	assert.NoError(t, err)
	sc := alice.createSignedContainer(addCarol.OriginalPayload)
	addCarol.Signature = sc.Signature
	addCarol.Signer = sc.Signer

	history := &catchUp{
		Frames: []frame{
			frame{
				ID:      addCarol.ID,
				Type:    typeUpdateGroup,
				Payload: addCarol.getPayload(),
			},
		},
	}
	b.handleCatchUp(alice.network.Address(), history.getPayload(), false)

	// I should now have Carol's user
	var u user
	assert.NoError(t, b.database.First(&u, "id = ?", carol.currentUserID()).Error)
}

func TestUserDoesNotJoinGroupWithUnknownUserByDefault(t *testing.T) {
	_, alice, _, groupID := createUsersAndGroups(t)
	var err error

	// Carol exists
	carol := newBounceUser("Carol")
	ts := time.Now().Unix() + 1

	// Carol and Alice are friends
	aliceUser, _ := alice.currentUser()
	var au user
	ab, _ := msgpack.Marshal(aliceUser)
	msgpack.Unmarshal(ab, &au)

	carolUser, _ := carol.currentUser()
	var cu user
	cb, _ := msgpack.Marshal(carolUser)
	msgpack.Unmarshal(cb, &cu)

	assert.NoError(t, alice.database.Create(&cu).Error)
	assert.NoError(t, carol.database.Create(&au).Error)

	// Get the history of the group up until now
	var gc groupCreation
	err = alice.database.First(&gc, "id = ?", groupID).Error
	assert.NoError(t, err)
	carol.handleGroupCreation(alice.network.Address(), gc.getPayload(), false)

	groupHistory := []frame{
		frame{
			ID:      gc.ID,
			Type:    typeGroupCreation,
			Payload: gc.getPayload(),
		},
	}

	var ugs []updateGroup
	err = alice.database.Order("timestamp asc").Where("target = ?", groupID).Find(&ugs).Error
	assert.NoError(t, err)
	for _, ug := range ugs {
		groupHistory = append(
			groupHistory,
			frame{
				ID:      ug.ID,
				Type:    typeUpdateGroup,
				Payload: ug.getPayload(),
			},
		)
	}

	// Alice invites Carol to the group
	addCarol := &updateGroup{
		ID:        uuid.New(),
		Actor:     alice.currentUserID(),
		Target:    groupID,
		Timestamp: ts,
		Type:      updateGroupTypeInviteUser,
		Data:      cb,
	}
	addCarol.OriginalPayload, err = msgpack.Marshal(addCarol)
	assert.NoError(t, err)
	sc := alice.createSignedContainer(addCarol.OriginalPayload)
	addCarol.Signature = sc.Signature
	addCarol.Signer = sc.Signer

	history := &catchUp{
		Frames: append(
			groupHistory,
			frame{
				ID:      addCarol.ID,
				Type:    typeUpdateGroup,
				Payload: addCarol.getPayload(),
			},
		),
	}
	carol.handleCatchUp(alice.network.Address(), history.getPayload(), false)

	// Carol should not have automatically accepted the invite
	gs, err := carol.currentGroupState(groupID)
	assert.NoError(t, err)
	assert.False(t, gs.isMember(carol.currentUserID()))
	assert.True(t, gs.isInvited(carol.currentUserID()))
}
