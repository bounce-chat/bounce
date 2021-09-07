package chat

import (
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
)

var SYNC_SCOPE = 0 // TODO: unexport these
var USER_SCOPE = 1
var GROUP_SCOPE = 2

//var GLOBAL_SCOPE = 3 // TODO: how should this be used?  all groups + all users not in groups?
var DEVICE_SCOPE = 4

var TYPE_DIRECT_MESSAGE = uint16(0)
var TYPE_GROUP_MESSAGE = uint16(1)
var TYPE_REFERENCE_OFFER = uint16(2)

type broadcastable interface {
	getScope() int
	getDestination() uuid.UUID // A group or user ID depending on the scope
	getType() uint16           // TODO: make these a custom type?
	getPayload() []byte
	isAlreadyDeliveredTo(address string) bool
}

func (bounce *Bounce) broadcast(b broadcastable) {
	peerScope, err := bounce.getBroadcastScope(b)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error getting broadcast targets")
		// TODO: don't need to error if there's just noone online  maybe handle other error logs in the get functions
		return
	}

	for _, peer := range peerScope {
		// Async try to write this message to every device that should be written to
		go func(dst chan broadcastable, msg broadcastable) {
			dst <- msg
		}(peer.messages, b)
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
// A group message is wrapped in a signed object because it can come from devices that are not owned by the author
//

type signedGroupMessage struct { // TODO: can the behavior of this be "merged" into the regular struct?  such that calling the broadcastable functions on that struct transparently does the signed ones?
	Message     []byte
	Signature   []byte
	payload     []byte
	destination uuid.UUID
}

func (bounce *Bounce) newSignedGroupMessage(message GroupMessage) *signedGroupMessage {
	marshalledMessage, err := msgpack.Marshal(message)
	if err != nil {
		// TODO: how to handle?
	}
	signature := bounce.network.Sign(marshalledMessage) // TODO: just sign the SHA3 of the data for speed reasons

	sgm := &signedGroupMessage{
		Message:     marshalledMessage,
		Signature:   signature,
		destination: message.Destination,
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

func (sgm *signedGroupMessage) getPayload() []byte {
	return sgm.payload
}
