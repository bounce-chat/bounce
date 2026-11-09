package chat

import (
	log "github.com/sirupsen/logrus"
)

// An add user request rejected frame indicates that a recent attempt to add a user was rejected
// by the offer user for some reason.  The user interface should only allow one of these interactions
// at a time, but to avoid confusion about which request was rejected, the peer is passed directly
// to the UI.
type addUserRequestRejected struct{}

func (aurr *addUserRequestRejected) getType() uint16 {
	return typeAddUserRequestRejected
}

func (aurr *addUserRequestRejected) getPayload() []byte {
	return []byte{}
}

func (b *Bounce) handleAddUserRequestRejected(peer string, payload []byte, _ bool) (broadcastable, bool) {
	if !pendingAddUserRequests.isExpectedFrom(peer) {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("ignoring add user request rejected from a peer we did not recently send a request to")
		return nil, false
	}
	b.ui.AddUserRequestRejected(peer)
	return nil, false
}
