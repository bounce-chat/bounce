package chat

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type GroupMessage message

func (groupMessage *GroupMessage) BeforeCreate(tx *gorm.DB) error {
	groupMessage.SavedAt = time.Now().Unix()
	return nil
}

/*
//
// A group message is wrapped in a signed object because it can come from devices that are not owned by the author
//

type signedGroupMessage struct { // TODO: can the behavior of this be "merged" into the regular struct?  such that calling the broadcastable functions on that struct transparently does the signed ones?
	Message      []byte
	Signature    []byte
	payload      []byte
	payloadMutex sync.Mutex
	destination  uuid.UUID
}

func (b *bounce) newSignedGroupMessage(message GroupMessage) *signedGroupMessage {
	marshalledMessage, err := msgpack.Marshal(message)
	if err != nil {
		// TODO: how to handle?
	}
	signature := b.network.Sign(marshalledMessage) // TODO: just sign the hash of the data for speed reasons https://github.com/lukechampine/blake3 or https://github.com/zeebo/blake3

	sgm := &signedGroupMessage{
		Message:     marshalledMessage,
		Signature:   signature,
		destination: message.Destination,
	}

	return sgm
}

func (sgm *signedGroupMessage) getScope() int {
	return scopeGroup
}

func (sgm *signedGroupMessage) getDestination(_ uuid.UUID) uuid.UUID {
	return sgm.destination
}

func (sgm *signedGroupMessage) getType() uint16 {
	return typeGroupMessage // TODO: after I figure out types
}

func (sgm *signedGroupMessage) getPayload() []byte {
	return sgm.payload
}
*/

func (b *bounce) sendGroupMessage(message GroupMessage) uuid.UUID {
	return uuid.New()
}
