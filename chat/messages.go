package chat

import (
	"strings"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
)

type message struct {
	ID          uuid.UUID `gorm:"type:uuid;primary_key;"`
	CreatedAt   int64
	ReceivedAt  int64
	Read        bool `msgpack:"-"`
	Source      uuid.UUID
	Destination uuid.UUID
	Text        string
	// TODO: other things that can be in a message, like a reference to an image, audio, video, or file attachment
	DeliveredTo string `msgpack:"-"` // Comma-separated list of addresses that have acked this message.  TODO: make an actual relation to the devices?
	payload     []byte
}

type GroupMessage message

func (groupMessage *GroupMessage) BeforeCreate(tx *gorm.DB) error {
	groupMessage.ReceivedAt = time.Now().Unix()
	return nil
}

type DirectMessage message

func (directMessage *DirectMessage) BeforeCreate(tx *gorm.DB) error {
	directMessage.ReceivedAt = time.Now().Unix()
	return nil
}

func (dm *DirectMessage) getScope() int {
	return USER_SCOPE
}

func (dm *DirectMessage) getDestination() uuid.UUID {
	return dm.Destination
}

func (dm *DirectMessage) getType() uint16 {
	return TYPE_DIRECT_MESSAGE
}

func (dm *DirectMessage) getPayload() []byte {
	if len(dm.payload) == 0 {
		bytes, err := msgpack.Marshal(dm)
		if err != nil {
			// TODO: how to handle?
		}
		dm.payload = bytes
	}
	return dm.payload
}

func (dm *DirectMessage) isAlreadyDeliveredTo(address string) bool {
	recipients := strings.Split(dm.DeliveredTo, ",")
	for _, recipient := range recipients {
		if address == recipient {
			return true
		}
	}
	return false
}

func (bounce *Bounce) markDeliveredTo(dm *DirectMessage, address string) {
	if !dm.isAlreadyDeliveredTo(address) {
		// TODO: don't add comma is it's empty. DRY
		if len(dm.DeliveredTo) == 0 {
			dm.DeliveredTo = dm.DeliveredTo + ","
		}
		dm.DeliveredTo = dm.DeliveredTo + address

		err := bounce.database.Save(dm).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":   err.Error(),
				"message": dm.ID,
			}).Error("error updating direct message delivery status")
		}
	}
}
