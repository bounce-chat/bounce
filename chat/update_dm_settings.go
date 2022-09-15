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

var updateDMSettingsMutex sync.Mutex

type updateDMSettings struct {
	ID           uuid.UUID `gorm:"type:uuid;primary_key;"`
	Actor        uuid.UUID
	Timestamp    int64
	Xor          uuid.UUID
	Retention    int64
	ClearBefore  int64
	payload      []byte
	payloadMutex sync.Mutex
}

func (uds *updateDMSettings) BeforeCreate(tx *gorm.DB) error {
	if uds.ID == uuid.Nil {
		uds.ID = uuid.New()
	} // TODO: fatal if already set?
	return nil
}

func (uds *updateDMSettings) AfterDelete(tx *gorm.DB) error {
	return tx.Where("frame_id = ? AND frame_type = ?", uds.ID, typeUpdateDMSettings).Delete(&deliveryRecord{}).Error
}

func (uds *updateDMSettings) getID() uuid.UUID {
	return uds.ID
}

func (ulds *updateDMSettings) getScope(_ uuid.UUID) int {
	return scopeUser
}

func (uds *updateDMSettings) getDestination(myID uuid.UUID) uuid.UUID {
	return xor(myID, uds.Xor)
}

func (uds *updateDMSettings) getType() uint16 {
	return typeUpdateDMSettings
}

func (uds *updateDMSettings) getPayload() []byte {
	uds.payloadMutex.Lock()
	defer uds.payloadMutex.Unlock()

	if len(uds.payload) == 0 {
		bytes, err := msgpack.Marshal(uds)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal update dm settings")
		}
		uds.payload = bytes
	}
	return uds.payload
}

func (uds *updateDMSettings) getTimestamp() int64 {
	return uds.Timestamp
}

func (b *bounce) setDMRetention(u uuid.UUID, retention int64) {
	// Find the user
	var target user
	err := b.database.First(&target, "id = ?", u).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"user_id": u,
			}).Warn("cannot update retention settings for user not found in database")
			return
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up user")
		}
	}

	// Apply the change locally
	updateTime := time.Now().Unix()
	err = b.database.Model(&target).Select(
		"message_retention",
		"last_dm_settings_update",
	).Updates(map[string]interface{}{
		"message_retention":       retention,
		"last_dm_settings_update": updateTime,
	}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error updating retention settings for user")
	}

	// Inform the UI that the change has been applied
	b.userInterface.DMRetentionChanged(u, b.currentUserID(), retention)

	// Create an updateDMSettings and broadcast it
	update := &updateDMSettings{
		Timestamp:   updateTime,
		Xor:         xor(u, b.currentUserID()),
		Actor:       b.currentUserID(),
		Retention:   retention,
		ClearBefore: target.ClearBefore,
	}
	err = b.database.Create(update).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error while saving an updateDMSettngs")
	}
	go b.broadcast(update)
}

func (b *bounce) clearDMChatHistory(userID uuid.UUID) {
	clearTime := time.Now().Unix()

	var target user
	err := b.database.First(&target, "id = ?", userID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"user_id": userID,
			}).Warn("cannot clear chat history for user not found in database")
			return
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up user")
		}
	}

	update := &updateDMSettings{
		Timestamp:   clearTime,
		Xor:         xor(userID, b.currentUserID()),
		Actor:       b.currentUserID(),
		Retention:   target.MessageRetention,
		ClearBefore: clearTime,
	}
	err = b.database.Create(update).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error while saving an updateDMSettngs")
	}
	b.broadcast(update)

	dms := []DirectMessage{}
	err = b.database.Select("id").Where("written_at <= ? AND (source = ? OR destination = ?)", clearTime, userID, userID).Find(&dms).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error selecting direct messages to delete while clearing chat history")
	}
	for _, dm := range dms {
		err := b.database.Delete(&dm).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
				"id":    dm.ID,
			}).Fatal("error deleting direct message while clearing chat history")
		}
		b.userInterface.DeleteMessage(dm.ID)
	}
	b.userInterface.DMChatHistoryCleared(userID, b.currentUserID())
}

func (b *bounce) handleUpdateDMSettings(peer string, payload []byte) {
	updateDMSettingsMutex.Lock()
	defer updateDMSettingsMutex.Unlock()

	// Unmarshall it
	var uds updateDMSettings
	err := msgpack.Unmarshal(payload, &uds)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling update DM settings")
		return
	}

	// Find the user this applies to
	counterparty := xor(b.currentUserID(), uds.Xor)
	var targetUser user
	err = b.database.Preload(clause.Associations).First(&targetUser, "id = ?", counterparty).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"user_id": counterparty,
			}).Error("cannot update DM settings for unknown user")
			return
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up user for DM settings update")
		}
	}

	// Make sure this came from a sync device or one of the user's devices
	dev, exists := b.getDeviceFromAddress(peer)
	if !exists || !(dev.UserID == b.currentUserID() || dev.UserID == counterparty) {
		log.WithFields(log.Fields{
			"peer":        peer,
			"target_user": counterparty,
		}).Warn("rejecting update DM settings from out of scope device")
		return
	}

	// Ack it
	go b.broadcast(&ack{
		UpdateDMSettings: uds.ID.String(),
		destination:      dev.ID,
	})

	// If the timestamp is newer than the last update we're aware of, apply the update
	if uds.Timestamp > targetUser.LastDMSettingsUpdate {
		var existingUDS updateDMSettings
		err = b.database.Where("id = ?", uds.ID).First(&existingUDS).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// This update is newer than our last update and we don't have it saved.  We save it,
			// apply it, and broadcast it to the rest of the devices.
			err = b.database.Create(&uds).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("error saving update DM settings")
			}
			b.markDeliveredTo(&uds, peer)

			// Inform the UI of any changes
			if targetUser.MessageRetention != uds.Retention {
				b.userInterface.DMRetentionChanged(targetUser.ID, uds.Actor, uds.Retention)
			}

			if targetUser.ClearBefore != uds.ClearBefore {
				dms := []DirectMessage{}
				err := b.database.Select("id").Where("written_at < ? AND (source = ? OR destination = ?)", uds.ClearBefore, targetUser.ID, targetUser.ID).Find(&dms).Error
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Fatal("error selecting direct messages to delete while clearing chat history")
				}
				for _, dm := range dms {
					err := b.database.Delete(&dm).Error
					if err != nil {
						log.WithFields(log.Fields{
							"error": err.Error(),
							"id":    dm.ID,
						}).Fatal("error deleting direct message while clearing chat history")
					}
					b.userInterface.DeleteMessage(dm.ID)
				}
				b.userInterface.DMChatHistoryCleared(counterparty, uds.Actor)
			}

			// Apply the settings in this update to the user
			err = b.database.Model(&targetUser).Select(
				"message_retention",
				"clear_before",
				"last_dm_settings_update",
			).Updates(map[string]interface{}{
				"message_retention":       uds.Retention,
				"clear_before":            uds.ClearBefore,
				"last_dm_settings_update": uds.Timestamp,
			}).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("error applying update DM settings for user")
			}

			// Broadcast it to other devices
			go b.broadcast(&uds)
		} else if err != nil {
			// There was some other database error while attempting to look this up.
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up update DM settings")
		} else {
			// TODO: is this reachable?
			b.markDeliveredTo(&existingUDS, peer)
		}
	}
}

func xor(uuid1, uuid2 uuid.UUID) uuid.UUID {
	xored := [16]byte{}
	for i, b := range uuid1 {
		xored[i] = b ^ uuid2[i]
	}

	xorUUID, err := uuid.FromBytes(xored[:])
	if err != nil {
		log.WithFields(log.Fields{
			"uuid1": uuid1,
			"uuid2": uuid2,
			"xored": xored,
			"error": err.Error(),
		}).Fatal("unable to create UUID from XORed UUIDs")
	}

	return xorUUID
}
