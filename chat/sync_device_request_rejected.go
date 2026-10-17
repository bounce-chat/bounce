package chat

import (
	log "github.com/sirupsen/logrus"
)

// A sync device request rejected frame indicates that a recent attempt to become a sync device was rejected
// by the offer user for some reason.  The user interface should only allow one of these interactions at a
// time, but to avoid confusion about which request was rejected, the peer is passed directly to the UI.
type syncDeviceRequestRejected struct{}

func (sdrr *syncDeviceRequestRejected) getType() uint16 {
	return typeSyncDeviceRequestRejected
}

func (sdrr *syncDeviceRequestRejected) getPayload() []byte {
	return []byte{}
}

func (b *Bounce) handleSyncDeviceRequestRejected(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	if !syncRequestedFrom(peer) {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("ignoring sync device request rejected from peer we did not make a sync request to")
		return nil, false
	}

	if waitingForInitialSyncFrom() == peer {
		setWaitingForInitialSyncFrom("")
	}
	b.ui.SyncDeviceRequestRejected(peer)
	return nil, false
}
