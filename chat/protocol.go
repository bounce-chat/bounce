package chat

import (
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

var SYNC_SCOPE = 0 // TODO: unexport these
var USER_SCOPE = 1
var GROUP_SCOPE = 2
var GLOBAL_SCOPE = 3 // TODO: how should this be used?

var TYPE_DIRECT_MESSAGE = uint16(0)
var TYPE_GROUP_MESSAGE = uint16(1)

type broadcastable interface {
	getScope() int
	getDestination() uuid.UUID // A group or user ID depending on the scope
	getType() uint16           // TODO: make these a custom type?
	getPayload() []byte
	deliveredTo(device uuid.UUID)
}

func (bounce *Bounce) broadcast(b broadcastable) {
	var cg *connectionGroup
	frameScope := b.getScope()
	if frameScope == SYNC_SCOPE {
		cg = bounce.devicePool.sync
	} else if frameScope == USER_SCOPE {
		cg = bounce.devicePool.users[b.getDestination()]
	} else if frameScope == GROUP_SCOPE {
		cg = bounce.devicePool.groups[b.getDestination()]
		//} else if frameScope == GLOBAL_SCOPE {
	} else {
		log.WithFields(log.Fields{
			"scope": frameScope,
		}).Fatal("cannot broadcast to an unknown scope")
	}

	for _, peer := range cg.connected {
		// Async try to write this message to every device that should be written to
		go func(frameType uint16, framePayload []byte) {
			err := peer.writeFrame(frameType, framePayload)
			if err != nil {
				// TODO: maybe don't handle this with the interface?  Going to depend on how normalized the protocol is with the db
				//b.deliveredTo(peer.device.ID)
			}
			// TODO: UI callbacks for delivery status
		}(b.getType(), b.getPayload()) // TODO: skip if it's already been delivered to this device?
	}
}

//
// Below are all the types of messages that can be sent in bounce, expressed as implementations of the broadcastable interface
//

//
// A message sent from user A to user B when user A imports user B's contact file.  User B will be promped to accept the invitation to connected in the
// user interface, and if this is accepted will respond to user A's device group with TODO
//
type contactImported struct {
}

//
// A direct message.  TODO: merge with the database object since we don't need to sign?
//
type directMessage struct {
	Source      uuid.UUID // TODO: could only include one of these depending on if syncing outgoing/incoming and if going to sync devices or other user
	Destination uuid.UUID
	Text        string
	payload     []byte
}

func (dm *directMessage) getScope() int {
	return USER_SCOPE
}

func (dm *directMessage) getDestination() uuid.UUID {
	return dm.Destination
}

func (dm *directMessage) getType() uint16 {
	return TYPE_DIRECT_MESSAGE
}

func (dm *directMessage) getPayload() []byte {
	if len(dm.payload) == 0 {
		// TODO: dm.payload = messagepack
	}
	return dm.payload
}

func (dm *directMessage) deliveredTo(destination uuid.UUID) {
	// TODO: update the database, need access to bounce.database...
}

//
// A group message is wrapped in a signed object because it can come from devices that are not owned by the author
//

type signedGroupMessage struct {
	Message     []byte
	Signature   []byte
	payload     []byte
	destination uuid.UUID
}

func newSignedGroupMessage(message GroupMessage, signer BounceNetwork) *signedGroupMessage { // TODO: remove the signer arg and put this on bounce
	marshalledMessage := []byte{}               // TODO: msgpack
	signature := signer.Sign(marshalledMessage) // TODO: just sign the SHA3 of the data for speed reasons

	sgm := &signedGroupMessage{
		Message:     marshalledMessage,
		Signature:   signature,
		destination: uuid.UUID{}, // TODO: message.Destination once it's a UUID
	}

	return sgm
}

func (sgm *signedGroupMessage) getScope() int {
	return GROUP_SCOPE
}

func (sgm *signedGroupMessage) getDestination() uuid.UUID {
	return sgm.destination
}

func (sgm *signedGroupMessage) getType() uint16 {
	return TYPE_GROUP_MESSAGE // TODO: after I figure out types
}

func (sgm *signedGroupMessage) getPayload() []byte { // TODO: broadcastable should have some handling for signature?  I'm probably going to need to wrap this.
	return sgm.payload
}

func (sgm *signedGroupMessage) deliveredTo(destination uuid.UUID) {
	// TODO: update the database, need access to bounce.database...
}
