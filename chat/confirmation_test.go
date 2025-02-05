package chat

import (
	"testing"

	"github.com/alecthomas/assert/v2"
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
