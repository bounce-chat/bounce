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
