package chat

import (
	"io"
	"net"
	"runtime"
	"testing"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/alecthomas/assert/v2"
)

func rawReferenceOfferPeer(t *testing.T) (*Bounce, *Bounce, net.Conn) {
	t.Helper()
	me := newBounceUser("Me")
	t.Cleanup(me.Shutdown)
	peer := newBounceUser("Peer")
	t.Cleanup(peer.Shutdown)

	profile, ok := peer.currentUser()
	assert.True(t, ok)
	raw, err := msgpack.Marshal(profile)
	assert.NoError(t, err)
	var contact user
	assert.NoError(t, msgpack.Unmarshal(raw, &contact))
	assert.NoError(t, me.database.Create(&contact).Error)

	conn, err := peer.network.Dial(me.network.Address())
	assert.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		connected := me.getRemoteDevice(peer.network.Address()).connectedSockets() > 0
		if connected && len(me.getReferenceOfferFor(peer.network.Address()).References) > 0 {
			return me, peer, conn
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("reference offer peer was not connected with references to offer")
	return nil, nil, nil
}

func readReferenceOfferFrom(t *testing.T, conn net.Conn) referenceOffer {
	t.Helper()
	assert.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	for {
		frameType, payload, err := readFrame(conn, true)
		assert.NoError(t, err)
		if frameType != typeReferenceOffer {
			continue
		}
		var offer referenceOffer
		assert.NoError(t, msgpack.Unmarshal(payload, &offer))
		return offer
	}
}

func TestReferenceOfferRequestsDoNotPileUp(t *testing.T) {
	me, peer, conn := rawReferenceOfferPeer(t)
	go io.Copy(io.Discard, conn)
	time.Sleep(300 * time.Millisecond)

	before := runtime.NumGoroutine()
	const requests = 2000
	for range requests {
		assert.NoError(t, writeFrame(conn, typeRequestReferenceOffer, []byte{}))
	}
	time.Sleep(2 * time.Second)
	after := runtime.NumGoroutine()

	assert.True(t, after-before < requests/10,
		"reference offer requests left %d extra goroutines after %d requests", after-before, requests)
	assert.True(t, me.getRemoteDevice(peer.network.Address()).connectedSockets() > 0)
}

func TestReferenceOfferRequestDuringSendRunsAfterIt(t *testing.T) {
	quickReferenceRetries(t)
	me, peer, conn := rawReferenceOfferPeer(t)
	first := readReferenceOfferFrom(t, conn)

	returned := make(chan struct{})
	go func() {
		me.sendReferences(peer.network.Address())
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("a reference offer request waited behind the active send")
	}

	acknowledgement, err := msgpack.Marshal(&ack{References: []frameReference{{
		FrameID: first.ID,
		Type:    typeReferenceOffer,
	}}})
	assert.NoError(t, err)
	me.handleAck(peer.network.Address(), acknowledgement, false)

	second := readReferenceOfferFrom(t, conn)
	assert.NotEqual(t, first.ID, second.ID, "the request made during the active send must not be lost")
}

func TestIncompleteOffersAreTrackedPerEngine(t *testing.T) {
	me := &Bounce{referenceState: newReferenceState()}
	other := &Bounce{referenceState: newReferenceState()}
	peer := newTestnet().Address()

	me.setIncompleteOfferFrom(peer, true)
	other.setIncompleteOfferFrom(peer, false)

	assert.True(t, me.incompleteOfferFrom(peer))
	assert.False(t, other.incompleteOfferFrom(peer))
}

func TestAlreadyHeldIncompleteOfferRequestsNextPart(t *testing.T) {
	me, peer, conn := rawReferenceOfferPeer(t)
	local, exists := me.getDeviceFromAddress(me.network.Address())
	assert.True(t, exists)
	payload, err := msgpack.Marshal(&referenceOffer{
		ID:         NewID(),
		References: []frameReference{{FrameID: local.ID, Type: typeDevice}},
		Incomplete: true,
	})
	assert.NoError(t, err)
	assert.NoError(t, writeFrame(conn, typeReferenceOffer, payload))

	assert.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	for {
		frameType, _, err := readFrame(conn, true)
		assert.NoError(t, err)
		if frameType == typeRequestReferenceOffer {
			assert.False(t, me.incompleteOfferFrom(peer.network.Address()))
			return
		}
	}
}

func offerContainsFrame(offer *referenceOffer, frameType uint16, id ID) bool {
	for _, ref := range offer.References {
		if ref.Type == frameType && ref.FrameID == id {
			return true
		}
	}
	return false
}

func TestDepartedMemberCannotReopenUpdateGroupOfferWindow(t *testing.T) {
	me, alice, bob, groupID := createUsersAndGroups(t)
	bobID := bob.currentUserID()
	bobAddr := bob.network.Address()
	aliceID := alice.currentUserID()
	aliceAddr := alice.network.Address()

	// Add a non-member contact (Dave) who knows Me, so we can also test the never-a-member variant.
	dave := newBounceUser("Dave")
	t.Cleanup(dave.Shutdown)
	assert.NoError(t, me.RequestToAddUser(dave.GetNewAddUserString()))
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, found := me.getDeviceFromAddress(dave.network.Address()); found {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Disconnect everyone so delivery records are not written in the background while checking offers.
	hostsMutex.Lock()
	delete(hosts, me.network.Address())
	hostsMutex.Unlock()
	sever(t, me, alice)
	sever(t, me, bob)
	sever(t, me, dave)
	me.networkOffline()

	// Remove Bob, then rename the group at a strictly later timestamp so the control holds before any forgery.
	assert.NoError(t, me.RemoveUserFromGroup(groupID, bobID))
	var bobRemoval updateGroup
	assert.NoError(t, me.database.Where("target = ? AND type = ? AND data = ?", groupID, updateGroupTypeRemoveUser, bobID[:]).First(&bobRemoval).Error)
	assert.NoError(t, me.applyAndBroadcastUpdateGroup(&updateGroup{
		Actor:     me.currentUserID(),
		Target:    groupID,
		Timestamp: bobRemoval.Timestamp + 1,
		Type:      updateGroupTypeChangeName,
		Data:      []byte("After Bob Left"),
	}))
	var renameAfterBob updateGroup
	assert.NoError(t, me.database.Where("target = ? AND type = ? AND data = ?", groupID, updateGroupTypeChangeName, []byte("After Bob Left")).First(&renameAfterBob).Error)

	t.Run("ForgedFutureDepartureByRemovedMember", func(t *testing.T) {
		bobOffer := me.getReferenceOfferFor(bobAddr)
		assert.True(t, offerContainsFrame(bobOffer, typeUpdateGroup, bobRemoval.ID), "departed member must still be offered their removal")
		assert.False(t, offerContainsFrame(bobOffer, typeUpdateGroup, renameAfterBob.ID), "control: post-removal rename is not offered before forgery")

		forgedReject := bnc01BuildUG(bob, bobID, groupID, int64(1)<<60, updateGroupTypeRespondToInvite, []byte{rejectInvite})
		me.handleUpdateGroup(bobAddr, forgedReject.getPayload(), false)

		bobOfferAfterForgery := me.getReferenceOfferFor(bobAddr)
		assert.False(t, offerContainsFrame(bobOfferAfterForgery, typeUpdateGroup, renameAfterBob.ID),
			"forged future-dated departure must not reopen update-group offers for a removed member")
	})

	t.Run("FutureDatedSelfRemoval", func(t *testing.T) {
		futureLeave := bnc01BuildUG(alice, aliceID, groupID, int64(1)<<60, updateGroupTypeRemoveUser, aliceID[:])
		me.handleUpdateGroup(aliceAddr, futureLeave.getPayload(), false)
		assert.False(t, me.userIsInGroup(groupID, aliceID))

		assert.NoError(t, me.RenameGroup(groupID, "After Alice Future Left"))
		var renameAfterAlice updateGroup
		assert.NoError(t, me.database.Where("target = ? AND type = ? AND data = ?", groupID, updateGroupTypeChangeName, []byte("After Alice Future Left")).First(&renameAfterAlice).Error)

		aliceOffer := me.getReferenceOfferFor(aliceAddr)
		assert.False(t, offerContainsFrame(aliceOffer, typeUpdateGroup, renameAfterAlice.ID),
			"future-dated real departure must not expose updates saved after the departure")
	})

	t.Run("ForgedDepartureByNeverMember", func(t *testing.T) {
		daveReject := bnc01BuildUG(dave, dave.currentUserID(), groupID, int64(1)<<60, updateGroupTypeRespondToInvite, []byte{rejectInvite})
		me.handleUpdateGroup(dave.network.Address(), daveReject.getPayload(), false)

		daveOffer := me.getReferenceOfferFor(dave.network.Address())
		assert.False(t, offerContainsFrame(daveOffer, typeUpdateGroup, renameAfterBob.ID),
			"a contact who was never a member must not be offered group updates via a forged departure")
	})
}
