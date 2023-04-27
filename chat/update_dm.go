package chat

import (
	"encoding/binary"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const updateDMTypeChangeMutedUntil = uint16(0)
const updateDMTypeChangeRetention = uint16(1)
const updateDMTypeSetClearBefore = uint16(2)

var ERR_UPDATE_DM_WITH_UNKNOWN_TYPE = errors.New("update DM has unknown update type")

var updateDMMutex sync.Mutex

//
// An updateDM frame changes the settings of a direct message thread, such as retention or notification settings.
// Some settings, like retention, must be observed by both participants of the DM, where others like notification
// settings are only sent to sync devices.  The data field of the structure contains different data depending on
// the type of update.
//
type updateDM struct {
	ID           uuid.UUID `gorm:"type:uuid;primary_key;"`
	Actor        uuid.UUID
	Target       uuid.UUID // XOR of two users in the DM
	Timestamp    int64
	Type         uint16
	Data         []byte
	payload      []byte
	payloadMutex sync.Mutex
}

func (ud *updateDM) BeforeCreate(tx *gorm.DB) error {
	if ud.ID == uuid.Nil {
		return errors.New("update DM ID must be set before creation")
	}

	return nil
}

func (ud *updateDM) AfterDelete(tx *gorm.DB) error {
	return tx.Where("frame_id = ? AND frame_type = ?", ud.ID, typeUpdateDM).Delete(&deliveryRecord{}).Error
}

func (ud *updateDM) getID() uuid.UUID {
	return ud.ID
}

func (ud *updateDM) getScope(_ uuid.UUID) int {
	if ud.Type == updateDMTypeChangeMutedUntil {
		return scopeSync
	}

	return scopeUser
}

func (ud *updateDM) getDestination(myID uuid.UUID) uuid.UUID {
	if ud.Type == updateDMTypeChangeMutedUntil {
		return myID
	}

	return xor(myID, ud.Target)
}

func (ud *updateDM) getType() uint16 {
	return typeUpdateDM
}

func (ud *updateDM) getPayload() []byte {
	ud.payloadMutex.Lock()
	defer ud.payloadMutex.Unlock()

	if len(ud.payload) == 0 {
		bytes, err := msgpack.Marshal(ud)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal update dm settings")
		}
		ud.payload = bytes
	}
	return ud.payload
}

func (ud *updateDM) getAuthor() uuid.UUID {
	return ud.Actor
}

func (ud *updateDM) getTimestamp() int64 {
	return ud.Timestamp
}

func (b *bounce) handleUpdateDM(peer string, payload []byte) {
	updateDMMutex.Lock()
	defer updateDMMutex.Unlock()

	// Unmarshall it
	var ud updateDM
	err := msgpack.Unmarshal(payload, &ud)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling update DM settings")
		return
	}

	// Find the user this applies to
	counterparty := xor(b.currentUserID(), ud.Target)
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
	srcDevice, exists := b.getDeviceFromAddress(peer)
	if !exists || !(srcDevice.UserID == b.currentUserID() || srcDevice.UserID == counterparty) {
		log.WithFields(log.Fields{
			"peer":        peer,
			"target_user": counterparty,
		}).Warn("rejecting update DM settings from out of scope device")
		return
	}

	// If we already have this update, we just mark that this peer has it too, ack it, and return
	var existingUD updateDM
	err = b.database.Where("id = ?", ud.ID).First(&existingUD).Error
	if err == nil {
		b.markDeliveredTo(&existingUD, peer)
		go b.sendAck(peer, typeUpdateDM, ud.ID)
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up update DM")
	}

	// Apply this update locally
	err = b.saveAndApplyUpdateDM(ud)
	if err != nil {
		log.WithFields(log.Fields{
			"user":   xor(ud.Target, b.currentUserID()),
			"device": srcDevice.ID,
			"type":   ud.Type,
			"error":  err.Error(),
		}).Error("error applying update DM")
		return
	}

	// Ack it
	go b.sendAck(peer, typeUpdateDM, ud.ID)

	// Mark that the peer that send this update already has it
	b.markDeliveredTo(&ud, peer)

	// Broadcast it
	go b.broadcast(&ud)
}

func (b *bounce) saveAndApplyUpdateDM(ud updateDM) error {
	// Look up the user that we're updating
	counterpartyID := xor(ud.Target, b.currentUserID())
	var u user
	err := b.database.Where("id = ?", counterpartyID).First(&u).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"user_id": counterpartyID,
			}).Error("update DM specifies user not found in database")
			return err
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up user")
		}
	}

	// Apply the function that handles this type of update
	switch ud.Type {
	case updateDMTypeChangeMutedUntil:
		return b.saveAndApplyUpdateDMChangeMutedUntil(u, ud)
	case updateDMTypeChangeRetention:
		return b.saveAndApplyUpdateDMChangeRetention(u, ud)
	case updateDMTypeSetClearBefore:
		return b.saveAndApplyUpdateDMSetClearBefore(u, ud)
	default:
		log.WithFields(log.Fields{
			"type": ud.Type,
		}).Warn("received update DM with unknown type")
		return ERR_UPDATE_DM_WITH_UNKNOWN_TYPE
	}

	// Update the activity timestamp on the user model
	b.updateLastUserActivity(xor(b.currentUserID(), ud.Target), ud.Timestamp)

	return nil
}

func (b *bounce) saveAndApplyUpdateDMChangeMutedUntil(u user, ud updateDM) error {
	// Notification settings can only be changed by sync devices
	if ud.Actor != b.currentUserID() {
		return ERR_MUTED_UNTIL_ONLY_MUTABLE_BY_SELF
	}

	// Save the update DM
	err := b.database.Create(&ud).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving update DM")
	}

	// Apply the update if it is the most recent one
	if !b.moreRecentUpdateDM(ud) {
		mutedUntil := int64(binary.LittleEndian.Uint64(ud.Data))

		err = b.database.Model(&u).Update("muted_until", mutedUntil).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error updating user muted until")
		}

		// Inform the UI
		b.userInterface.DMMutedUntilChanged(u.ID, mutedUntil)
	}

	return nil
}

func (b *bounce) saveAndApplyUpdateDMChangeRetention(u user, ud updateDM) error {
	// Save the update DM
	err := b.database.Create(&ud).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving update DM")
	}

	// Decode the new retention value
	retention := int64(binary.LittleEndian.Uint64(ud.Data))

	// Inform the UI
	b.userInterface.DMRetentionChanged(u.ID, ud.Actor, retention, ud.Timestamp)

	// Apply the update if it is the most recent one
	if !b.moreRecentUpdateDM(ud) {
		err = b.database.Model(&u).Update("retention", retention).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error updating user retention")
		}
	}

	return nil
}

func (b *bounce) saveAndApplyUpdateDMSetClearBefore(u user, ud updateDM) error {
	// Save the update DM
	err := b.database.Create(&ud).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving update DM")
	}

	// Decode the new retention value
	clearBefore := int64(binary.LittleEndian.Uint64(ud.Data))

	dms := []DirectMessage{}
	err = b.database.Select("id").Where("written_at <= ? AND (direct_messages.destination = ? OR direct_messages.source = ?)", clearBefore, u.ID, u.ID).Find(&dms).Error
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
	b.userInterface.DMChatHistoryCleared(u.ID, ud.Actor)

	// Update the clear before value on the group if this one is newer
	if u.ClearBefore < clearBefore {
		err := b.database.Model(&u).Update("clear_before", clearBefore).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":        err.Error(),
				"user_id":      u.ID,
				"clear_before": clearBefore,
			}).Fatal("database error updating user clear before")
		}
	}

	return nil
}

func (b *bounce) setDMMutedUntil(userID uuid.UUID, mutedUntil int64) error {
	payload := make([]byte, 8)
	binary.LittleEndian.PutUint64(payload, uint64(mutedUntil))

	return b.applyAndBroadcastUpdateDM(updateDM{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    xor(userID, b.currentUserID()),
		Timestamp: time.Now().Unix(),
		Type:      updateDMTypeChangeMutedUntil,
		Data:      payload,
	})
}

func (b *bounce) setDMRetention(userID uuid.UUID, retention int64) error {
	payload := make([]byte, 8)
	binary.LittleEndian.PutUint64(payload, uint64(retention))

	return b.applyAndBroadcastUpdateDM(updateDM{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    xor(userID, b.currentUserID()),
		Timestamp: time.Now().Unix(),
		Type:      updateDMTypeChangeRetention,
		Data:      payload,
	})
}

func (b *bounce) clearDMChatHistory(userID uuid.UUID) error {
	payload := make([]byte, 8)
	binary.LittleEndian.PutUint64(payload, uint64(time.Now().Unix()))

	return b.applyAndBroadcastUpdateDM(updateDM{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    xor(userID, b.currentUserID()),
		Timestamp: time.Now().Unix(),
		Type:      updateDMTypeSetClearBefore,
		Data:      payload,
	})
}

func (b *bounce) moreRecentUpdateDM(ud updateDM) bool {
	var moreRecentUpdates bool

	err := b.database.Table("update_dms").
		Select("count(*) >= 1").
		Where("target = ? AND type = ? AND timestamp > ?", ud.Target, ud.Type, ud.Timestamp).
		Find(&moreRecentUpdates).
		Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error checking for more recent update DMs")
	}

	return moreRecentUpdates
}

func (b *bounce) applyAndBroadcastUpdateDM(ud updateDM) error {
	// Apply the update locally
	err := b.saveAndApplyUpdateDM(ud)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error applying update DM")
		return err
	}

	// Broadcast
	go b.broadcast(&ud)

	return nil
}
