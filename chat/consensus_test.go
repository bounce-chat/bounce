package chat

import (
	"testing"

	"github.com/alecthomas/assert/v2"
)

func TestCanRenameGroup(t *testing.T) {
	b, alice, bob, groupID := createUsersAndGroups(t)

	newName := "New Name"
	b.renameGroup(groupID, newName)

	var g group
	err := b.database.First(&g, "id = ?", groupID).Error
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
}
