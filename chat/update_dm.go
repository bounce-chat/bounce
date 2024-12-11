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
const updateDMTypeSetReadReceipts = uint16(3)
const updateDMTypeSetTypingIndicators = uint16(4)

var errUpdateDMWithUnknownType = errors.New("update DM has unknown update type")
var errInvalidPayloadLength = errors.New("invalid payload length")
var errInvalidOverriddenValue = errors.New("invalid value for overridden byte")
var errInvalidEnabledValue = errors.New("invalid value for enabled byte")
var errSyncScopedMessageFromNonSyncSource = errors.New("sync scoped frame can only come from sync device")

const readReceiptsDefaultValue = 0x00
const readReceiptsOverriddenValue = 0x01
const readReceiptsEnabledValue = 0x00
const readReceiptsDisabledValue = 0x01
const typingIndicatorsDefaultValue = 0x00
const typingIndicatorsOverriddenValue = 0x01
const typingIndicatorsEnabledValue = 0x00
const typingIndicatorsDisabledValue = 0x01

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
	Seen         bool `msgpack:"-"`
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
	if ud.Type == updateDMTypeChangeMutedUntil || ud.Type == updateDMTypeSetReadReceipts || ud.Type == updateDMTypeSetTypingIndicators || ud.Target == uuid.Nil {
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

func (ud *updateDM) getAuthor() uuid.UUID {
	return ud.Actor
}

func (ud *updateDM) getTimestamp() int64 {
	return ud.Timestamp
}

func (ud *updateDM) validPayload() error {
	switch ud.Type {
	case updateDMTypeSetReadReceipts:
		if len(ud.Data) != 2 {
			return errInvalidPayloadLength
		}
		if !(ud.Data[0] == readReceiptsDefaultValue || ud.Data[0] == readReceiptsOverriddenValue) {
			return errInvalidOverriddenValue
		}
		if !(ud.Data[1] == readReceiptsEnabledValue || ud.Data[1] == readReceiptsDisabledValue) {
			return errInvalidEnabledValue
		}
	case updateDMTypeSetTypingIndicators:
		if len(ud.Data) != 2 {
			return errInvalidPayloadLength
		}
		if !(ud.Data[0] == typingIndicatorsDefaultValue || ud.Data[0] == typingIndicatorsOverriddenValue) {
			return errInvalidOverriddenValue
		}
		if !(ud.Data[1] == typingIndicatorsEnabledValue || ud.Data[1] == typingIndicatorsDisabledValue) {
			return errInvalidEnabledValue
		}
	}

	return nil
}

func (b *bounce) handleUpdateDM(peer string, payload []byte, catchUp bool) broadcastable {
	updateDMMutex.Lock()
	defer updateDMMutex.Unlock()

	// Unmarshall it
	var ud updateDM
	err := msgpack.Unmarshal(payload, &ud)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling update DM settings")
		return nil
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
			return nil
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
		return nil
	}

	// Sync scoped messages must come from a sync device
	if ud.getScope(b.currentUserID()) == scopeSync {
		if srcDevice.UserID != b.currentUserID() {
			log.WithFields(log.Fields{
				"peer":        peer,
				"target_user": counterparty,
			}).Warn("rejecting update DM for muting a thread that is not sent by sync device")
			return nil
		}
	}

	// If we already have this update, we just mark that this peer has it too, ack it, and return
	var existingUD updateDM
	err = b.database.Where("id = ?", ud.ID).First(&existingUD).Error
	if err == nil {
		return &existingUD
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
		return nil
	}

	return &ud
}

func (b *bounce) updateDMState(userID uuid.UUID) {
	// Set the initial values for the DM
	retention := int64(0)
	mutedUntil := int64(0)
	clearBefore := int64(0)
	readReceiptsOverridden := false
	readReceiptsEnabled := true
	typingIndicatorsOverridden := false
	typingIndicatorsEnabled := true

	// Find all updates
	uds := []updateDM{}
	err := b.database.Where("target =  ?", xor(userID, b.currentUserID())).Order("timestamp asc").Find(&uds).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up updateDMs")
	}

	// Update the DM fields in the orders of the updates
	for _, ud := range uds {
		err := ud.validPayload()
		if err != nil {
			log.WithFields(log.Fields{
				"id":    ud.ID,
				"error": err.Error(),
			}).Error("skipping update DM with invalid payload")
			continue
		}

		switch ud.Type {
		case updateDMTypeChangeMutedUntil:
			mutedUntil = int64(binary.LittleEndian.Uint64(ud.Data))
		case updateDMTypeChangeRetention:
			retention = int64(binary.LittleEndian.Uint64(ud.Data))
		case updateDMTypeSetClearBefore:
			clearBefore = int64(binary.LittleEndian.Uint64(ud.Data))
		case updateDMTypeSetReadReceipts:
			readReceiptsOverridden = ud.Data[0] == readReceiptsOverriddenValue
			readReceiptsEnabled = ud.Data[1] == readReceiptsEnabledValue
		case updateDMTypeSetTypingIndicators:
			typingIndicatorsOverridden = ud.Data[0] == typingIndicatorsOverriddenValue
			typingIndicatorsEnabled = ud.Data[1] == typingIndicatorsEnabledValue
		default:
			log.WithFields(log.Fields{
				"type": ud.Type,
			}).Warn("ignoring update DM with unknown type")
		}
	}

	// Set the values in the database
	err = b.database.Model(&user{}).Where("id = ?", userID).Updates(map[string]interface{}{
		"retention":                    retention,
		"muted_until":                  mutedUntil,
		"clear_before":                 clearBefore,
		"read_receipts_overridden":     readReceiptsOverridden,
		"read_receipts_enabled":        readReceiptsEnabled,
		"typing_indicators_overridden": typingIndicatorsOverridden,
		"typing_indicators_enabled":    typingIndicatorsEnabled,
	}).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"user_id": userID,
				"error":   err.Error(),
			}).Error("user not found when updating DM fields")
			return
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error updating user fields")
		}
	}

	// Inform the UI of the current state
	b.userInterface.SetDMState(
		userID,
		DMState{
			Retention:                      retention,
			MutedUntil:                     mutedUntil,
			OverrideReadReceiptSetting:     readReceiptsOverridden,
			ReadReceiptsEnabled:            readReceiptsEnabled,
			OverrideTypingIndicatorSetting: typingIndicatorsOverridden,
			TypingIndicatorsEnabled:        typingIndicatorsEnabled,
		},
	)
}

func (b *bounce) saveAndApplyUpdateDM(ud updateDM) error {
	// Only sync devices can change sync scoped messages
	if ud.getScope(b.currentUserID()) == scopeSync {
		if ud.Actor != b.currentUserID() {
			return errSyncScopedMessageFromNonSyncSource
		}
	}

	// Validate payload
	err := ud.validPayload()
	if err != nil {
		return err
	}

	// Save the update DM
	err = b.database.Create(&ud).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving update DM")
	}

	// Look up the user that we're updating
	counterpartyID := xor(ud.Target, b.currentUserID())
	var u user
	err = b.database.Where("id = ?", counterpartyID).First(&u).Error
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
		// nothing to show in thread
	case updateDMTypeChangeRetention:
		err = b.informUIUpdateDMChangeRetention(u, ud)
		if err != nil {
			return err
		}
	case updateDMTypeSetClearBefore:
		err = b.informUIUpdateDMSetClearBefore(u, ud)
		if err != nil {
			return err
		}
	case updateDMTypeSetReadReceipts:
		// No UI status changes for read receipt settings
	case updateDMTypeSetTypingIndicators:
		// No UI status changes for read receipt settings
	default:
		log.WithFields(log.Fields{
			"type": ud.Type,
		}).Warn("received update DM with unknown type")
		return errUpdateDMWithUnknownType
	}

	// Update the activity timestamp on the user model
	if ud.getScope(b.currentUserID()) != scopeSync {
		b.updateLastUserActivity(xor(b.currentUserID(), ud.Target), ud.Timestamp)
	}

	// Update the database and UI
	b.updateDMState(counterpartyID)

	return nil
}

func (b *bounce) informUIUpdateDMChangeRetention(u user, ud updateDM) error {
	// Decode the new retention value
	retention := int64(binary.LittleEndian.Uint64(ud.Data))

	// Inform the UI
	b.userInterface.DMRetentionChanged(UpdateDMRetention{
		ID:        ud.ID,
		Thread:    u.ID,
		Actor:     ud.Actor,
		Timestamp: ud.Timestamp,
		Retention: retention,
	})

	return nil
}

func (b *bounce) informUIUpdateDMSetClearBefore(u user, ud updateDM) error {
	// Decode the new retention value
	clearBefore := int64(binary.LittleEndian.Uint64(ud.Data))

	// Find and delete any DMs older than the retention value
	dms := []directMessage{}
	err := b.database.Select("id").Where("written_at <= ? AND xor = ?", clearBefore, ud.Target).Find(&dms).Error
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
		b.userInterface.DeleteItem(dm.ID)
	}
	b.userInterface.DMChatHistoryCleared(UpdateDMClearHistory{
		ID:        ud.ID,
		Thread:    u.ID,
		Actor:     ud.Actor,
		Timestamp: ud.Timestamp,
		ClearTime: clearBefore,
	})

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

func (b *bounce) setDMReadReceiptSettings(userID uuid.UUID, override bool, enabled bool) error {
	payload := []byte{}
	if override {
		payload = append(payload, readReceiptsOverriddenValue)
	} else {
		payload = append(payload, readReceiptsDefaultValue)

	}
	if enabled {
		payload = append(payload, readReceiptsEnabledValue)
	} else {
		payload = append(payload, readReceiptsDisabledValue)
	}

	return b.applyAndBroadcastUpdateDM(updateDM{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    xor(userID, b.currentUserID()),
		Timestamp: time.Now().Unix(),
		Type:      updateDMTypeSetReadReceipts,
		Data:      payload,
	})
}

func (b *bounce) setDMTypingIndicatorSettings(userID uuid.UUID, override bool, enabled bool) error {
	payload := []byte{}
	if override {
		payload = append(payload, typingIndicatorsOverriddenValue)
	} else {
		payload = append(payload, typingIndicatorsDefaultValue)

	}
	if enabled {
		payload = append(payload, typingIndicatorsEnabledValue)
	} else {
		payload = append(payload, typingIndicatorsDisabledValue)
	}

	return b.applyAndBroadcastUpdateDM(updateDM{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    xor(userID, b.currentUserID()),
		Timestamp: time.Now().Unix(),
		Type:      updateDMTypeSetTypingIndicators,
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
	b.broadcast(&ud)

	return nil
}
