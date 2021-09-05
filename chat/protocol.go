package chat

import (
	"github.com/google/uuid"
	"github.com/vmihailenco/msgpack/v5"
)

var SYNC_SCOPE = 0 // TODO: unexport these
var USER_SCOPE = 1
var GROUP_SCOPE = 2

//var GLOBAL_SCOPE = 3 // TODO: how should this be used?  all groups + all users not in groups?

var TYPE_DIRECT_MESSAGE = uint16(0)
var TYPE_GROUP_MESSAGE = uint16(1)

type broadcastable interface {
	getScope() int
	getDestination() uuid.UUID // A group or user ID depending on the scope
	getType() uint16           // TODO: make these a custom type?
	getPayload() []byte
	deliveredTo(address string)
}

func (bounce *Bounce) broadcast(b broadcastable) {
	peerScope, err := bounce.getBroadcastScope(b)
	if err != nil {
		// TODO: log
		return
	}

	for _, peer := range peerScope {
		// Async try to write this message to every device that should be written to
		go func(msg broadcastable) {
			peer.messages <- msg
		}(b)
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
		bytes, err := msgpack.Marshal(dm)
		if err != nil {
			// TODO: how to handle?
		}
		dm.payload = bytes
	}
	return dm.payload
}

func (dm *directMessage) deliveredTo(address string) {
	// TODO: update the database, need access to bounce.database...
	// TODO: should UI delivery status callbacks also happen here?
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
	marshalledMessage, err := msgpack.Marshal(message)
	if err != nil {
		// TODO: how to handle?
	}
	signature := signer.Sign(marshalledMessage) // TODO: just sign the SHA3 of the data for speed reasons

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

func (sgm *signedGroupMessage) deliveredTo(destination uuid.UUID) {
	// TODO: update the database, need access to bounce.database...
}
