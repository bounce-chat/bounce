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

var updateDMMutex sync.Mutex

// TODO: don't export
const UPDATE_DM_TYPE_CHANGE_MUTED_UNTIL = uint16(0)
const UPDATE_DM_TYPE_CHANGE_RETENTION = uint16(1)
const UPDATE_DM_TYPE_SET_CLEAR_BEFORE = uint16(2)

var ERR_UPDATE_DM_WITH_UNKNOWN_TYPE = errors.New("update DM has unknown update type")

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
		log.Fatal("attempt to create update DM with nil ID, ID must be set before creation")
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
	if ud.Type == UPDATE_DM_TYPE_CHANGE_MUTED_UNTIL {
		return scopeSync
	}

	return scopeUser
}

func (ud *updateDM) getDestination(myID uuid.UUID) uuid.UUID {
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

	// If we already have this update, we just mark that this peer has it too and return
	var existingUD updateDM
	err = b.database.Where("id = ?", ud.ID).First(&existingUD).Error
	if err == nil {
		b.markDeliveredTo(&existingUD, peer)
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
	go b.sendDirectAck(peer, frameReference{FrameID: ud.ID, Type: typeUpdateDM})

	// Mark that the peer that send this update already has it
	b.markDeliveredTo(&ud, peer)

	// Broadcast it
	go b.broadcast(&ud)
}

func (b *bounce) saveAndApplyUpdateDM(ud updateDM) error {
	// Look up the user that we're updating
	counterpartyID := xor(ud.Target, b.currentUserID())
	// TODO: if the counterparty ID is nil, then we're updating a self-DM
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

	switch ud.Type {
	case UPDATE_DM_TYPE_CHANGE_MUTED_UNTIL:
		return b.saveAndApplyUpdateDMChangeMutedUntil(u, ud)
	case UPDATE_DM_TYPE_CHANGE_RETENTION:
		return b.saveAndApplyUpdateDMChangeRetention(u, ud)
	case UPDATE_DM_TYPE_SET_CLEAR_BEFORE:
		return b.saveAndApplyUpdateDMSetClearBefore(u, ud)
	default:
		log.WithFields(log.Fields{
			"type": ud.Type,
		}).Warn("received update DM with unknown type")
		return ERR_UPDATE_DM_WITH_UNKNOWN_TYPE
	}

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

	// Check to make sure there isn't a more recent change we're already aware of
	var moreRecentUpdates bool
	err = b.database.Table("update_dms").
		Select("count(*) >= 1").
		Where("target = ? AND type = ? AND timestamp > ?", ud.Target, ud.Type, ud.Timestamp).
		Find(&moreRecentUpdates).
		Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error checking for more recent update DMs")
	} // TODO: DRY?

	// Decode the new muted until value
	mutedUntil := int64(binary.LittleEndian.Uint64(ud.Data))

	// Apply the update if it is the most recent one
	if !moreRecentUpdates {
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

	// Check to make sure there isn't a more recent change we're already aware of
	var moreRecentUpdates bool
	err = b.database.Table("update_dms").
		Select("count(*) >= 1").
		Where("target = ? AND type = ? AND timestamp > ?", ud.Target, ud.Type, ud.Timestamp).
		Find(&moreRecentUpdates).
		Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error checking for more recent update DMs")
	} // TODO: DRY?

	// Decode the new retention value
	retention := int64(binary.LittleEndian.Uint64(ud.Data))

	// Apply the update if it is the most recent one
	if !moreRecentUpdates {
		err = b.database.Model(&u).Update("retention", retention).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error updating user retention")
		}

		// Inform the UI
		b.userInterface.DMRetentionChanged(u.ID, ud.Actor, retention)

		// TODO: we should notify the UI even if there are more recent updates once the UI understands how to
		// insetion sort everything by timestamp
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
	err = b.database.Select("id").Where("written_at <= ? AND (direct_messages.destination = ? OR direct_messages.source = ?)", clearBefore, u.ID, u.ID).Find(&dms).Error // TODO: maybe identify these by XOR
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
		Type:      UPDATE_DM_TYPE_CHANGE_MUTED_UNTIL,
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
		Type:      UPDATE_DM_TYPE_CHANGE_RETENTION,
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
		Type:      UPDATE_DM_TYPE_SET_CLEAR_BEFORE,
		Data:      payload,
	})
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
