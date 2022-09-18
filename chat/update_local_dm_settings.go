package chat

import (
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var updateLocalDMSettingsMutex sync.Mutex

type updateLocalDMSettings struct {
	ID                      uuid.UUID `gorm:"type:uuid;primary_key;"`
	Target                  uuid.UUID
	Timestamp               int64
	NotificationsMutedUntil int64 `gorm:"-"`
	payload                 []byte
	payloadMutex            sync.Mutex
}

func (ulds *updateLocalDMSettings) BeforeCreate(tx *gorm.DB) error {
	if ulds.ID == uuid.Nil {
		ulds.ID = uuid.New()
	}
	return nil
}

func (ulds *updateLocalDMSettings) AfterDelete(tx *gorm.DB) error {
	return tx.Where("frame_id = ? AND frame_type = ?", ulds.ID, typeUpdateLocalDMSettings).Delete(&deliveryRecord{}).Error
}

func (ulds *updateLocalDMSettings) getID() uuid.UUID {
	return ulds.ID
}

func (ulds *updateLocalDMSettings) getScope(_ uuid.UUID) int {
	return scopeSync
}

func (ulds *updateLocalDMSettings) getDestination(_ uuid.UUID) uuid.UUID {
	return uuid.Nil
}

func (ulds *updateLocalDMSettings) getType() uint16 {
	return typeUpdateLocalDMSettings
}

func (ulds *updateLocalDMSettings) getPayload() []byte {
	ulds.payloadMutex.Lock()
	defer ulds.payloadMutex.Unlock()

	if len(ulds.payload) == 0 {
		bytes, err := msgpack.Marshal(ulds)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal update local dm settings")
		}
		ulds.payload = bytes
	}
	return ulds.payload
}

func (ulds *updateLocalDMSettings) getTimestamp() int64 {
	return ulds.Timestamp
}

func (b *bounce) handleUpdateLocalDMSettings(peer string, payload []byte) {
	updateLocalDMSettingsMutex.Lock()
	defer updateLocalDMSettingsMutex.Unlock()

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

	// Find the user this update is referring to
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
			b.markDeliveredTo(&ulds, peer)

			// Inform the UI of any changes
			if targetUser.NotificationsMutedUntil != ulds.NotificationsMutedUntil {
				b.userInterface.DMNotificationsMutedUntilChanged(targetUser.ID, ulds.NotificationsMutedUntil)
			}

			// Apply the settings in this update to the user
			err = b.database.Model(&targetUser).Select(
				"notifications_muted_until",
				"last_local_dm_settings_update",
			).Updates(map[string]interface{}{
				"notifications_muted_until":     ulds.NotificationsMutedUntil,
				"last_local_dm_settings_update": ulds.Timestamp,
			}).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("error applying update local DM settings for user")
			}

			// Delete all old updates
			err = b.database.Where("target = ? AND timestamp != ?", ulds.Target, ulds.Timestamp).Delete(&updateLocalDMSettings{}).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error pruning old update local DM settings")
			}

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
			b.markDeliveredTo(&existingULDS, peer)
			// TODO: is this reachable?  This update is newer than our last applied update, but we already have it?
		}
	}
}

func (b *bounce) setDMNotificationMutedUntil(userID uuid.UUID, mutedUntil int64) {
	// Find the user to update
	var target user
	err := b.database.First(&target, "id = ?", userID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"user_id": userID,
			}).Warn("cannot update muted until for user not found in database")
			return
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up user")
		}
	}

	// Apply this change locally
	updateTime := time.Now().Unix()
	err = b.database.Model(&target).Select(
		"notifications_muted_until",
		"last_local_dm_settings_update",
	).Updates(map[string]interface{}{
		"notifications_muted_until":     mutedUntil,
		"last_local_dm_settings_update": updateTime,
	}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error updating muted until for user")
	}

	// Inform the UI that the change has been applied
	b.userInterface.DMNotificationsMutedUntilChanged(userID, mutedUntil)

	// Create an update for other sync devices and broadcast it
	update := &updateLocalDMSettings{
		Target:                  userID,
		Timestamp:               updateTime,
		NotificationsMutedUntil: mutedUntil,
	}
	err = b.database.Create(update).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error while saving an updateLocalDMSettngs")
	}
	go b.broadcast(update)

	// Delete all older updates
	err = b.database.Where("target = ? AND timestamp != ?", userID, updateTime).Delete(&updateLocalDMSettings{}).Error // TODO: use the primary key?
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error pruning old update local DM settings")
	}

}

func (b *bounce) getDMNotificationMutedUntil(userID uuid.UUID) (int64, error) {
	var u user
	err := b.database.Select("notifications_muted_until").First(&u, "id = ?", userID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"user_id": u,
			}).Warn("cannot query muted until settings for user not found in database")
			return 0, err
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up user muted until")
		}
	}

	return u.NotificationsMutedUntil, nil
}
