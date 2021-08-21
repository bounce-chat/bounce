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

	//peers := &connectionGroup{} // []*remoteDevice{}
	frameScope := b.getScope()
	if frameScope == SYNC_SCOPE {
		//	peers = bounce.devicePool.sync
	} else if frameScope == USER_SCOPE {
		//	peers = bounce.devicePool.users[b.getDestination()]
	} else if frameScope == GROUP_SCOPE {
		//	peers = bounce.devicePool.groups[b.getDestination()]
		//} else if frameScope == GLOBAL_SCOPE {
	} else {
		// TODO: log.Fatal
	}

	//for _, peer := range peers.remoteDevices { // TODO: only online devices
	//	// TODO: async in a goroutine
	//	//err := peer.writeFrame(b.getType(), b.getPayload())
	//	//if err != nil {
	//	// TODO: maybe don't handle this with the interface?  Going to depend on how normalized the protocol is with the db
	//	//b.deliveredTo(peer.device.ID)
	//	//}
	//	// TODO: UI callbacks for delivery status
	//}
}

//
// Below are all the types of messages that can be sent in bounce, expressed as implementations of the broadcastable interface
//

type contactImported struct {
}

//
// A direct message that implements the broadcastable interface
//
type directMessage struct {
	Source      uuid.UUID // TODO: could only include one of these depending on if syncing outgoing/incoming and if going to syncdevices  or other user
	Destination uuid.UUID
	Text        string
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
	return []byte{} // TODO: messagepack.  memoize?
}

func (dm *directMessage) deliveredTo(destination uuid.UUID) {
	// TODO: update the database, need access to bounce.database...
}
