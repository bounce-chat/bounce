package chat

import (
	"time"

	"gorm.io/gorm"
)

type GroupMessage message

func (groupMessage *GroupMessage) BeforeCreate(tx *gorm.DB) error {
	groupMessage.ReceivedAt = time.Now().Unix()
	return nil
}

/*
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
*/
