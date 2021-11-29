package chat

import (
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type updateLocalDMSettings struct {
	ID                      uuid.UUID `gorm:"type:uuid;primary_key;"`
	Target                  uuid.UUID
	Timestamp               int64
	DeliveredTo             string `msgpack:"-"`
	NotificationsEnabled    bool   `gorm:"-"`
	NotificationsMutedUntil uint64 `gorm:"-"`
}

func (ulds *updateLocalDMSettings) BeforeCreate(tx *gorm.DB) error {
	ulds.ID = uuid.New()
	return nil
}

func (ulds updateLocalDMSettings) getScope() int {
	return scopeSync
}

func (ulds updateLocalDMSettings) getDestination(_ uuid.UUID) uuid.UUID {
	return uuid.Nil
}

func (ulds updateLocalDMSettings) getType() uint16 {
	return typeUpdateLocalDMSettings
}

func (ulds updateLocalDMSettings) getPayload() []byte {
	return []byte("")
}

func (ulds updateLocalDMSettings) isAlreadyDeliveredTo(address string) bool {
	return false
}

func (b *bounce) setDMNotificationSettings(u uuid.UUID, enabled bool) { // TODO: accept mutedUntil, or make it another call?
	update := updateLocalDMSettings{
		Target:                  u,
		Timestamp:               time.Now().Unix(),
		NotificationsEnabled:    enabled,
		NotificationsMutedUntil: 0,
	}

	err := b.database.Create(&update).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error while saving an updateLocalDMSettngs")
	}

	go b.broadcast(update)
}

func (b *bounce) handleUpdateLocalDMSettings(peer string, payload []byte) {
	// Update the target's DM settings to the ones in this message, as long
	// as the last update stored on that user isn't newer than this message
	// broadcast.  Must come from sync device.
	return
}
