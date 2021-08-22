package chat

import (
	"github.com/google/uuid"
)

type scope string

var SYNC_SCOPE = scope("sync")
var USER_SCOPE = scope("user")
var GROUP_SCOPE = scope("group")
var GLOBAL_SCOPE = scope("global") // TODO: how should this be used?

var TYPE_DIRECT_MESSAGE = uint16(0)

type broadcastable interface {
	getScope() scope
	getDestination() uuid.UUID // A group or user ID depending on the scope
	getType() uint16           // TODO: make these a custom type?
	getPayload() []byte
	deliveredTo(device uuid.UUID)
}

func (bounce *Bounce) broadcast(b broadcastable) {
	// TODO: this should only be called after the message has been persisted in the database

	cg := &connectionGroup{} // []*remoteDevice{}
	frameScope := b.getScope()
	if frameScope == SYNC_SCOPE {
		cg = bounce.devicePool.sync
	} else if frameScope == USER_SCOPE {
		cg = bounce.devicePool.users[b.getDestination()]
	} else if frameScope == GROUP_SCOPE {
		cg = bounce.devicePool.groups[b.getDestination()]
		//} else if frameScope == GLOBAL_SCOPE {
	} else {
		// TODO: log.Fatal
	}

	for _, peer := range cg.connected { // TODO: only online devices
		// Async try to write this message to every device that should be written to
		go func(frameType uint16, framePayload []byte) {
			err := peer.writeFrame(frameType, framePayload)
			if err != nil {
				// TODO: maybe don't handle this with the interface?  Going to depend on how normalized the protocol is with the db
				//b.deliveredTo(peer.device.ID)
			}
			// TODO: UI callbacks for delivery status
		}(b.getType(), b.getPayload())
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
// A direct message
//
type directMessage struct {
	Source      uuid.UUID // TODO: could only include one of these depending on if syncing outgoing/incoming and if going to syncdevices  or other user
	Destination uuid.UUID
	Text        string
	payload     []byte
}

func (dm *directMessage) getScope() scope {
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
