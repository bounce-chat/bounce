package chat

import (
	"strings"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
)

type updateLocalDMSettings struct {
	ID                      uuid.UUID `gorm:"type:uuid;primary_key;"`
	Target                  uuid.UUID
	Timestamp               int64
	DeliveredTo             string `msgpack:"-"`
	NotificationsEnabled    bool   `gorm:"-"`
	NotificationsMutedUntil uint64 `gorm:"-"`
	payload                 []byte
}

func (ulds *updateLocalDMSettings) BeforeCreate(tx *gorm.DB) error {
	ulds.ID = uuid.New()
	return nil
}

func (ulds *updateLocalDMSettings) getScope() int {
	return scopeSync
}

func (ulds *updateLocalDMSettings) getDestination(_ uuid.UUID) uuid.UUID {
	return uuid.Nil
}

func (ulds *updateLocalDMSettings) getType() uint16 {
	return typeUpdateLocalDMSettings
}

func (ulds *updateLocalDMSettings) getPayload() []byte {
	if len(ulds.payload) == 0 {
		bytes, err := msgpack.Marshal(ulds)
		if err != nil {
			// TODO: how to handle?
		}
		ulds.payload = bytes
	}
	return ulds.payload
}

func (ulds *updateLocalDMSettings) isAlreadyDeliveredTo(address string) bool {
	recipients := strings.Split(ulds.DeliveredTo, ",")
	for _, recipient := range recipients {
		if address == recipient {
			return true
		}
	}
	return false
}

func (b *bounce) markUpdateLocalDMSettingsDeliveredTo(ulds *updateLocalDMSettings, address string) {
	if !ulds.isAlreadyDeliveredTo(address) {
		if len(ulds.DeliveredTo) != 0 {
			ulds.DeliveredTo = ulds.DeliveredTo + ","
		}
		ulds.DeliveredTo = ulds.DeliveredTo + address

		err := b.database.Save(ulds).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":   err.Error(),
				"message": ulds.ID,
			}).Error("error updating local DM settings delivery status")
		}
	}
}

func (b *bounce) setDMNotificationSettings(u uuid.UUID, enabled bool) { // TODO: accept mutedUntil, or make it another call?
	update := &updateLocalDMSettings{
		Target:                  u,
		Timestamp:               time.Now().Unix(),
		NotificationsEnabled:    enabled,
		NotificationsMutedUntil: 0,
	}

	err := b.database.Create(update).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error while saving an updateLocalDMSettngs")
	}

	go b.broadcast(update)
}

func (b *bounce) handleUpdateLocalDMSettings(peer string, payload []byte) {
	// Make sure we only get these updates from sync devices
	//!if b.isSyncDevice(peer) {
	//	log.Warn()
	//	return
	//}

	// Update the target's DM settings to the ones in this message, as long
	// as the last update stored on that user isn't newer than this message
	// broadcast.

	// ACK it
	//go b.broadcast(&ack{
	//	UpdateLocalDMSettings: ulds.ID,
	//	destination: dev.ID,
	//})
	return
}
