package chat

import (
	"fmt"
	"testing"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/alecthomas/assert/v2"
	"github.com/google/uuid"
)

// A catch up is bounded in size.  When a peer asks for more than fits, it gets the earliest frames and a flag saying
// there is more; once it has handled those it immediately asks for the rest.  Stages therefore arrive in order
// however the wire reorders them, because each is requested only after the previous one was processed, and the
// answering device remembers nothing between stages.

// smallBudget shrinks catch ups for the duration of a test so that a handful of messages need several stages
func smallBudget(t *testing.T, bytes int) {
	t.Helper()
	old := catchUpPayloadBudget
	catchUpPayloadBudget = bytes
	t.Cleanup(func() { catchUpPayloadBudget = old })
}

// messagesFrom builds n signed direct messages from author to counterparty, saved one second apart
func messagesFrom(author *Bounce, counterparty uuid.UUID, n int) []*directMessage {
	dms := []*directMessage{}
	base := time.Now().Unix() - int64(n)
	for i := 0; i < n; i++ {
		dm := &directMessage{ID: uuid.New(), Author: author.currentUserID(), Xor: xor(author.currentUserID(), counterparty), Text: fmt.Sprintf("message %02d", i), WrittenAt: base + int64(i), SavedAt: base + int64(i)}
		dm.OriginalPayload, _ = msgpack.Marshal(dm)
		sc := author.createSignedContainer(typeDirectMessage, dm.OriginalPayload)
		dm.Signature, dm.Signer = sc.Signature, sc.Signer
		dms = append(dms, dm)
	}
	return dms
}

func TestACatchUpStopsAtItsBudgetAndSaysSo(t *testing.T) {
	me, alice, _, _ := createUsersAndGroups(t)
	dms := messagesFrom(me, alice.currentUserID(), 12)
	oneFrame := len(dms[0].getPayload())
	smallBudget(t, 5*oneFrame)

	// Stage one: the earliest five, and a flag
	cu := &catchUp{}
	for _, dm := range dms {
		cu.sendables = append(cu.sendables, dm)
	}
	var stage catchUp
	assert.NoError(t, msgpack.Unmarshal(cu.getPayload(), &stage))
	assert.True(t, stage.Incomplete)
	assert.Equal(t, 5, len(stage.Frames))
	for i, fr := range stage.Frames {
		assert.Equal(t, dms[i].ID, fr.ID, "frames come in the order they were saved")
	}

	// Later stages: what was left, until it all fits
	remaining := &catchUp{}
	for _, dm := range dms[5:] {
		remaining.sendables = append(remaining.sendables, dm)
	}
	assert.NoError(t, msgpack.Unmarshal(remaining.getPayload(), &stage))
	assert.True(t, stage.Incomplete)
	assert.Equal(t, dms[5].ID, stage.Frames[0].ID)
	last := &catchUp{}
	for _, dm := range dms[10:] {
		last.sendables = append(last.sendables, dm)
	}
	assert.NoError(t, msgpack.Unmarshal(last.getPayload(), &stage))
	assert.False(t, stage.Incomplete)
	assert.Equal(t, 2, len(stage.Frames))

	// A single frame larger than the whole budget still goes, alone, rather than never
	smallBudget(t, oneFrame/2)
	alone := &catchUp{sendables: sortableCatchUpAbles{dms[0], dms[1]}}
	assert.NoError(t, msgpack.Unmarshal(alone.getPayload(), &stage))
	assert.Equal(t, 1, len(stage.Frames))
	assert.True(t, stage.Incomplete)
}

func TestAnIncompleteCatchUpIsFollowedAtOnceByARequestForTheRest(t *testing.T) {
	me, alice, _, _ := createUsersAndGroups(t)
	dms := messagesFrom(me, alice.currentUserID(), 6)

	// Alice has been offered all six by me and has requested them
	offered := []frameReference{}
	for _, dm := range dms {
		offered = append(offered, frameReference{FrameID: dm.ID, Type: typeDirectMessage})
	}
	alice.loadReferenceOffer(me.network.Address(), offered)
	alice.referenceDatabase.Table("frame_references").Where("peer = ?", me.network.Address()).Updates(map[string]interface{}{"state": referenceStateRequested, "last_action": 1})

	// The first stage carries three and says more is coming
	stage := catchUp{Incomplete: true}
	for _, dm := range dms[:3] {
		stage.Frames = append(stage.Frames, frame{ID: dm.ID, Type: typeDirectMessage, Payload: dm.getPayload()})
	}
	payload, _ := msgpack.Marshal(stage)
	alice.handleCatchUp(me.network.Address(), payload, false)

	// Alice holds the three, has forgotten their references, and has just re-requested the other three from me
	for _, dm := range dms[:3] {
		var n int64
		alice.database.Model(&directMessage{}).Where("id = ?", dm.ID).Count(&n)
		assert.Equal(t, int64(1), n)
	}
	// (The live fixture may have offered Alice other frames of mine meanwhile; those are swept into the same request,
	// so the check is on the six we know about rather than on the total.)
	var left []frameReference
	alice.referenceDatabase.Where("peer = ?", me.network.Address()).Find(&left)
	remaining := map[uuid.UUID]frameReference{}
	for _, fr := range left {
		remaining[fr.FrameID] = fr
	}
	for _, dm := range dms[:3] {
		_, still := remaining[dm.ID]
		assert.False(t, still, "a delivered frame's reference is gone")
	}
	for _, dm := range dms[3:] {
		fr, still := remaining[dm.ID]
		assert.True(t, still)
		assert.Equal(t, referenceStateRequested, fr.State)
		assert.True(t, fr.LastAction >= time.Now().Unix()-2, "requested again just now, not left for the retry timer")
	}
}

func TestInitialSyncCompletesOnlyWithTheFinalStage(t *testing.T) {
	_, alice, _, _ := createUsersAndGroups(t)
	// A source that is not a live peer, so no real catch up from it can arrive mid-test and end the sync for us
	source := newTestnet().Address()
	setWaitingForInitialSyncFrom(source)
	t.Cleanup(func() { setWaitingForInitialSyncFrom(""); initialSyncAnnounced.Store(false) })

	incomplete, _ := msgpack.Marshal(catchUp{Incomplete: true})
	alice.handleCatchUp(source, incomplete, false)
	assert.Equal(t, source, waitingForInitialSyncFrom(), "still syncing while stages remain")

	complete, _ := msgpack.Marshal(catchUp{})
	alice.handleCatchUp(source, complete, false)
	assert.Equal(t, "", waitingForInitialSyncFrom())
}

// sever closes every socket between two peers and waits until both sides agree
func sever(t *testing.T, a, b *Bounce) {
	t.Helper()
	a.getRemoteDevice(b.network.Address()).shutdown()
	b.getRemoteDevice(a.network.Address()).shutdown()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if a.getRemoteDevice(b.network.Address()).connectedSockets() == 0 && b.getRemoteDevice(a.network.Address()).connectedSockets() == 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("peers never disconnected")
}

// redial has a dial b again, ignoring the dial cooldown, and waits for the socket
func redial(t *testing.T, a, b *Bounce) {
	t.Helper()
	a.devicePool.lastDialMutex.Lock()
	delete(a.devicePool.lastDial, b.network.Address())
	delete(a.devicePool.lastFailedDial, b.network.Address())
	a.devicePool.lastDialMutex.Unlock()
	go a.tryDialing(b.network.Address())
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if a.getRemoteDevice(b.network.Address()).connectedSockets() > 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("peers never reconnected")
}

func TestABacklogSyncsInStagesOverTheNetwork(t *testing.T) {
	quickReferenceRetries(t)
	me, alice, _, _ := createUsersAndGroups(t)
	oneFrame := len(messagesFrom(me, alice.currentUserID(), 1)[0].getPayload())
	smallBudget(t, 4*oneFrame)

	// Alice goes offline and I write her twenty messages
	sever(t, me, alice)
	for i := 0; i < 20; i++ {
		me.SendDirectMessage(DirectMessage{Thread: alice.currentUserID(), Text: fmt.Sprintf("while you were out %02d", i), WrittenAt: time.Now().Unix()}, nil, nil)
	}

	// She comes back; the reference flow delivers all twenty in several catch ups, each requested only after the
	// previous one was handled
	redial(t, alice, me)
	deadline := time.Now().Add(15 * time.Second)
	var n int64
	for time.Now().Before(deadline) {
		alice.database.Model(&directMessage{}).Where("text LIKE ?", "while you were out%").Count(&n)
		if n == 20 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	assert.Equal(t, int64(20), n)
}
