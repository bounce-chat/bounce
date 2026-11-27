package chat

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/alecthomas/assert/v2"
)

func TestUnverifiedUpdateGroupQuotaGatedPerPeerEvictingAndUnforgeablePrune(t *testing.T) {
	origPerPeer := maximumUnverifiedUpdateGroupsPerPeer
	origTotal := maximumUnverifiedUpdateGroups
	maximumUnverifiedUpdateGroupsPerPeer = 3
	maximumUnverifiedUpdateGroups = 5
	t.Cleanup(func() {
		maximumUnverifiedUpdateGroupsPerPeer = origPerPeer
		maximumUnverifiedUpdateGroups = origTotal
	})

	me, alice, bob, _ := createUsersAndGroups(t)
	now := time.Now().Unix()

	// 1. An unknown stranger sending unverified updateGroups in an unsolicited catch-up must store 0 rows,
	// even if they forge Actor == me or InviteUser(me).
	stranger := bnc01NewOnion()
	meRecord, ok := me.currentUser()
	assert.True(t, ok)
	meBytes, err := msgpack.Marshal(meRecord)
	assert.NoError(t, err)

	strangerForgedActor := &updateGroup{
		Nonce:     newNonce(),
		Actor:     me.currentUserID(),
		Target:    NewID(),
		Timestamp: now,
		Type:      updateGroupTypeChangeName,
		Data:      []byte("forged-actor"),
	}
	strangerForgedActor.ID = strangerForgedActor.generateID()
	strangerForgedInvite := &updateGroup{
		Nonce:     newNonce(),
		Actor:     NewID(),
		Target:    NewID(),
		Timestamp: now,
		Type:      updateGroupTypeInviteUser,
		Data:      meBytes,
	}
	strangerForgedInvite.ID = strangerForgedInvite.generateID()

	cu := catchUp{
		Frames: []frame{
			{ID: strangerForgedActor.ID, Type: typeUpdateGroup, Payload: bnc01OnionFrame(stranger, strangerForgedActor)},
			{ID: strangerForgedInvite.ID, Type: typeUpdateGroup, Payload: bnc01OnionFrame(stranger, strangerForgedInvite)},
		},
	}
	me.handleCatchUp(stranger.addr(), cu.getPayload(), false)

	var strangerStored int64
	assert.NoError(t, me.database.Model(&updateGroup{}).
		Where("id IN ?", []ID{strangerForgedActor.ID, strangerForgedInvite.ID}).
		Count(&strangerStored).Error)
	assert.Equal(t, int64(0), strangerStored)

	// 2. A known contact (alice) using 5 distinct throwaway signers is still bounded by the per-serving-peer cap (3).
	aliceIDs := make([]ID, 0, 5)
	for i := 0; i < 5; i++ {
		throwaway := bnc01NewOnion()
		ug := &updateGroup{
			Nonce:     newNonce(),
			Actor:     me.currentUserID(), // forged Actor == me by unknown signer
			Target:    NewID(),
			Timestamp: now + int64(i),
			Type:      updateGroupTypeChangeName,
			Data:      []byte(fmt.Sprintf("spam-%d", i)),
		}
		ug.ID = ug.generateID()
		aliceIDs = append(aliceIDs, ug.ID)
		me.handleUpdateGroup(alice.network.Address(), bnc01OnionFrame(throwaway, ug), true)
	}

	var aliceUnverified int64
	assert.NoError(t, me.database.Model(&updateGroup{}).Where("id IN ?", aliceIDs).Count(&aliceUnverified).Error)
	assert.Equal(t, int64(3), aliceUnverified)

	// 3. Bob sends 3 unverified updates; reaching the global cap of 5 evicts Alice's oldest unverified row
	// rather than rejecting Bob's legitimate unverified update.
	bobIDs := make([]ID, 0, 3)
	for i := 0; i < 3; i++ {
		throwaway := bnc01NewOnion()
		ug := &updateGroup{
			Nonce:     newNonce(),
			Actor:     NewID(),
			Target:    NewID(),
			Timestamp: now + int64(10+i),
			Type:      updateGroupTypeInviteUser,
			Data:      meBytes, // forged InviteUser(me) by unknown signer
		}
		ug.ID = ug.generateID()
		bobIDs = append(bobIDs, ug.ID)
		me.handleUpdateGroup(bob.network.Address(), bnc01OnionFrame(throwaway, ug), true)
	}

	var bobStored, aliceRemaining int64
	assert.NoError(t, me.database.Model(&updateGroup{}).Where("id IN ?", bobIDs).Count(&bobStored).Error)
	assert.Equal(t, int64(3), bobStored, "global cap should evict oldest unverified rows to admit new ones")
	assert.NoError(t, me.database.Model(&updateGroup{}).Where("id IN ?", aliceIDs).Count(&aliceRemaining).Error)
	assert.Equal(t, int64(2), aliceRemaining, "oldest row from alice should have been evicted")

	// 4. Add a genuine known-signer (alice) InviteUser(me) for an unknown group, age all unverified rows past
	// unverifiedUpdateGroupLifetime, and prune.  Forged unknown-signer exemptions must be pruned; known-signer invite kept.
	realInvite := &updateGroup{
		Nonce:     newNonce(),
		Actor:     alice.currentUserID(),
		Target:    NewID(),
		Timestamp: now + 50,
		Type:      updateGroupTypeInviteUser,
		Data:      meBytes,
	}
	realInvite.ID = realInvite.generateID()
	me.handleUpdateGroup(alice.network.Address(), bnc01BounceFrame(alice, realInvite), true)

	allTestIDs := append(append([]ID{}, aliceIDs...), bobIDs...)
	allTestIDs = append(allTestIDs, realInvite.ID)
	assert.NoError(t, me.database.Model(&updateGroup{}).Where("id IN ?", allTestIDs).Update("saved_at", int64(1)).Error)

	me.pruneUnverifiedUpdateGroups()

	var forgedRemaining int64
	assert.NoError(t, me.database.Model(&updateGroup{}).Where("id IN ?", append(aliceIDs, bobIDs...)).Count(&forgedRemaining).Error)
	assert.Equal(t, int64(0), forgedRemaining, "unknown-signer rows with forged Actor==me or InviteUser(me) must be pruned")

	var realInviteRemaining int64
	assert.NoError(t, me.database.Model(&updateGroup{}).Where("id = ?", realInvite.ID).Count(&realInviteRemaining).Error)
	assert.Equal(t, int64(1), realInviteRemaining, "known-signer invite naming us must survive pruning")
}

func TestRemovedMemberUpdateGroupsValidatedCappedAndPruned(t *testing.T) {
	origMaxRows := maximumUnappliedUpdateGroupsPerActor
	origMaxBytes := maximumUnappliedUpdateGroupBytesPerActor
	maximumUnappliedUpdateGroupsPerActor = 4
	maximumUnappliedUpdateGroupBytesPerActor = 4 * 1024
	t.Cleanup(func() {
		maximumUnappliedUpdateGroupsPerActor = origMaxRows
		maximumUnappliedUpdateGroupBytesPerActor = origMaxBytes
	})

	me, alice, bob, groupID := createUsersAndGroups(t)

	// Remove Bob from the group and wait until he is no longer a member or invitee in Me's consensus state.
	assert.NoError(t, me.RemoveUserFromGroup(groupID, bob.currentUserID()))
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		gs, err := me.currentGroupState(groupID)
		if err == nil && !gs.isMember(bob.currentUserID()) && !gs.isInvited(bob.currentUserID()) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	gs, err := me.currentGroupState(groupID)
	assert.NoError(t, err)
	assert.False(t, gs.isMember(bob.currentUserID()))

	now := time.Now().Unix()

	// 1. An InviteUser with an invalid user record (e.g. bloated name) is rejected by validPayloadFormat before saving.
	badUser := user{
		ID:   NewID(),
		Name: strings.Repeat("A", 1024),
	}
	badUserBytes, err := msgpack.Marshal(badUser)
	assert.NoError(t, err)
	badInvite := &updateGroup{
		Nonce:     newNonce(),
		Actor:     bob.currentUserID(),
		Target:    groupID,
		Timestamp: now + 100,
		Type:      updateGroupTypeInviteUser,
		Data:      badUserBytes,
	}
	badInvite.ID = badInvite.generateID()
	br, accepted := me.handleUpdateGroup(bob.network.Address(), bnc01BounceFrame(bob, badInvite), false)
	assert.False(t, accepted)
	assert.True(t, br == nil)
	var badCount int64
	assert.NoError(t, me.database.Model(&updateGroup{}).Where("id = ?", badInvite.ID).Count(&badCount).Error)
	assert.Equal(t, int64(0), badCount)

	// 2. Validly formatted unapplied updates from removed member Bob are capped at maximumUnappliedUpdateGroupsPerActor (4).
	bobUpdateIDs := make([]ID, 0, 6)
	for i := 0; i < 6; i++ {
		ug := &updateGroup{
			Nonce:     newNonce(),
			Actor:     bob.currentUserID(),
			Target:    groupID,
			Timestamp: now + int64(200+i),
			Type:      updateGroupTypeChangeName,
			Data:      []byte(fmt.Sprintf("hijack-%d", i)),
		}
		ug.ID = ug.generateID()
		bobUpdateIDs = append(bobUpdateIDs, ug.ID)
		me.handleUpdateGroup(bob.network.Address(), bnc01BounceFrame(bob, ug), false)
	}

	var bobStored int64
	assert.NoError(t, me.database.Model(&updateGroup{}).Where("id IN ?", bobUpdateIDs).Count(&bobStored).Error)
	assert.Equal(t, int64(4), bobStored, "removed member's unapplied updates must be capped at the per-actor row limit")

	// 3. Also verify the byte cap on valid InviteUser records from Bob.
	assert.NoError(t, deleteEach[updateGroup](me.database, "id IN ?", bobUpdateIDs))
	validInviteTarget := bnc01SyntheticUser("ValidInvitee", bnc01NewOnion(), now)
	// Pad Images up to the valid limit (20 IDs ~1.3 KiB) so 2 invites (~3 KiB with OriginalPayload) fit under 4 KiB
	// while the 3rd exceeds the 4 KiB byte cap before hitting the 4-row cap.
	imgList := make([]string, 20)
	for i := range imgList {
		imgList[i] = NewID().String()
	}
	validInviteTarget.Images = strings.Join(imgList, ",")
	validInviteBytes, err := msgpack.Marshal(validInviteTarget)
	assert.NoError(t, err)

	bobInviteIDs := make([]ID, 0, 4)
	for i := 0; i < 4; i++ {
		ug := &updateGroup{
			Nonce:     newNonce(),
			Actor:     bob.currentUserID(),
			Target:    groupID,
			Timestamp: now + int64(300+i),
			Type:      updateGroupTypeInviteUser,
			Data:      validInviteBytes,
		}
		ug.ID = ug.generateID()
		bobInviteIDs = append(bobInviteIDs, ug.ID)
		me.handleUpdateGroup(bob.network.Address(), bnc01BounceFrame(bob, ug), false)
	}
	var bobInviteStored int64
	assert.NoError(t, me.database.Model(&updateGroup{}).Where("id IN ?", bobInviteIDs).Count(&bobInviteStored).Error)
	assert.True(t, bobInviteStored > 0 && bobInviteStored < 4, "byte cap should stop large unapplied invites before the row cap")

	// 4. Pruning removes aged unapplied updates from removed member Bob, while keeping an aged unapplied update
	// authored by current member Alice.
	aliceUnapplied := updateGroup{
		SignedFrame: SignedFrame{
			OriginalPayload: []byte("alice-unapplied"),
			Signature:       []byte("sig"),
			Signer:          alice.network.Address(),
		},
		Nonce:     newNonce(),
		Actor:     alice.currentUserID(),
		Target:    groupID,
		Timestamp: now + 400,
		Type:      updateGroupTypeChangeName,
		Data:      []byte("alice-rolled-back"),
		Applied:   false,
	}
	aliceUnapplied.ID = aliceUnapplied.generateID()
	assert.NoError(t, me.database.Omit("Confirmations").Create(&aliceUnapplied).Error)

	assert.NoError(t, me.database.Model(&updateGroup{}).Where("id IN ?", append(bobInviteIDs, aliceUnapplied.ID)).Update("saved_at", int64(1)).Error)
	me.pruneUnverifiedUpdateGroups()

	var bobAfterPrune, aliceAfterPrune int64
	assert.NoError(t, me.database.Model(&updateGroup{}).Where("id IN ?", bobInviteIDs).Count(&bobAfterPrune).Error)
	assert.Equal(t, int64(0), bobAfterPrune, "aged unapplied rows from a removed member must be pruned")
	assert.NoError(t, me.database.Model(&updateGroup{}).Where("id = ?", aliceUnapplied.ID).Count(&aliceAfterPrune).Error)
	assert.Equal(t, int64(1), aliceAfterPrune, "aged unapplied row from a current member must be preserved")
}

func TestStrangerCanDeliverCatchUpForContactsInvite(t *testing.T) {
	me, alice, _, _ := createUsersAndGroups(t)

	// Carol is unknown to me; Carol creates a group, invites Alice (my contact), and Alice invites me.
	carol := newBounceUser("Carol")
	t.Cleanup(carol.Shutdown)
	assert.NoError(t, carol.CreateGroup(NewGroup{Name: "Carol Group"}))

	var gc groupCreation
	assert.NoError(t, carol.database.First(&gc).Error)
	ts := gc.Timestamp + 1

	aliceUser, ok := alice.currentUser()
	assert.True(t, ok)
	aliceBytes, err := msgpack.Marshal(aliceUser)
	assert.NoError(t, err)
	meUser, ok := me.currentUser()
	assert.True(t, ok)
	meBytes, err := msgpack.Marshal(meUser)
	assert.NoError(t, err)

	addAlice := &updateGroup{
		Nonce:     newNonce(),
		Actor:     carol.currentUserID(),
		Target:    gc.ID,
		Timestamp: ts,
		Type:      updateGroupTypeInviteUser,
		Data:      aliceBytes,
	}
	addAlice.ID = addAlice.generateID()
	ts++

	acceptAlice := &updateGroup{
		Nonce:     newNonce(),
		Actor:     alice.currentUserID(),
		Target:    gc.ID,
		Timestamp: ts,
		Type:      updateGroupTypeRespondToInvite,
		Data:      []byte{acceptInvite},
	}
	acceptAlice.ID = acceptAlice.generateID()
	ts++

	addMe := &updateGroup{
		Nonce:     newNonce(),
		Actor:     alice.currentUserID(),
		Target:    gc.ID,
		Timestamp: ts,
		Type:      updateGroupTypeInviteUser,
		Data:      meBytes,
	}
	addMe.ID = addMe.generateID()

	// Carol (still an unknown peer to me) delivers the catch-up while Alice is absent.
	history := &catchUp{
		Frames: []frame{
			{ID: gc.ID, Type: typeGroupCreation, Payload: gc.getPayload()},
			{ID: addAlice.ID, Type: typeUpdateGroup, Payload: bnc01BounceFrame(carol, addAlice)},
			{ID: acceptAlice.ID, Type: typeUpdateGroup, Payload: bnc01BounceFrame(alice, acceptAlice)},
			{ID: addMe.ID, Type: typeUpdateGroup, Payload: bnc01BounceFrame(alice, addMe)},
		},
	}
	me.handleCatchUp(carol.network.Address(), history.getPayload(), false)

	var g group
	var carolRow user
	assert.NoError(t, me.database.First(&g, "id = ?", gc.ID).Error, "stranger-delivered catch-up carrying a contact's invite must join the group")
	assert.NoError(t, me.database.First(&carolRow, "id = ?", carol.currentUserID()).Error, "group creator should be learned from the valid invite chain")
}

type countingVerifyNetwork struct {
	*testnet
	verifyCalls atomic.Int64
}

func (c *countingVerifyNetwork) VerifySignature(address string, message []byte, signature []byte) bool {
	c.verifyCalls.Add(1)
	return c.testnet.VerifySignature(address, message, signature)
}

func TestConsensusReplayDoesNotReVerifyCanonicalInviteDeviceGroups(t *testing.T) {
	net := &countingVerifyNetwork{testnet: newTestnet()}
	me := newBounceOnNetwork(newTestUI(), net)
	me.SetProfile("Me", []byte{}, "test")

	_, alice, _, groupID := createUsersAndGroupsFor(t, me)
	now := time.Now().Unix()

	// Build a large valid 150-device sockpuppet user record (1 original + 149 introduced = 448 ed25519 signatures).
	rootKey := bnc01NewOnion()
	sock := bnc01SyntheticUser("Sock", rootKey, now-1000)
	for i := 0; i < 149; i++ {
		childKey := bnc01NewOnion()
		child := introducedBy(sock.ID, rootKey.addr(), rootKey, childKey, now-900+int64(i))
		sock.Devices = append(sock.Devices, child)
	}
	sockBytes, err := msgpack.Marshal(sock)
	assert.NoError(t, err)

	// Alice invites the 150-device sockpuppet into the group so it sits on canonical history.
	inviteSock := &updateGroup{
		Nonce:     newNonce(),
		Actor:     alice.currentUserID(),
		Target:    groupID,
		Timestamp: now + 10,
		Type:      updateGroupTypeInviteUser,
		Data:      sockBytes,
	}
	inviteSock.ID = inviteSock.generateID()
	_, accepted := me.handleUpdateGroup(alice.network.Address(), bnc01BounceFrame(alice, inviteSock), false)
	assert.True(t, accepted)

	// Reset signature verification counter and send 20 tiny group renames from Alice.
	// Without caching/avoiding redundant recomputeDeviceMaps, each rename re-verifies all 448 signatures twice (>17,000 calls).
	net.verifyCalls.Store(0)
	for i := 0; i < 20; i++ {
		rename := &updateGroup{
			Nonce:     newNonce(),
			Actor:     alice.currentUserID(),
			Target:    groupID,
			Timestamp: now + int64(20+i),
			Type:      updateGroupTypeChangeName,
			Data:      []byte(fmt.Sprintf("Renamed-%d", i)),
		}
		rename.ID = rename.generateID()
		_, accepted = me.handleUpdateGroup(alice.network.Address(), bnc01BounceFrame(alice, rename), false)
		assert.True(t, accepted)
	}
	liveCalls := net.verifyCalls.Load()
	assert.True(t, liveCalls <= 40, "expected <= 40 VerifySignature calls across 20 renames, got %d", liveCalls)

	// A full cold reload (dropping the in-memory stack and inviteCache) must also avoid re-running ed25519
	// on already-verified device signatures.
	net.verifyCalls.Store(0)
	assert.NoError(t, me.reloadGroupConsensus(groupID))
	coldReloadCalls := net.verifyCalls.Load()
	assert.Equal(t, int64(0), coldReloadCalls, "cold reload should hit the verified device signature cache")

	// Dynamic checks still apply: adding a new device for Sock in DB is picked up on reload, and revoking a
	// device of Sock still causes updates signed after revocation to be rejected.
	newSockKey := bnc01NewOnion()
	newSockDev := introducedBy(sock.ID, rootKey.addr(), rootKey, newSockKey, now+50)
	_, accepted = me.handleDevice(alice.network.Address(), newSockDev.getPayload(), false)
	assert.True(t, accepted)

	sockAccept := &updateGroup{
		Nonce:     newNonce(),
		Actor:     sock.ID,
		Target:    groupID,
		Timestamp: now + 60,
		Type:      updateGroupTypeRespondToInvite,
		Data:      []byte{acceptInvite},
	}
	sockAccept.ID = sockAccept.generateID()
	_, accepted = me.handleUpdateGroup(newSockKey.addr(), bnc01OnionFrame(newSockKey, sockAccept), false)
	assert.True(t, accepted, "a newly added device for an invited user must still be recognized via live DB refresh")

	// Revoke newSockDev and ensure a post-revocation rename by it is rejected by consensus.
	assert.NoError(t, me.database.Model(&device{}).Where("id = ?", newSockDev.ID).Update("revoked_at", now+65).Error)
	postRevokeRename := &updateGroup{
		Nonce:     newNonce(),
		Actor:     sock.ID,
		Target:    groupID,
		Timestamp: now + 70,
		Type:      updateGroupTypeChangeName,
		Data:      []byte("AfterRevoke"),
	}
	postRevokeRename.ID = postRevokeRename.generateID()
	_, accepted = me.handleUpdateGroup(newSockKey.addr(), bnc01OnionFrame(newSockKey, postRevokeRename), false)
	assert.False(t, accepted, "revoking a device must still reject post-revocation updates")
}
