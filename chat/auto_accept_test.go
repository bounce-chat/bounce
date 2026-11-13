package chat

import (
	"testing"
	"time"

	"github.com/alecthomas/assert/v2"
)

// Whether an invitation is accepted automatically is a profile setting: never, always, or only when everyone in the
// group is already an accepted contact.  Accepting automatically does everything accepting by hand does: the members
// become accepted contacts and the group is told at once, rather than whenever the reference flow next runs.

// setAutoJoin applies the setting and then waits for a later second: the setting only governs invitations that
// arrive after it was changed, and both are stamped in whole seconds
func setAutoJoin(t *testing.T, b *Bounce, value int) {
	t.Helper()
	b.SetAutoJoinGroups(value)
	settingSecond := time.Now().Unix()
	for time.Now().Unix() <= settingSecond {
		time.Sleep(50 * time.Millisecond)
	}
}

// inviteToNewGroup has the creator make a group inviting the given users, and waits until the invitee holds its
// creation, which is the moment the invitee decides whether to join.  A creation is broadcast before its invitations
// exist, so the invitee only learns of the group through the reference flow; callers use quickReferenceRetries.
func inviteToNewGroup(t *testing.T, creator, invitee *Bounce, name string, invitees ...ID) ID {
	t.Helper()
	assert.NoError(t, creator.CreateGroup(NewGroup{Name: name, InitialInvites: invitees, Retention: 3600}))
	var created group
	assert.NoError(t, creator.database.Where("name = ?", name).First(&created).Error)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if invitee.haveGroupCreation(created.ID) {
			return created.ID
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("the invitee never received the group creation")
	return NilID
}

func acceptances(b *Bounce, groupID, by ID) int64 {
	var n int64
	b.database.Model(&updateGroup{}).Where("target = ? AND type = ? AND actor = ?", groupID, updateGroupTypeRespondToInvite, by).Count(&n)
	return n
}

func TestAnAutomaticAcceptanceIsBroadcastAndAcceptsTheMembers(t *testing.T) {
	quickReferenceRetries(t)
	me, _, bob, _ := createUsersAndGroups(t)
	setAutoJoin(t, bob, AlwaysAutoJoinGroups)
	assert.NoError(t, bob.database.Table("users").Where("id = ?", me.currentUserID()).Update("accepted", false).Error)

	groupID := inviteToNewGroup(t, me, bob, "Auto", bob.currentUserID())

	// Bob's acceptance reaches me as a live update, and bob now counts me as accepted
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && acceptances(me, groupID, bob.currentUserID()) == 0 {
		time.Sleep(25 * time.Millisecond)
	}
	assert.Equal(t, int64(1), acceptances(me, groupID, bob.currentUserID()), "bob's automatic acceptance never reached me")
	assert.True(t, bob.userIsInGroup(groupID, bob.currentUserID()))
	var meAtBob user
	assert.NoError(t, bob.database.Select("accepted").Take(&meAtBob, "id = ?", me.currentUserID()).Error)
	assert.True(t, meAtBob.Accepted, "joining a group should accept its members")
}

func TestNoAutomaticAcceptanceWhenAutoJoinIsOff(t *testing.T) {
	quickReferenceRetries(t)
	me, _, bob, _ := createUsersAndGroups(t)
	setAutoJoin(t, bob, NeverAutoJoinGroups)

	groupID := inviteToNewGroup(t, me, bob, "Manual", bob.currentUserID())
	time.Sleep(time.Second) // long enough for a decision bob is not going to make

	assert.True(t, bob.isInvited(groupID, bob.currentUserID()), "bob should still be invited")
	assert.False(t, bob.userIsInGroup(groupID, bob.currentUserID()), "bob joined with auto-join off")
	assert.Equal(t, int64(0), acceptances(bob, groupID, bob.currentUserID()))
}

func TestAutoJoinWithNoNewUsersDependsOnKnowingEveryone(t *testing.T) {
	quickReferenceRetries(t)
	me, alice, bob, _ := createUsersAndGroups(t)
	setAutoJoin(t, bob, OnlyAutoJoinGroupsWithNoNewUsers)
	assert.NoError(t, bob.database.Table("users").Where("id = ?", me.currentUserID()).Update("accepted", true).Error)
	assert.NoError(t, bob.database.Table("users").Where("id = ?", alice.currentUserID()).Update("accepted", false).Error)

	// A group of people bob has accepted: he joins
	familiar := inviteToNewGroup(t, me, bob, "Familiar", bob.currentUserID())
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && !bob.userIsInGroup(familiar, bob.currentUserID()) {
		time.Sleep(25 * time.Millisecond)
	}
	assert.True(t, bob.userIsInGroup(familiar, bob.currentUserID()), "bob did not join a group of accepted contacts")

	// A group with someone he has not accepted: he waits to be asked
	unfamiliar := inviteToNewGroup(t, me, bob, "Unfamiliar", bob.currentUserID(), alice.currentUserID())
	time.Sleep(time.Second)
	assert.True(t, bob.isInvited(unfamiliar, bob.currentUserID()), "bob should still be invited")
	assert.False(t, bob.userIsInGroup(unfamiliar, bob.currentUserID()), "bob joined a group with an unaccepted member")
	assert.Equal(t, int64(0), acceptances(bob, unfamiliar, bob.currentUserID()))
}
