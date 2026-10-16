package chat

import (
	"bytes"
	"testing"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/alecthomas/assert/v2"
	"github.com/google/uuid"
)

// These tests describe the group key chain (group_key.go): the symmetric keys that a group's members share, that
// rotate away from anyone who is removed, and that nobody outside the group - including the encrypted devices that
// store the group's frames - can derive.

// signUpdateGroup finishes a hand built update group the way applyAndBroadcastUpdateGroup would, so a test can
// deliver the same frame to several engines in whatever order it likes
func signUpdateGroup(b *Bounce, ug *updateGroup) []byte {
	ug.ID = updateGroupID(*ug)
	payload, _ := msgpack.Marshal(ug)
	ug.OriginalPayload = payload
	sc := b.createSignedContainer(payload)
	ug.Signature = sc.Signature
	ug.Signer = sc.Signer
	frame, _ := msgpack.Marshal(*sc)
	return frame
}

// waitForGroupKeys waits until an engine holds at least n keys for a group, which may involve it asking another
// member for them, or waiting a reference flow retry cycle for the group creation to arrive
func waitForGroupKeys(t *testing.T, b *Bounce, groupID uuid.UUID, n int) []groupKey {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		chain := b.groupKeyChain(groupID)
		if len(chain) >= n {
			return chain
		}
		time.Sleep(25 * time.Millisecond)
	}
	chain := b.groupKeyChain(groupID)
	var creations, updates int64
	b.database.Model(&groupCreation{}).Where("id = ?", groupID).Count(&creations)
	b.database.Model(&updateGroup{}).Where("target = ?", groupID).Count(&updates)
	gs, err := b.currentGroupState(groupID)
	t.Fatalf("%s holds %d group keys, wanted at least %d (creations=%d updates=%d member=%v invited=%v stateErr=%v)",
		b.network.Address()[:8], len(chain), n, creations, updates, gs.isMember(b.currentUserID()), gs.isInvited(b.currentUserID()), err)
	return chain
}

func keyIDs(chain []groupKey) [][]byte {
	ids := [][]byte{}
	for _, gk := range chain {
		ids = append(ids, gk.KeyID)
	}
	return ids
}

func sameKeys(a, b []groupKey) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Epoch != b[i].Epoch || !bytes.Equal(a[i].Key, b[i].Key) {
			return false
		}
	}
	return true
}

// fastReferenceRetries shortens the reference flow's retry interval for a test.  sendReferences holds a per peer
// mutex while it waits referenceRetrySeconds (30s) for an ack, so a reference offer queued behind another one,
// such as the one carrying a group creation to a freshly invited user, would otherwise not go out for half a minute.
func fastReferenceRetries(t *testing.T) {
	t.Helper()
	previous := referenceRetrySeconds
	referenceRetrySeconds = 1
	t.Cleanup(func() { referenceRetrySeconds = previous })
}

// introduce makes two engines aware of each other as contacts and connects them
func introduce(t *testing.T, a, b *Bounce) {
	t.Helper()
	aUser, _ := a.currentUser()
	bUser, _ := b.currentUser()
	var aCopy, bCopy user
	ab, _ := msgpack.Marshal(aUser)
	msgpack.Unmarshal(ab, &aCopy)
	bb, _ := msgpack.Marshal(bUser)
	msgpack.Unmarshal(bb, &bCopy)
	assert.NoError(t, a.database.Create(&bCopy).Error)
	assert.NoError(t, b.database.Create(&aCopy).Error)
	a.UserConnectionDesired(bUser.ID)
	b.UserConnectionDesired(aUser.ID)

	// Wait for the connection, so that anything sent next (an invite, and the reference offer carrying the group
	// creation that follows it) lands on a live socket rather than racing the dial
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if a.getRemoteDevice(b.network.Address()).connectedSockets() > 0 && b.getRemoteDevice(a.network.Address()).connectedSockets() > 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("%s and %s never connected", a.network.Address()[:8], b.network.Address()[:8])
}

func TestGroupCreatorHoldsTheInitialKey(t *testing.T) {
	me, _, _, groupID := createUsersAndGroups(t)

	chain := waitForGroupKeys(t, me, groupID, 1)
	assert.Equal(t, 1, len(chain))
	assert.Equal(t, groupID, chain[0].Epoch)
	assert.Equal(t, 0, chain[0].Index)
	assert.Equal(t, groupKeySize, len(chain[0].Key))

	// The creation frame wraps the secret for the creator only, and commits to it
	var gc groupCreation
	assert.NoError(t, me.database.First(&gc, "id = ?", groupID).Error)
	assert.Equal(t, 1, len(gc.KeyWraps))
	assert.Equal(t, me.currentUserID(), gc.KeyWraps[0].UserID)
	assert.Equal(t, 32, len(gc.KeySecretHash))
}

func TestMembersWithoutTheChainRecoverItFromAnotherMember(t *testing.T) {
	// The fixture invites Alice and Bob with hand made frames that carry no key material, so they have to ask
	me, alice, bob, groupID := createUsersAndGroups(t)

	mine := waitForGroupKeys(t, me, groupID, 1)
	alices := waitForGroupKeys(t, alice, groupID, 1)
	bobs := waitForGroupKeys(t, bob, groupID, 1)

	assert.True(t, sameKeys(mine, alices))
	assert.True(t, sameKeys(mine, bobs))
}

func TestAnInviteCarriesTheChainToTheInvitee(t *testing.T) {
	fastReferenceRetries(t)
	me, _, _, groupID := createUsersAndGroups(t)
	waitForGroupKeys(t, me, groupID, 1)

	carol := newBounceUser("Carol")
	t.Cleanup(carol.Shutdown)
	introduce(t, me, carol)

	assert.NoError(t, me.InviteUserToGroup(groupID, carol.currentUserID()))

	// The invite frame itself carries the chain wrapped for Carol, so she does not need to ask anyone
	var invite updateGroup
	assert.NoError(t, me.database.Where("target = ? AND type = ?", groupID, updateGroupTypeInviteUser).Order("timestamp desc").First(&invite).Error)
	assert.Equal(t, 1, len(invite.KeyWraps))
	assert.Equal(t, carol.currentUserID(), invite.KeyWraps[0].UserID)

	carols := waitForGroupKeys(t, carol, groupID, 1)
	assert.True(t, sameKeys(me.groupKeyChain(groupID), carols))
}

func TestRemovingAUserRotatesTheKeyAwayFromThem(t *testing.T) {
	me, alice, bob, groupID := createUsersAndGroups(t)
	waitForGroupKeys(t, me, groupID, 1)
	waitForGroupKeys(t, alice, groupID, 1)
	waitForGroupKeys(t, bob, groupID, 1)

	assert.NoError(t, me.RemoveUserFromGroup(groupID, bob.currentUserID()))

	// The removal carries a secret wrapped for the members who remain, and not for Bob
	var removal updateGroup
	assert.NoError(t, me.database.Where("target = ? AND type = ?", groupID, updateGroupTypeRemoveUser).First(&removal).Error)
	assert.True(t, removal.carriesGroupKeySecret())
	for _, wrap := range removal.KeyWraps {
		assert.NotEqual(t, bob.currentUserID(), wrap.UserID)
	}

	// Everyone who remains derives the same new key
	mine := waitForGroupKeys(t, me, groupID, 2)
	alices := waitForGroupKeys(t, alice, groupID, 2)
	assert.True(t, sameKeys(mine, alices))
	assert.Equal(t, removal.ID, mine[1].Epoch)

	// Bob, who received the removal (that is how he learns he is out), cannot derive it
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, 1, len(bob.groupKeyChain(groupID)))
	_, found := bob.groupKeyByID(groupID, mine[1].KeyID)
	assert.False(t, found)
}

func TestConcurrentRemovalsComposeUnderConsensus(t *testing.T) {
	// Me and Alice are both admins.  Each removes a different user at the same time, each wrapping the new secret
	// for their own, stale, view of the membership: Alice still thinks Bob is a member, and Me still thinks Carol
	// is.  So Bob can unwrap Alice's secret and Carol can unwrap Me's.  Chaining means it does not matter.
	fastReferenceRetries(t)
	me, alice, bob, groupID := createUsersAndGroups(t)
	carol := newBounceUser("Carol")
	t.Cleanup(carol.Shutdown)
	for _, b := range []*Bounce{me, alice, bob} {
		introduce(t, b, carol)
	}
	assert.NoError(t, me.InviteUserToGroup(groupID, carol.currentUserID()))
	waitForGroupKeys(t, carol, groupID, 1)
	assert.NoError(t, carol.AcceptInvite(groupID))
	assert.NoError(t, me.PromoteGroupAdmin(groupID, alice.currentUserID()))
	for _, b := range []*Bounce{me, alice, bob, carol} {
		waitForGroupKeys(t, b, groupID, 1)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		gs, _ := alice.currentGroupState(groupID)
		if gs.isAdmin(alice.currentUserID()) && gs.isMember(carol.currentUserID()) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	base := me.groupKeyChain(groupID)

	// Build both removals from the same starting state, without broadcasting either
	bobID := bob.currentUserID()
	carolID := carol.currentUserID()
	meState, _ := me.currentGroupState(groupID)
	meRemovesBob := &updateGroup{Actor: me.currentUserID(), Target: groupID, Timestamp: time.Now().Unix(), Type: updateGroupTypeRemoveUser, Data: bobID[:]}
	assert.NoError(t, me.attachGroupKeySecret(meRemovesBob, meState, bob.currentUserID()))
	meFrame := signUpdateGroup(me, meRemovesBob)

	aliceState, _ := alice.currentGroupState(groupID)
	aliceRemovesCarol := &updateGroup{Actor: alice.currentUserID(), Target: groupID, Timestamp: time.Now().Unix() + 1, Type: updateGroupTypeRemoveUser, Data: carolID[:]}
	assert.NoError(t, alice.attachGroupKeySecret(aliceRemovesCarol, aliceState, carol.currentUserID()))
	aliceFrame := signUpdateGroup(alice, aliceRemovesCarol)

	// Deliver them in opposite orders to the two remaining members
	me.handleUpdateGroup(alice.network.Address(), aliceFrame, false)
	me.handleUpdateGroup(me.network.Address(), meFrame, false)
	alice.handleUpdateGroup(me.network.Address(), meFrame, false)
	alice.handleUpdateGroup(alice.network.Address(), aliceFrame, false)

	// And both to each removed user
	for _, removed := range []*Bounce{bob, carol} {
		removed.handleUpdateGroup(me.network.Address(), meFrame, false)
		removed.handleUpdateGroup(alice.network.Address(), aliceFrame, false)
	}

	mine := me.groupKeyChain(groupID)
	alices := alice.groupKeyChain(groupID)
	assert.Equal(t, len(base)+2, len(mine))
	assert.True(t, sameKeys(mine, alices), "remaining members must agree on the chain regardless of delivery order")

	// Each removed user could unwrap one of the two secrets, but is missing the other link
	final := mine[len(mine)-1].KeyID
	for _, removed := range []*Bounce{bob, carol} {
		_, found := removed.groupKeyByID(groupID, final)
		assert.False(t, found)
		assert.Equal(t, len(base), len(removed.groupKeyChain(groupID)))
	}
}

func TestLeavingMarksTheChainStaleUntilAMemberRotates(t *testing.T) {
	me, alice, bob, groupID := createUsersAndGroups(t)
	for _, b := range []*Bounce{me, alice, bob} {
		waitForGroupKeys(t, b, groupID, 1)
	}

	// Bob leaves on his own.  He cannot be trusted to rotate for the others, so nothing rotates yet.
	assert.NoError(t, bob.RemoveUserFromGroup(groupID, bob.currentUserID()))
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		gs, _ := me.currentGroupState(groupID)
		if !gs.isMember(bob.currentUserID()) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	gs, _ := me.currentGroupState(groupID)
	assert.True(t, gs.keyStale)
	assert.Equal(t, 1, len(me.groupKeyChain(groupID)))

	// The next member who needs the key rotates it first
	assert.NoError(t, me.ensureGroupKeyFresh(groupID))
	gs, _ = me.currentGroupState(groupID)
	assert.False(t, gs.keyStale)

	mine := waitForGroupKeys(t, me, groupID, 2)
	alices := waitForGroupKeys(t, alice, groupID, 2)
	assert.True(t, sameKeys(mine, alices))
	time.Sleep(200 * time.Millisecond)
	_, found := bob.groupKeyByID(groupID, mine[1].KeyID)
	assert.False(t, found)

	// Rotating again when nothing is stale is a no-op
	assert.NoError(t, me.ensureGroupKeyFresh(groupID))
	assert.Equal(t, 2, len(me.groupKeyChain(groupID)))
}

func TestConcurrentRotationsKeepEveryIntermediateKeyReachable(t *testing.T) {
	// Me and Alice both rotate at once.  Consensus orders the two rotations, but Alice sealed a frame under the
	// key she had after applying only her own rotation, which is not on the canonical chain.  Every member can
	// still reconstruct it, because every secret is wrapped for every member.
	me, alice, _, groupID := createUsersAndGroups(t)
	waitForGroupKeys(t, me, groupID, 1)
	waitForGroupKeys(t, alice, groupID, 1)

	meState, _ := me.currentGroupState(groupID)
	meRotates := &updateGroup{Actor: me.currentUserID(), Target: groupID, Timestamp: time.Now().Unix(), Type: updateGroupTypeRotateKey}
	assert.NoError(t, me.attachGroupKeySecret(meRotates, meState, uuid.Nil))
	meFrame := signUpdateGroup(me, meRotates)

	aliceState, _ := alice.currentGroupState(groupID)
	aliceRotates := &updateGroup{Actor: alice.currentUserID(), Target: groupID, Timestamp: time.Now().Unix() + 1, Type: updateGroupTypeRotateKey}
	assert.NoError(t, alice.attachGroupKeySecret(aliceRotates, aliceState, uuid.Nil))
	aliceFrame := signUpdateGroup(alice, aliceRotates)

	// Alice applies only her own rotation and seals something under the result
	alice.handleUpdateGroup(alice.network.Address(), aliceFrame, false)
	aliceIntermediate, ok := alice.currentGroupKey(groupID)
	assert.True(t, ok)

	// Me learns of both; the canonical order puts Me's rotation first, so Alice's intermediate key is not on
	// Me's chain
	me.handleUpdateGroup(me.network.Address(), meFrame, false)
	me.handleUpdateGroup(alice.network.Address(), aliceFrame, false)
	mine := me.groupKeyChain(groupID)
	assert.Equal(t, 3, len(mine))
	for _, gk := range mine {
		assert.False(t, bytes.Equal(gk.KeyID, aliceIntermediate.KeyID), "the intermediate key must not be canonical for this test to mean anything")
	}

	// But Me can still find it
	reconstructed, found := me.groupKeyByID(groupID, aliceIntermediate.KeyID)
	assert.True(t, found)
	assert.True(t, bytes.Equal(reconstructed, aliceIntermediate.Key))

	// Once Alice learns of Me's rotation, both agree on the canonical chain
	alice.handleUpdateGroup(me.network.Address(), meFrame, false)
	assert.True(t, sameKeys(mine, alice.groupKeyChain(groupID)))
}

func TestAMemberGivenADifferentSecretDetectsTheFork(t *testing.T) {
	// A malicious admin wraps one secret for Me and another for Alice.  The frame commits to one hash, so
	// whoever got the other secret notices and refuses to advance their chain.
	me, alice, _, groupID := createUsersAndGroups(t)
	waitForGroupKeys(t, me, groupID, 1)
	waitForGroupKeys(t, alice, groupID, 1)

	meUser, _ := me.currentUser()
	aliceUser, _ := alice.currentUser()
	var meRow, aliceRow user
	me.database.Select("id", "public_ecdh_key").First(&meRow, "id = ?", meUser.ID)
	me.database.Select("id", "public_ecdh_key").First(&aliceRow, "id = ?", aliceUser.ID)

	honest, hash := newGroupKeySecret()
	forged, _ := newGroupKeySecret()
	ephemeralForMe, meWraps, err := wrapGroupKeyMaterial(honest, groupKeyDomainSecret, []user{meRow})
	assert.NoError(t, err)
	_, aliceWraps, err := wrapGroupKeyMaterial(forged, groupKeyDomainSecret, []user{aliceRow})
	assert.NoError(t, err)
	// Both wraps claim the same ephemeral key; only Me's actually opens under it, Alice's opens under its own
	// ephemeral, which we splice in to simulate an author handing out two secrets
	rotation := &updateGroup{Actor: me.currentUserID(), Target: groupID, Timestamp: time.Now().Unix(), Type: updateGroupTypeRotateKey,
		KeyEphemeralKey: ephemeralForMe, KeyWraps: append(meWraps, aliceWraps...), KeySecretHash: hash}
	frame := signUpdateGroup(me, rotation)

	me.handleUpdateGroup(me.network.Address(), frame, false)
	alice.handleUpdateGroup(me.network.Address(), frame, false)

	assert.Equal(t, 2, len(me.groupKeyChain(groupID)))
	// Alice's wrap does not open under the frame's ephemeral key at all, which is one way a forged secret
	// fails; a wrap that opens to a secret with the wrong hash fails the same way
	assert.Equal(t, 1, len(alice.groupKeyChain(groupID)))
}

func TestKeysAreStableAcrossAConsensusReload(t *testing.T) {
	me, alice, bob, groupID := createUsersAndGroups(t)
	for _, b := range []*Bounce{me, alice, bob} {
		waitForGroupKeys(t, b, groupID, 1)
	}
	assert.NoError(t, me.RemoveUserFromGroup(groupID, bob.currentUserID()))
	before := waitForGroupKeys(t, me, groupID, 2)

	assert.NoError(t, me.reloadGroupConsensus(groupID))
	assert.NoError(t, me.writeGroupConsensus(groupID))

	after := me.groupKeyChain(groupID)
	assert.True(t, sameKeys(before, after))
	assert.Equal(t, keyIDs(before), keyIDs(after))
}
