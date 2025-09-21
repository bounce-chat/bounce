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
	userIDs := []uuid.UUID{b.currentUserID(), alice.currentUserID(), bob.currentUserID()}

	newName := "New Name"
	b.RenameGroup(groupID, newName)
	var ug updateGroup
	err := b.database.Select("id").First(&ug, "target = ? AND type = ?", groupID, updateGroupTypeChangeName).Error
	assert.NoError(t, err)

	var g group
	err = b.database.First(&g, "id = ?", groupID).Error
	assert.NoError(t, err)
	assert.Equal(t, newName, g.Name)

	await(t, alice, "RenameGroup")
	err = alice.database.First(&g, "id = ?", groupID).Error
	assert.NoError(t, err)
	assert.Equal(t, newName, g.Name)

	await(t, bob, "RenameGroup")
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
	awaitAck(t, alice, b, typeConfirmation, ac.ID)
	awaitAck(t, bob, b, typeConfirmation, bc.ID)
	err = b.database.Preload(clause.Associations).First(&ug, "target = ? AND type = ?", groupID, updateGroupTypeChangeName).Error
	assert.NoError(t, err)
	assert.Equal(t, 3, ug.confirmingUsers(userIDs))

	// Alice sees three confirmations
	awaitAck(t, bob, alice, typeConfirmation, bc.ID)
	var aug updateGroup
	err = alice.database.Preload(clause.Associations).First(&aug, "target = ? AND type = ?", groupID, updateGroupTypeChangeName).Error
	assert.NoError(t, err)
	assert.Equal(t, 3, aug.confirmingUsers(userIDs))

	// Bob sees three confirmations
	awaitAck(t, alice, bob, typeConfirmation, ac.ID)
	var bug updateGroup
	err = bob.database.Preload(clause.Associations).First(&bug, "target = ? AND type = ?", groupID, updateGroupTypeChangeName).Error
	assert.NoError(t, err)
	assert.Equal(t, 3, bug.confirmingUsers(userIDs))
}

func TestEarlyConfirmationWorks(t *testing.T) {
	b, alice, bob, groupID := createUsersAndGroups(t)
	userIDs := []uuid.UUID{b.currentUserID(), alice.currentUserID(), bob.currentUserID()}

	// Make an arbitrary update group
	restrictEdits := updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeChangeGroupEditsPermission,
		Data:      []byte{permissionRestricted},
	}
	var err error
	restrictEdits.OriginalPayload, err = msgpack.Marshal(restrictEdits)
	assert.NoError(t, err)
	sc := b.createSignedContainer(restrictEdits.OriginalPayload)
	restrictEdits.Signature = sc.Signature
	restrictEdits.Signer = sc.Signer

	// Create a confirmation from Alice
	aliceConfirmation := confirmation{
		ID:            uuid.New(),
		UpdateGroupID: restrictEdits.ID,
		Destination:   groupID,
		Author:        alice.currentUserID(),
		SigningDevice: alice.network.Address(),
		Signature:     alice.network.Sign(restrictEdits.ID[:]),
		Timestamp:     time.Now().Unix(),
	}

	// Handle the confirmation, ensure it gets saved
	fr, _ := b.handleConfirmation(alice.network.Address(), aliceConfirmation.getPayload(), false)
	assert.True(t, fr == nil)
	var c confirmation
	assert.NoError(t, b.database.First(&c, "id = ?", aliceConfirmation.ID).Error)

	// Handle the update group and ensure that it gets saved
	fr, _ = b.handleUpdateGroup(alice.network.Address(), restrictEdits.getPayload(), false)
	assert.False(t, fr == nil)

	// Load the update from the database and see that it already has a confirmation
	var ug updateGroup
	assert.NoError(t, b.database.Preload(clause.Associations).First(&ug, "id = ?", restrictEdits.ID).Error)
	assert.Equal(t, 2, ug.confirmingUsers(userIDs))
}
