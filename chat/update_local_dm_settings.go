package chat

import (
	"github.com/google/uuid"
)

type updateLocalDMSettings struct {
	Target      uuid.UUID
	Timestamp   int64
	DeliveredTo string
	//destination uuid.UUID
	// put the rest of the details in here but exclude them from gorm?
	NotificationsEnabled    bool   `gorm:"-"`
	NotificationsMutedUntil uint64 `gorm:"-"`
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

func (b *bounce) handleUpdateLocalDMSettings(peer string, payload []byte) {
	// Update the target's DM settings to the ones in this message, as long
	// as the last update stored on that user isn't newer than this message
	// broadcast.  Must come from sync device.
	return
}
