package chat

import (
	"fmt"
	"testing"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/alecthomas/assert/v2"
)

// The reference engine remembers what each connected peer has offered us.  Nothing about that memory is on a clock:
// a peer mid way through a sync of any size keeps its references until the frames arrive or it disconnects, a peer
// is held to one offer's worth of references at a time, a frame it served that we refused is never asked of it
// again, and a history too large for one offer comes in parts that the receiver asks for one at a time.

// smallOffers shrinks reference offers, and the per peer allowance with them, for the duration of a test.  Call it
// before creating engines so their shutdown cleanup runs before the global limit is restored.
func smallOffers(t *testing.T, references int) {
	t.Helper()
	old := maximumReferencesPerOffer
	maximumReferencesPerOffer = references
	t.Cleanup(func() { maximumReferencesPerOffer = old })
}

func referencesTo(dms []*directMessage) []frameReference {
	refs := []frameReference{}
	for _, dm := range dms {
		refs = append(refs, frameReference{FrameID: dm.ID, Type: typeDirectMessage})
	}
	return refs
}

func rowsFrom(b *Bounce, peer string) []frameReference {
	var rows []frameReference
	b.referenceDatabase.Where("peer = ?", peer).Order("created_at").Find(&rows)
	return rows
}

func TestReferencesLiveWhileThePeerIsConnectedAndGoWhenItLeaves(t *testing.T) {
	me, alice, _, _ := createUsersAndGroups(t)
	alice.loadReferenceOffer(me.network.Address(), referencesTo(messagesFrom(me, alice.currentUserID(), 3)))
	alice.setIncompleteOfferFrom(me.network.Address(), true)
	// However old the references are, a connected peer keeps them
	alice.referenceDatabase.Table("frame_references").Where("peer = ?", me.network.Address()).Updates(map[string]interface{}{"created_at": 1, "last_action": 1})

	alice.pruneReferences()
	assert.True(t, len(rowsFrom(alice, me.network.Address())) >= 3, "a connected peer's references were pruned")
	assert.True(t, alice.incompleteOfferFrom(me.network.Address()))

	sever(t, me, alice)
	alice.pruneReferences()
	assert.Equal(t, 0, len(rowsFrom(alice, me.network.Address())), "a departed peer's references remain")
	assert.False(t, alice.incompleteOfferFrom(me.network.Address()), "a departed peer's part offer is still awaited")
}

func TestAPeerIsHeldToItsReferenceAllowance(t *testing.T) {
	smallOffers(t, 3)
	oldTotal := maximumReferences
	maximumReferences = 4
	t.Cleanup(func() { maximumReferences = oldTotal })
	me, alice, _, _ := createUsersAndGroups(t)
	stranger := newTestnet().Address()
	dms := messagesFrom(me, alice.currentUserID(), 5)

	// Five offered, three kept; two more, still three
	alice.loadReferenceOffer(stranger, referencesTo(dms))
	assert.Equal(t, 3, len(rowsFrom(alice, stranger)))
	alice.loadReferenceOffer(stranger, referencesTo(messagesFrom(me, alice.currentUserID(), 2)))
	assert.Equal(t, 3, len(rowsFrom(alice, stranger)))

	// Offering what is already held is a refresh, not a new reference, even at the allowance
	alice.referenceDatabase.Table("frame_references").Where("peer = ?", stranger).Updates(map[string]interface{}{"state": referenceStateRequested, "last_action": 1})
	alice.loadReferenceOffer(stranger, referencesTo(dms[:3]))
	for _, row := range rowsFrom(alice, stranger) {
		assert.Equal(t, referenceStateRequested, row.State, "a request in flight was reset by a re-offer")
		assert.True(t, row.LastAction >= time.Now().Unix()-2, "a re-offer did not refresh the reference")
	}

	// The engine as a whole has a ceiling too, whoever is offering
	other := newTestnet().Address()
	alice.loadReferenceOffer(other, referencesTo(messagesFrom(me, alice.currentUserID(), 3)))
	assert.Equal(t, 1, len(rowsFrom(alice, other)), "the global ceiling was not applied")
}

func TestARefusedFrameIsNotAskedOfThatPeerAgain(t *testing.T) {
	me, alice, _, _ := createUsersAndGroups(t)
	dms := messagesFrom(me, alice.currentUserID(), 3)
	alice.loadReferenceOffer(me.network.Address(), referencesTo(dms))
	alice.referenceDatabase.Table("frame_references").Where("peer = ?", me.network.Address()).Updates(map[string]interface{}{"state": referenceStateRequested, "last_action": 1})

	// The first frame arrives damaged and is refused; the other two are fine
	damaged := append([]byte{}, dms[0].getPayload()...)
	damaged[len(damaged)-1] ^= 0xff
	stage := catchUp{Incomplete: true}
	stage.Frames = append(stage.Frames, frame{ID: dms[0].ID, Type: typeDirectMessage, Payload: damaged})
	stage.Frames = append(stage.Frames, frame{ID: dms[1].ID, Type: typeDirectMessage, Payload: dms[1].getPayload()})
	payload, _ := msgpack.Marshal(stage)
	alice.handleCatchUp(me.network.Address(), payload, false)

	states := map[ID]int{}
	for _, row := range rowsFrom(alice, me.network.Address()) {
		states[row.FrameID] = row.State
	}
	assert.Equal(t, referenceStateRefused, states[dms[0].ID], "a refused frame should be remembered as refused")
	_, held := states[dms[1].ID]
	assert.False(t, held, "a delivered frame's reference is gone")
	assert.Equal(t, referenceStateRequested, states[dms[2].ID], "the rest was requested again for the next stage")
	assert.Equal(t, int64(1), alice.outstandingReferencesFrom(me.network.Address()), "a refused frame is not outstanding")

	// Offered again, it stays refused rather than being requested once more
	alice.loadReferenceOffer(me.network.Address(), referencesTo(dms[:1]))
	for _, row := range rowsFrom(alice, me.network.Address()) {
		if row.FrameID == dms[0].ID {
			assert.Equal(t, referenceStateRefused, row.State)
		}
	}
}

func TestALargeHistoryIsOfferedInPartsAndArrivesWhole(t *testing.T) {
	quickReferenceRetries(t)
	smallOffers(t, 4)
	me, alice, _, _ := createUsersAndGroups(t)

	// Alice goes offline and I write her ten messages, more than one offer names
	sever(t, me, alice)
	for i := 0; i < 10; i++ {
		me.SendDirectMessage(alice.currentUserID(), fmt.Sprintf("part of a long story %02d", i), nil, NilID)
	}
	offer := me.getReferenceOfferFor(alice.network.Address())
	assert.Equal(t, 4, len(offer.References))
	assert.True(t, offer.Incomplete, "an offer that left frames out should say so")

	// She comes back; each part is drained and the next asked for, until everything has arrived and the last part,
	// which fits in one offer, has been seen by both sides.  (The fixture leaves a few frames of mine that Alice
	// never takes, so her copy of the story, not an empty offer, is the measure.)
	redial(t, alice, me)
	// Each part costs a retry tick or two, and the suite can be busy, so this is generous rather than tight
	deadline := time.Now().Add(40 * time.Second)
	var n int64
	settled := false
	for time.Now().Before(deadline) {
		alice.database.Model(&directMessage{}).Where("text LIKE ?", "part of a long story%").Count(&n)
		settled = n == 10 && !me.getReferenceOfferFor(alice.network.Address()).Incomplete && !alice.incompleteOfferFrom(me.network.Address())
		if settled {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	assert.Equal(t, int64(10), n, "not every part of the history arrived")
	assert.True(t, settled, "the final offer part was never recognised as final by both sides")
}

func TestInitialSyncWaitsForTheFinalOfferPart(t *testing.T) {
	_, alice, _, _ := createUsersAndGroups(t)
	source := newTestnet().Address()
	setWaitingForInitialSyncFrom(source)
	t.Cleanup(func() {
		setWaitingForInitialSyncFrom("")
		initialSyncAnnounced.Store(false)
		alice.setIncompleteOfferFrom(source, false)
	})

	// The source's offer was one part of more; a complete catch up drains it, so the next part is asked for
	alice.setIncompleteOfferFrom(source, true)
	complete, _ := msgpack.Marshal(catchUp{})
	alice.handleCatchUp(source, complete, false)
	assert.Equal(t, source, waitingForInitialSyncFrom(), "still syncing while offer parts remain")
	assert.False(t, alice.incompleteOfferFrom(source), "the next part should have been asked for")

	// The last part's catch up ends the sync
	alice.handleCatchUp(source, complete, false)
	assert.Equal(t, "", waitingForInitialSyncFrom())
}

func TestForgedReferenceOfferAcksAreIgnored(t *testing.T) {
	smallOffers(t, 4)
	me, alice, _, _ := createUsersAndGroups(t)
	stranger := newTestnet().Address()

	// A stranger flooding typeReferenceOffer acks must not grow referenceDatabase or pendingOfferAcks
	forged := ack{References: make([]frameReference, 500)}
	for i := range forged.References {
		forged.References[i] = frameReference{FrameID: NewID(), Type: typeReferenceOffer}
	}
	payload, err := msgpack.Marshal(&forged)
	assert.NoError(t, err)
	me.handleAck(stranger, payload, false)

	// Reference database should no longer store delivery_records at all (and if a table existed, zero rows)
	var drCount int64
	if me.referenceDatabase.Migrator().HasTable(&deliveryRecord{}) {
		assert.NoError(t, me.referenceDatabase.Model(&deliveryRecord{}).Count(&drCount).Error)
	}
	assert.Equal(t, int64(0), drCount, "forged reference offer acks must not populate referenceDatabase")

	me.referenceState.pendingOfferAcksMutex.Lock()
	strangerEntries := 0
	for key := range me.referenceState.pendingOfferAcks {
		if key.peer == stranger {
			strangerEntries++
		}
	}
	me.referenceState.pendingOfferAcksMutex.Unlock()
	assert.Equal(t, 0, strangerEntries, "forged reference offer acks must not add pending entries")

	// Only the exact (peer, offerID) in flight is marked acked
	inFlightID := NewID()
	me.registerPendingReferenceOffer(alice.network.Address(), inFlightID)
	t.Cleanup(func() { me.clearPendingReferenceOffer(alice.network.Address(), inFlightID) })

	// Wrong peer naming the right offer ID must not ack it
	wrongPeerAck, err := msgpack.Marshal(&ack{References: []frameReference{{FrameID: inFlightID, Type: typeReferenceOffer}}})
	assert.NoError(t, err)
	me.handleAck(stranger, wrongPeerAck, false)
	assert.False(t, me.consumePendingReferenceOfferAck(alice.network.Address(), inFlightID))

	// Right peer naming a wrong offer ID must not ack it
	wrongIDAck, err := msgpack.Marshal(&ack{References: []frameReference{{FrameID: NewID(), Type: typeReferenceOffer}}})
	assert.NoError(t, err)
	me.handleAck(alice.network.Address(), wrongIDAck, false)
	assert.False(t, me.consumePendingReferenceOfferAck(alice.network.Address(), inFlightID))

	// Oversized acks are truncated to maximumReferencesPerOffer (4)
	oversized := ack{References: make([]frameReference, 10)}
	for i := 0; i < 9; i++ {
		oversized.References[i] = frameReference{FrameID: NewID(), Type: typeReferenceOffer}
	}
	oversized.References[9] = frameReference{FrameID: inFlightID, Type: typeReferenceOffer}
	oversizedPayload, err := msgpack.Marshal(&oversized)
	assert.NoError(t, err)
	me.handleAck(alice.network.Address(), oversizedPayload, false)
	assert.False(t, me.consumePendingReferenceOfferAck(alice.network.Address(), inFlightID), "references beyond maximumReferencesPerOffer must be truncated")

	// Right peer naming the exact in-flight offer ID completes it
	me.handleAck(alice.network.Address(), wrongPeerAck, false)
	assert.True(t, me.consumePendingReferenceOfferAck(alice.network.Address(), inFlightID))
}

func TestUnknownPeersCannotStarveKnownPeersInReferenceEngine(t *testing.T) {
	smallOffers(t, 10)
	oldTotal := maximumReferences
	oldUnknownPerPeer := maximumUnknownReferencesPerPeer
	oldUnknownTotal := maximumUnknownReferences
	maximumReferences = 40
	maximumUnknownReferencesPerPeer = 4
	maximumUnknownReferences = 10
	t.Cleanup(func() {
		maximumReferences = oldTotal
		maximumUnknownReferencesPerPeer = oldUnknownPerPeer
		maximumUnknownReferences = oldUnknownTotal
	})

	me, alice, _, _ := createUsersAndGroups(t)

	// Five unknown identities each offer 10 fake references: each gets at most 4, and all five
	// combined get at most 10 (leaving 30 slots reserved for known peers).
	strangers := make([]string, 5)
	for i := range strangers {
		strangers[i] = newTestnet().Address()
		refs := make([]frameReference, 10)
		for j := range refs {
			refs[j] = frameReference{FrameID: NewID(), Type: typeDirectMessage}
		}
		alice.loadReferenceOffer(strangers[i], refs)
		assert.True(t, len(rowsFrom(alice, strangers[i])) <= 4, "unknown peer exceeded per-peer cap")
	}

	var unknownTotal int64
	assert.NoError(t, alice.referenceDatabase.Model(&frameReference{}).Where("known_peer = ?", false).Count(&unknownTotal).Error)
	assert.Equal(t, int64(10), unknownTotal, "unknown peers must be capped at maximumUnknownReferences")

	// Known contact Me still gets all of its offered references stored without starvation
	dms := messagesFrom(me, alice.currentUserID(), 5)
	dropped := alice.loadReferenceOffer(me.network.Address(), referencesTo(dms))
	assert.Equal(t, 0, dropped, "known contact must not be starved by unknown peers")
	assert.True(t, len(rowsFrom(alice, me.network.Address())) >= 5)

	// Unserved rows from an unknown peer expire after maximumUnknownReferenceAttempts timeouts
	// even if the unknown peer keeps its socket open.
	stranger0 := strangers[0]
	assert.True(t, len(rowsFrom(alice, stranger0)) > 0)
	assert.NoError(t, alice.referenceDatabase.Table("frame_references").
		Where("peer = ?", stranger0).
		Updates(map[string]interface{}{
			"state":       referenceStateRequested,
			"attempts":    maximumUnknownReferenceAttempts,
			"last_action": 1,
		}).Error)
	alice.expireUnknownReferences()
	assert.Equal(t, 0, len(rowsFrom(alice, stranger0)), "unserved unknown-peer rows must expire after repeated attempts")

	// Unknown-peer rows older than unknownReferenceLifetime also expire even if attempts stayed 0
	stranger1 := strangers[1]
	assert.True(t, len(rowsFrom(alice, stranger1)) > 0)
	assert.NoError(t, alice.referenceDatabase.Table("frame_references").
		Where("peer = ?", stranger1).
		Update("created_at", 1).Error)
	alice.expireUnknownReferences()
	assert.Equal(t, 0, len(rowsFrom(alice, stranger1)), "old unknown-peer rows must expire past unknownReferenceLifetime")

	// Fill Me's per-peer allowance (10) on Alice, then send another offer: since dropped > 0 for a
	// known peer, Alice must NOT ack overflowOffer.ID so Me's retry loop re-offers once room frees up.
	alice.loadReferenceOffer(me.network.Address(), referencesTo(messagesFrom(me, alice.currentUserID(), 10)))
	overflowOffer := &referenceOffer{
		ID:         NewID(),
		References: referencesTo(messagesFrom(me, alice.currentUserID(), 2)),
	}
	payload, err := msgpack.Marshal(overflowOffer)
	assert.NoError(t, err)
	alice.handleReferenceOffer(me.network.Address(), payload, false)
	time.Sleep(50 * time.Millisecond)

	flow := alice.network.Address() + me.network.Address()
	networkTracking.Lock()
	offerAcked := false
	for _, ref := range acked[flow] {
		if ref.Type == typeReferenceOffer && ref.FrameID == overflowOffer.ID {
			offerAcked = true
			break
		}
	}
	networkTracking.Unlock()
	assert.False(t, offerAcked, "known peer's partially dropped reference offer must not be acked")
}

func TestUnknownDeviceWithLargeHistoryDeliversAllFramesAfterIdentityLearned(t *testing.T) {
	quickReferenceRetries(t)
	oldUnknownPerPeer := maximumUnknownReferencesPerPeer
	oldUnknownTotal := maximumUnknownReferences
	maximumUnknownReferencesPerPeer = 2
	maximumUnknownReferences = 10
	t.Cleanup(func() {
		maximumUnknownReferencesPerPeer = oldUnknownPerPeer
		maximumUnknownReferences = oldUnknownTotal
	})

	me := newBounceUser("Me")
	t.Cleanup(me.Shutdown)
	alice := newBounceUser("Alice")
	t.Cleanup(alice.Shutdown)

	// Pair sync device me2 onto Me, then sever Me <-> me2 so me2 does not learn about Alice from Me
	me2 := newBounceWithUI(newTestUI())
	t.Cleanup(me2.Shutdown)
	assert.NoError(t, me2.RequestToSync(me.GetNewSyncString()))
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, found := me.getDeviceFromAddress(me2.network.Address()); found && waitingForInitialSyncFrom() == "" {
			if _, ok := me2.currentUser(); ok {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, meKnowsMe2 := me.getDeviceFromAddress(me2.network.Address())
	assert.True(t, meKnowsMe2, "Me must know sync device me2 before adding Alice")
	// Take me2 offline before the addUser exchange so Alice learns me2's address from Me's record
	// without contacting me2 yet, leaving me2 with no device row for Alice.
	sever(t, me, me2)
	me2Addr := me2.network.Address()
	hostsMutex.Lock()
	me2Host := hosts[me2Addr]
	delete(hosts, me2Addr)
	hostsMutex.Unlock()
	t.Cleanup(func() {
		hostsMutex.Lock()
		hosts[me2Addr] = me2Host
		hostsMutex.Unlock()
	})

	assert.NoError(t, alice.RequestToAddUser(me.GetNewAddUserString()))
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, found := alice.getDeviceFromAddress(me2Addr); found {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, aliceKnowsMe2 := alice.getDeviceFromAddress(me2Addr)
	assert.True(t, aliceKnowsMe2, "Alice must learn me2's address from the addUser exchange")

	_, me2KnowsAliceBefore := me2.getDeviceFromAddress(alice.network.Address())
	assert.False(t, me2KnowsAliceBefore, "me2 should not know Alice yet")

	for i := 0; i < 6; i++ {
		assert.NoError(t, alice.SendDirectMessage(me.currentUserID(), fmt.Sprintf("handoff dm %02d", i), nil, NilID))
	}

	// Bring me2 back online (still severed from Me) and connect Alice -> me2: Alice's first referenceOffer
	// carries addUser + 6 DMs (exceeding maximumUnknownReferencesPerPeer=2).  me2 takes the first 2
	// (including addUser), learns Alice's identity in handleCatchUp, requests a fresh referenceOffer,
	// and receives all 6 DMs on the same connection.
	hostsMutex.Lock()
	hosts[me2Addr] = me2Host
	hostsMutex.Unlock()
	redial(t, alice, me2)
	deadline = time.Now().Add(15 * time.Second)
	var received int64
	for time.Now().Before(deadline) {
		me2.database.Model(&directMessage{}).Where("text LIKE ?", "handoff dm%").Count(&received)
		if received == 6 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	assert.Equal(t, int64(6), received, "all 6 DMs must arrive after me2 learns Alice's identity from addUser and requests a fresh offer")
	_, me2KnowsAliceAfter := me2.getDeviceFromAddress(alice.network.Address())
	assert.True(t, me2KnowsAliceAfter)
}
