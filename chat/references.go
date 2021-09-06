package chat

import (
	"github.com/google/uuid"
	"github.com/vmihailenco/msgpack/v5"
)

//
// References area a struct that contain a list of UUIDs for every type of message that Bounce stores
// in the database and needs to confirm delivery of to another device.  This structure is used for
// three types of broadcastable frames: reference offers, reference requests, and acks.
//
type references struct {
	_msgpack       struct{} `msgpack:",omitempty"`
	destination    uuid.UUID
	payload        []byte
	DirectMessages []uuid.UUID
}

type referenceOffer references

func (bounce *Bounce) getReferenceOfferFor(address string) *referenceOffer {
	// Load all the messages from the database that this device should know about that we
	// can't confirm we've already delivired to this device
	// SELECT * FROM direct_messages WHERE CREATED_AT > 1 week ago AND destination = user_id AND delivered_to NOT INCLUDE device_id;
	return &referenceOffer{}
}

func (ro *referenceOffer) getScope() int {
	return DEVICE_SCOPE
}

func (ro *referenceOffer) getDestination() uuid.UUID {
	return ro.destination
}

func (ro *referenceOffer) getType() uint16 {
	return TYPE_REFERENCE_OFFER
}

func (ro *referenceOffer) getPayload() []byte {
	if len(ro.payload) == 0 {
		bytes, err := msgpack.Marshal(ro)
		if err != nil {
			// TODO: how to handle?
		}
		ro.payload = bytes
	}
	return ro.payload
}

type referenceRequest references

//	getScope() int
//	getDestination() uuid.UUID
//	getType() uint16
//	getPayload() []byte

type ack references

//	getScope() int
//	getDestination() uuid.UUID
//	getType() uint16
//	getPayload() []byte
