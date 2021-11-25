package chat

import (
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
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
	Message     []byte
	Signature   []byte
	payload     []byte
	destination uuid.UUID
}

func (b *bounce) newSignedGroupMessage(message GroupMessage) *signedGroupMessage {
	marshalledMessage, err := msgpack.Marshal(message)
	if err != nil {
		// TODO: how to handle?
	}
	signature := b.network.Sign(marshalledMessage) // TODO: just sign the SHA3 of the data for speed reasons

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

func (sgm *signedGroupMessage) getDestination() uuid.UUID {
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

func (b *bounce) addUserToGroup(threadID, userID uuid.UUID) {
	log.WithFields(log.Fields{
		"thread": threadID,
		"user":   userID,
	}).Info("UI wants to add user to group")
}

func (b *bounce) renameGroup(threadID uuid.UUID, newName string) {
	log.WithFields(log.Fields{
		"thread":   threadID,
		"new_name": newName,
	}).Info("UI wants to rename a group")
}

func (b *bounce) changeGroupNotificationSettings(group uuid.UUID, enabled bool) {
	log.WithFields(log.Fields{
		"thread":                group,
		"notifications_enabled": enabled,
	}).Info("UI wants to chnage notification settings")
}
