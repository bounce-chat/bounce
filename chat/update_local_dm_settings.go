package chat

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	dev, exists := b.getDeviceFromAddress(peer)
	if !exists || dev.UserID != b.currentUserID() {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("update local DM settings received from device that is not a sync device")
		return
	}

	// Unmarshal it
	var ulds updateLocalDMSettings
	err := msgpack.Unmarshal(payload, &ulds)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling update local DM settings")
		return
	}

	// Reguardless of what we do with this, we should ack it
	go b.broadcast(&ack{
		UpdateLocalDMSettings: ulds.ID.String(),
		destination:           dev.ID,
	})

	// Find the user this update if refering to
	var targetUser user
	err = b.database.Preload(clause.Associations).First(&targetUser, "id = ?", ulds.Target).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"user_id": ulds.Target,
			}).Error("cannot update local DM settings for unknown user")
			return
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up user for local DM settings update")
		}
	}

	// We only need to do anything if the timestamp in this update is newer than the last time we updated the user.
	// If it isn't, we can just ignore this message.
	if ulds.Timestamp > targetUser.LastLocalDMSettingsUpdate {
		b.uldsExistenceCheck.Lock()
		defer b.uldsExistenceCheck.Unlock()

		var existingULDS updateLocalDMSettings
		err = b.database.Where("id = ?", ulds.ID).First(&existingULDS).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// This update is newer than our last update and we don't have it saved.  We save it,
			// apply it, and broadcast it to the rest of the sync devices.
			err = b.database.Create(&ulds).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("error saving update local DM settings")
			}

			// Apply the settings in this update to the user
			targetUser.NotificationsEnabled = ulds.NotificationsEnabled
			targetUser.NotificationsMutedUntil = ulds.NotificationsMutedUntil
			targetUser.LastLocalDMSettingsUpdate = ulds.Timestamp
			err = b.database.Save(targetUser).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("error update local DM settings for user")
			}

			// Delete all old updates
			err = b.database.Where("target = ? AND timestamp != ?", ulds.Target, ulds.Timestamp).Delete(updateLocalDMSettings{}).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error pruning old update local DM settings")
			}

			// Inform the UI about these changes
			//TODO
			//b.userInterface.LocalDMSettingsUpdated()

			// Broadcast it to other sync devices
			go b.broadcast(&ulds)
		} else if err != nil {
			// There was some other database error while attempting to look this up.
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up update local DM settings")
		} else {
			// There was no error looking up the update.  We have it, all we need to do is make sure
			// to mark is as delivered to the peer who sent it to us.
			b.markUpdateLocalDMSettingsDeliveredTo(&existingULDS, peer)
		}
	}
}
