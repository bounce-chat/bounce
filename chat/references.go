package chat

import (
	"github.com/google/uuid"
)

//
// References area a struct that contain a list of UUIDs for every type of message that Bounce stores
// in the database and needs to confirm delivery of to another device.  This structure is used for
// three types of broadcastable frames: reference offers, reference requests, and acks.
//
type references struct {
	_msgpack       struct{}  `msgpack:",omitempty"`
	ID             uuid.UUID `gorm:"type:uuid;primary_key;"`
	For            string    `msgpack:"-"`
	DirectMessages string    // Comma-separated list of DM UUIDs
	CatchUps       string    // for ack-ing catch ups to ensure delivery.  TODO: maybe not what we stick with, maybe break acks out of references
	destination    uuid.UUID
	payload        []byte
}
