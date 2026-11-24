package chat

import (
	"bytes"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/alecthomas/assert/v2"
	"gorm.io/gorm"
)

func TestReferenceRequestLoadsOnlyOnePart(t *testing.T) {
	me, alice, _, _ := createUsersAndGroups(t)
	dms := messagesFrom(me, alice.currentUserID(), 12)
	references := []frameReference{}
	for _, dm := range dms {
		assert.NoError(t, me.database.Create(dm).Error)
		references = append(references, frameReference{FrameID: dm.ID, Type: typeDirectMessage})
	}
	smallBudget(t, 5*len(dms[0].getPayload()))

	var loaded atomic.Int64
	assert.NoError(t, me.database.Callback().Query().After("gorm:query").Register(
		"count_reference_request_direct_messages",
		func(db *gorm.DB) {
			if _, ok := db.Statement.Dest.(*directMessage); ok {
				loaded.Add(1)
			}
		},
	))

	cu := me.generateCatchUpFor(alice.network.Address(), referenceRequest{References: references})
	assert.Equal(t, 5, len(cu.Frames))
	assert.True(t, cu.Incomplete)
	assert.Equal(t, int64(6), loaded.Load(), "only the fitting frames and the first frame for the next part are loaded")
}

func TestEncryptedReferenceRequestLoadsOnlyOnePart(t *testing.T) {
	b := &Bounce{encrypted: true, platform: Platform{ConfigDirectory: t.TempDir()}}
	b.openEncryptedDatabase(randomTestBytes(masterKeySize))
	database, err := b.database.DB()
	assert.NoError(t, err)
	t.Cleanup(func() { database.Close() })

	peer := newTestnet().Address()
	peerKey := []byte("peer encryption key")
	peerUserKeyMutex.Lock()
	peerUserKeys[peer] = peerKey
	peerUserKeyMutex.Unlock()
	t.Cleanup(func() {
		peerUserKeyMutex.Lock()
		delete(peerUserKeys, peer)
		peerUserKeyMutex.Unlock()
	})
	assert.NoError(t, b.database.Create(&authorizedUser{ID: NewID(), PublicKey: []byte("another key")}).Error)

	const payloadSize = 64
	references := []frameReference{}
	for i := 0; i < 6; i++ {
		ef := encryptedFrame{
			ID:        NewID(),
			Type:      typeDirectMessage,
			Timestamp: int64(i + 1),
			Payload:   bytes.Repeat([]byte{byte(i)}, payloadSize),
			Recipients: []recipient{{
				PublicKey: peerKey,
			}},
		}
		assert.NoError(t, b.database.Create(&ef).Error)
		references = append(references, frameReference{FrameID: ef.ID, Type: typeDirectMessage})
	}
	smallBudget(t, 3*payloadSize)

	var loaded atomic.Int64
	assert.NoError(t, b.database.Callback().Query().After("gorm:query").Register(
		"count_reference_request_encrypted_frames",
		func(db *gorm.DB) {
			if _, ok := db.Statement.Dest.(*encryptedFrame); ok {
				loaded.Add(1)
			}
		},
	))

	ecu := b.generateEncryptedCatchUpFor(peer, referenceRequest{References: references})
	assert.Equal(t, 3, len(ecu.Frames))
	assert.True(t, ecu.Incomplete)
	assert.Equal(t, int64(4), loaded.Load(), "only the fitting frames and the first frame for the next part are loaded")
}

func TestReferenceRequestIsIgnoredWhileCatchUpIsInFlight(t *testing.T) {
	me, peer, _ := rawReferenceOfferPeer(t)
	address := peer.network.Address()
	rd := me.getRemoteDevice(address)
	assert.True(t, rd.beginCatchUp())
	t.Cleanup(rd.finishCatchUp)

	offer := me.getReferenceOfferFor(address)
	payload, err := msgpack.Marshal(&referenceRequest{References: offer.References})
	assert.NoError(t, err)
	queued := len(rd.messages)
	me.handleReferenceRequest(address, payload, false)
	assert.Equal(t, queued, len(rd.messages))
}

func TestBackedUpQueueDropsCatchUpAndReleasesSlot(t *testing.T) {
	rd := newRemoteDevice()
	rd.messages = make(chan sendable, 1)
	rd.messages <- &keepAlive{}
	rd.sockets = []*socket{{alive: true, lastRead: time.Now()}}
	assert.True(t, rd.beginCatchUp())

	cu := &catchUp{onComplete: rd.finishCatchUp}
	rd.send(cu)
	assert.Equal(t, 1, len(rd.messages))
	assert.True(t, rd.beginCatchUp(), "the dropped catch up did not release the peer's slot")
	rd.finishCatchUp()
}

func TestDisconnectReleasesQueuedCatchUp(t *testing.T) {
	rd := newRemoteDevice()
	assert.True(t, rd.beginCatchUp())
	rd.messages <- &catchUp{onComplete: rd.finishCatchUp}

	drain(rd.messages)
	assert.True(t, rd.beginCatchUp(), "the disconnected peer's queued catch up did not release its slot")
	rd.finishCatchUp()
}
