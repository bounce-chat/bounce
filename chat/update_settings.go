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
)

var updateSettingsMutex sync.Mutex

const disabled = 0x00
const enabled = 0x01

const updateSettingsTypeSetDefaultGroupRetention = uint16(0)
const updateSettingsTypeSetDefatulReadReceipts = uint16(1)
const updateSettingsTypeSetDefaultTypingIndicators = uint16(2)
const updateSettingsTypeSetNewGroupRestrictUserManagement = uint16(3)
const updateSettingsTypeSetNewGroupRestirctGroupEdits = uint16(4)
const updateSettingsTypeSewNewGroupRestrictPosting = uint16(5)

var errInvalidPayloadValue = errors.New("invalid payload value")

type updateSettings struct {
	ID           uuid.UUID `gorm:"type:uuid;primary_key;"`
	Type         uint16
	Data         []byte
	Timestamp    int64
	payload      []byte
	payloadMutex sync.Mutex
}

func (us *updateSettings) BeforeCreate(tx *gorm.DB) error {
	if us.ID == uuid.Nil {
		return errors.New("update settings ID must be set before creation")
	}

	return nil
}

func (us *updateSettings) AfterDelete(tx *gorm.DB) error {
	return tx.Where("frame_id = ? AND frame_type = ?", us.ID, typeUpdateSettings).Delete(&deliveryRecord{}).Error
}

func (us *updateSettings) getID() uuid.UUID {
	return us.ID
}

func (us *updateSettings) getScope(_ uuid.UUID) int {
	return scopeSync
}

func (us *updateSettings) getDestination(myID uuid.UUID) uuid.UUID {
	return myID
}

func (us *updateSettings) getType() uint16 {
	return typeUpdateSettings
}

func (us *updateSettings) getPayload() []byte {
	us.payloadMutex.Lock()
	defer us.payloadMutex.Unlock()

	if len(us.payload) == 0 {
		bytes, err := msgpack.Marshal(us)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal update dm settings")
		}
		us.payload = bytes
	}
	return us.payload
}

func (us *updateSettings) getAuthor() uuid.UUID {
	return uuid.Nil // TODO us.Actor
}

func (us *updateSettings) getTimestamp() int64 {
	return us.Timestamp
}

func (us *updateSettings) validPayload() error {
	switch us.Type {
	case updateSettingsTypeSetDefaultGroupRetention:
		//
	case updateSettingsTypeSetDefatulReadReceipts:
		if len(us.Data) != 1 {
			return errInvalidPayloadLength
		}
		if !(us.Data[0] == enabled || us.Data[0] == disabled) {
			return errInvalidPayloadValue
		}
	case updateSettingsTypeSetDefaultTypingIndicators:
		if len(us.Data) != 1 {
			return errInvalidPayloadLength
		}
		if !(us.Data[0] == enabled || us.Data[0] == disabled) {
			return errInvalidPayloadValue
		}
	case updateSettingsTypeSetNewGroupRestrictUserManagement:
		if len(us.Data) != 1 {
			return errInvalidPayloadLength
		}
		if !(us.Data[0] == enabled || us.Data[0] == disabled) {
			return errInvalidPayloadValue
		}
	case updateSettingsTypeSetNewGroupRestirctGroupEdits:
		if len(us.Data) != 1 {
			return errInvalidPayloadLength
		}
		if !(us.Data[0] == enabled || us.Data[0] == disabled) {
			return errInvalidPayloadValue
		}
	case updateSettingsTypeSewNewGroupRestrictPosting:
		if len(us.Data) != 1 {
			return errInvalidPayloadLength
		}
		if !(us.Data[0] == enabled || us.Data[0] == disabled) {
			return errInvalidPayloadValue
		}
	default:
		log.WithFields(log.Fields{
			"type": us.Type,
		}).Warn("update settings with unknown type")
	}
	return nil
}

func (b *bounce) handleUpdateSettings(peer string, payload []byte, catchUp bool) broadcastable {
	updateSettingsMutex.Lock()
	defer updateSettingsMutex.Unlock()

	// Unmarshall it
	var us updateSettings
	err := msgpack.Unmarshal(payload, &us)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling update ettings")
		return nil
	}

	// Make sure this came from a sync device
	srcDevice, exists := b.getDeviceFromAddress(peer)
	if !exists || !(srcDevice.UserID == b.currentUserID()) {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("rejecting update settings from out of scope device")
		return nil
	}

	// If we already have this update, we just mark that this peer has it too, ack it, and return
	var existingUS updateSettings
	err = b.database.Where("id = ?", us.ID).First(&existingUS).Error
	if err == nil {
		return &existingUS
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up update settings")
	}

	// Apply this update locally
	err = b.saveAndApplyUpdateSettings(us)
	if err != nil {
		log.WithFields(log.Fields{
			"device": srcDevice.ID,
			"type":   us.Type,
			"error":  err.Error(),
		}).Error("error applying update DM")
		return nil
	}

	return &us
}

func (b *bounce) saveAndApplyUpdateSettings(us updateSettings) error {
	// Validate payload
	err := us.validPayload()
	if err != nil {
		return err
	}

	// Save the update
	err = b.database.Create(&us).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving update settings")
	}

	// Update the database and UI
	b.updateSettingsState()

	return nil
}

func (b *bounce) updateSettingsState() {
	// Set initial values
	defaultGroupRetention := int64(time.Duration(24 * time.Hour * 7 * 4).Seconds())
	defaultSendReadReceipts := true
	defaultSendTypingIndicators := true
	newGroupRestrictUserManagement := true
	newGroupRestrictGroupEdits := false
	newGroupRestrictPosting := false

	// Find all updates
	uss := []updateSettings{}
	err := b.database.Order("timestamp asc").Find(&uss).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up update settings")
	}

	// Update the DM fields in the orders of the updates
	for _, us := range uss {
		err := us.validPayload()
		if err != nil {
			log.WithFields(log.Fields{
				"id":    us.ID,
				"error": err.Error(),
			}).Error("skipping update settings with invalid payload")
			continue
		}

		switch us.Type {
		case updateSettingsTypeSetDefaultGroupRetention:
			defaultGroupRetention = int64(binary.LittleEndian.Uint64(us.Data))
		case updateSettingsTypeSetDefatulReadReceipts:
			defaultSendReadReceipts = us.Data[0] == enabled
		case updateSettingsTypeSetDefaultTypingIndicators:
			defaultSendTypingIndicators = us.Data[0] == enabled
		case updateSettingsTypeSetNewGroupRestrictUserManagement:
			newGroupRestrictUserManagement = us.Data[0] == enabled
		case updateSettingsTypeSetNewGroupRestirctGroupEdits:
			newGroupRestrictGroupEdits = us.Data[0] == enabled
		case updateSettingsTypeSewNewGroupRestrictPosting:
			newGroupRestrictPosting = us.Data[0] == enabled
		default:
			log.WithFields(log.Fields{
				"type": us.Type,
			}).Warn("ignoring update settings with unknown type")
		}
	}

	// Set the values in the database
	err = b.database.Table("profile_settings").Where("user_id = ?", b.currentUserID()).Updates(map[string]interface{}{
		"default_group_retention":            defaultGroupRetention,
		"default_send_read_receipts":         defaultSendReadReceipts,
		"default_send_typing_indicators":     defaultSendTypingIndicators,
		"new_group_restrict_user_management": newGroupRestrictUserManagement,
		"new_group_restrict_group_edits":     newGroupRestrictGroupEdits,
		"new_group_restrict_posting":         newGroupRestrictPosting,
	}).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("user not found when updating settings fields")
			return
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error updating settings fields")
		}
	}

	// Inform the UI of the current state
	b.userInterface.SetSettings(
		Settings{
			DefaultGroupRetention:          defaultGroupRetention,
			DefaultSendReadReceipts:        defaultSendReadReceipts,
			DefaultSendTypingIndicators:    defaultSendTypingIndicators,
			NewGroupRestrictUserManagement: newGroupRestrictUserManagement,
			NewGroupRestrictGroupEdits:     newGroupRestrictGroupEdits,
			NewGroupRestrictPosting:        newGroupRestrictPosting,
		},
	)
}

func (b *bounce) setNewGroupRetention(value int64) {
	payload := make([]byte, 8)
	binary.LittleEndian.PutUint64(payload, uint64(value))

	b.applyAndBroadcastUpdateSettings(updateSettings{
		ID:        uuid.New(),
		Timestamp: time.Now().Unix(),
		Type:      updateSettingsTypeSetDefaultGroupRetention,
		Data:      payload,
	})
}

func (b *bounce) setReadReceiptsByDefault(value bool) {
	payload := []byte{}
	if value {
		payload = append(payload, enabled)
	} else {
		payload = append(payload, disabled)
	}

	b.applyAndBroadcastUpdateSettings(updateSettings{
		ID:        uuid.New(),
		Timestamp: time.Now().Unix(),
		Type:      updateSettingsTypeSetDefatulReadReceipts,
		Data:      payload,
	})
}

func (b *bounce) setTypingIndicatorsByDefault(value bool) {
	payload := []byte{}
	if value {
		payload = append(payload, enabled)
	} else {
		payload = append(payload, disabled)
	}

	b.applyAndBroadcastUpdateSettings(updateSettings{
		ID:        uuid.New(),
		Timestamp: time.Now().Unix(),
		Type:      updateSettingsTypeSetDefaultTypingIndicators,
		Data:      payload,
	})
}

func (b *bounce) setNewGroupRestrictUserManagement(value bool) {
	payload := []byte{}
	if value {
		payload = append(payload, enabled)
	} else {
		payload = append(payload, disabled)
	}

	b.applyAndBroadcastUpdateSettings(updateSettings{
		ID:        uuid.New(),
		Timestamp: time.Now().Unix(),
		Type:      updateSettingsTypeSetNewGroupRestrictUserManagement,
		Data:      payload,
	})
}

func (b *bounce) setNewGroupRestrictGroupEdits(value bool) {
	payload := []byte{}
	if value {
		payload = append(payload, enabled)
	} else {
		payload = append(payload, disabled)
	}

	b.applyAndBroadcastUpdateSettings(updateSettings{
		ID:        uuid.New(),
		Timestamp: time.Now().Unix(),
		Type:      updateSettingsTypeSetNewGroupRestirctGroupEdits,
		Data:      payload,
	})
}

func (b *bounce) setNewGroupRestrictPosting(value bool) {
	payload := []byte{}
	if value {
		payload = append(payload, enabled)
	} else {
		payload = append(payload, disabled)
	}

	b.applyAndBroadcastUpdateSettings(updateSettings{
		ID:        uuid.New(),
		Timestamp: time.Now().Unix(),
		Type:      updateSettingsTypeSewNewGroupRestrictPosting,
		Data:      payload,
	})
}

func (b *bounce) applyAndBroadcastUpdateSettings(us updateSettings) error {
	// Apply the update locally
	err := b.saveAndApplyUpdateSettings(us)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error applying update settings")
		return err
	}

	// Broadcast
	b.broadcast(&us)

	return nil
}
