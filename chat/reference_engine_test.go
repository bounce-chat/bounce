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

// smallOffers shrinks reference offers, and the per peer allowance with them, for the duration of a test
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
	setIncompleteOfferFrom(me.network.Address(), true)
	// However old the references are, a connected peer keeps them
	alice.referenceDatabase.Table("frame_references").Where("peer = ?", me.network.Address()).Updates(map[string]interface{}{"created_at": 1, "last_action": 1})

	alice.pruneReferences()
	assert.True(t, len(rowsFrom(alice, me.network.Address())) >= 3, "a connected peer's references were pruned")
	assert.True(t, incompleteOfferFrom(me.network.Address()))

	sever(t, me, alice)
	alice.pruneReferences()
	assert.Equal(t, 0, len(rowsFrom(alice, me.network.Address())), "a departed peer's references remain")
	assert.False(t, incompleteOfferFrom(me.network.Address()), "a departed peer's part offer is still awaited")
}

func TestAPeerIsHeldToItsReferenceAllowance(t *testing.T) {
	me, alice, _, _ := createUsersAndGroups(t)
	smallOffers(t, 3)
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
	oldTotal := maximumReferences
	maximumReferences = 4
	t.Cleanup(func() { maximumReferences = oldTotal })
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
	me, alice, _, _ := createUsersAndGroups(t)
	smallOffers(t, 4)

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
		settled = n == 10 && !me.getReferenceOfferFor(alice.network.Address()).Incomplete && !incompleteOfferFrom(me.network.Address())
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
		setIncompleteOfferFrom(source, false)
	})

	// The source's offer was one part of more; a complete catch up drains it, so the next part is asked for
	setIncompleteOfferFrom(source, true)
	complete, _ := msgpack.Marshal(catchUp{})
	alice.handleCatchUp(source, complete, false)
	assert.Equal(t, source, waitingForInitialSyncFrom(), "still syncing while offer parts remain")
	assert.False(t, incompleteOfferFrom(source), "the next part should have been asked for")

	// The last part's catch up ends the sync
	alice.handleCatchUp(source, complete, false)
	assert.Equal(t, "", waitingForInitialSyncFrom())
}
