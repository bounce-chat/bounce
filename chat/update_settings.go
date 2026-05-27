package chat

import (
	"encoding/binary"
	"errors"
	"sync"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var updateSettingsMutex sync.Mutex

const disabled = 0x00
const enabled = 0x01

const updateSettingsTypeSetDefaultGroupRetention = uint16(0)
const updateSettingsTypeSetDefatulReadReceipts = uint16(1)
const updateSettingsTypeSetDefaultTypingIndicators = uint16(2)
const updateSettingsTypeSetNewGroupRestrictUserManagement = uint16(3)
const updateSettingsTypeSetNewGroupRestirctGroupEdits = uint16(4)
const updateSettingsTypeSetNewGroupRestrictPosting = uint16(5)
const updateSettingsTypeSetAutoJoinGroups = uint16(6)
const updateSettingsTypeSetDefaultDMRetention = uint16(7)

var errInvalidPayloadValue = errors.New("invalid payload value")

type updateSettings struct {
	SignedFrame
	cachedEncoding
	ID        uuid.UUID `gorm:"type:uuid;primary_key;"`
	Type      uint16
	Data      []byte
	Timestamp int64
	SavedAt   int64 `msgpack:"-"`
}

func (us *updateSettings) BeforeCreate(tx *gorm.DB) error {
	if us.ID == uuid.Nil {
		return errors.New("update settings ID must be set before creation")
	}
	us.SavedAt = time.Now().Unix()

	return nil
}

func (us *updateSettings) AfterDelete(tx *gorm.DB) error {
	if us.ID == uuid.Nil {
		return nil
	}
	return tx.Clauses(clause.Returning{}).Where("frame_id = ? AND frame_type = ?", us.ID, typeUpdateSettings).Delete(&deliveryRecord{}).Error
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
	if len(us.payload) == 0 {
		bytes, err := msgpack.Marshal(signedContainer{
			Payload:   us.OriginalPayload,
			Signature: us.Signature,
			Signer:    us.Signer,
		})
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error marshalling update settings' signed container")
		}
		us.payload = bytes
	}
	return us.payload
}

func (us *updateSettings) getAuthor() uuid.UUID {
	// Author isn't needed for these and it can only ever be us
	return uuid.Nil
}

func (us *updateSettings) getTimestamp() int64 {
	return us.Timestamp
}

func (us *updateSettings) getSavedAt() int64 {
	return us.SavedAt
}

func (us *updateSettings) validPayload() error {
	switch us.Type {
	case updateSettingsTypeSetDefaultGroupRetention:
		if len(us.Data) != 8 {
			return errInvalidPayloadLength
		}
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
	case updateSettingsTypeSetNewGroupRestrictPosting:
		if len(us.Data) != 1 {
			return errInvalidPayloadLength
		}
		if !(us.Data[0] == enabled || us.Data[0] == disabled) {
			return errInvalidPayloadValue
		}
	case updateSettingsTypeSetAutoJoinGroups:
		if len(us.Data) != 1 {
			return errInvalidPayloadLength
		}
		val := int(us.Data[0])
		if !(val == OnlyAutoJoinGroupsWithNoNewUsers || val == NeverAutoJoinGroups || val == AlwaysAutoJoinGroups) {
			return errInvalidPayloadValue
		}
	case updateSettingsTypeSetDefaultDMRetention:
		if len(us.Data) != 8 {
			return errInvalidPayloadLength
		}
	default:
		log.WithFields(log.Fields{
			"type": us.Type,
		}).Warn("update settings with unknown type")
	}
	return nil
}

func (b *Bounce) handleUpdateSettings(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	updateSettingsMutex.Lock()
	defer updateSettingsMutex.Unlock()

	// Verify the signature
	sc, err := b.unpackSignedContainer(payload)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unpacking signed container for group message")
		return nil, false
	}
	var us updateSettings
	err = msgpack.Unmarshal(sc.Payload, &us)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling update settings")
		return nil, false
	}
	us.OriginalPayload = sc.Payload
	us.Signature = sc.Signature
	us.Signer = sc.Signer

	// Make sure the signing device was not revoked before creating this
	var signerDevice device
	err = b.database.Select("revoked_at").Where("address = ?", us.Signer).First(&signerDevice).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"address": us.Signer,
			}).Error("signer device not found for update settings")
			return nil, false
		} else {
			log.WithFields(log.Fields{
				"address": us.Signer,
				"error":   err.Error(),
			}).Fatal("database error looking up signing device")
		}
	}
	if signerDevice.RevokedAt != 0 && signerDevice.RevokedAt < us.Timestamp {
		log.WithFields(log.Fields{
			"id":     us.ID,
			"signer": us.Signer,
		}).Warn("ignoring update settings signed by revoked device")
		go b.sendAck(peer, typeUpdateSettings, us.ID)
		return nil, false
	}

	// Make sure the device that signed this message belongs to the author
	if !b.signedByUser(sc, b.currentUserID()) {
		log.WithFields(log.Fields{
			"id":     us.ID,
			"signer": us.Signer,
		}).Warn("received update settings signed by a different user than the author, ignoring")
		return nil, false
	}

	// If we already have this update, we just mark that this peer has it too, ack it, and return
	var existingUS updateSettings
	err = b.database.Where("id = ?", us.ID).First(&existingUS).Error
	if err == nil {
		return &existingUS, false
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up update settings")
	}

	// Apply this update locally
	err = b.saveUpdateSettings(us)
	if err != nil {
		log.WithFields(log.Fields{
			"type":  us.Type,
			"error": err.Error(),
		}).Error("error applying update DM")
		return nil, false
	}

	// Update the database state if we're not in a catch up
	if !catchUp {
		b.updateSettingsState()
	}

	return &us, true
}

func (b *Bounce) saveUpdateSettings(us updateSettings) error {
	// Validate payload
	err := us.validPayload()
	if err != nil {
		return err
	}

	// Sign it
	us.OriginalPayload, err = msgpack.Marshal(&us)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling update settings")
	}
	sc := b.createSignedContainer(us.OriginalPayload)
	us.Signature = sc.Signature
	us.Signer = sc.Signer

	// Save the update
	err = b.database.Create(&us).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving update settings")
	}

	return nil
}

func (b *Bounce) updateSettingsState() {
	// Set initial values
	defaultGroupRetention := int64(time.Duration(24 * time.Hour * 7 * 4).Seconds())
	defaultSendReadReceipts := true
	defaultSendTypingIndicators := true
	newGroupRestrictUserManagement := true
	newGroupRestrictGroupEdits := false
	newGroupRestrictPosting := false
	autoJoinGroups := 0
	defaultDMRetention := int64(time.Duration(24 * time.Hour * 7 * 4).Seconds())

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
		if b.deviceWasRevokedAt(us.Signer, us.Timestamp) {
			continue
		}

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
		case updateSettingsTypeSetNewGroupRestrictPosting:
			newGroupRestrictPosting = us.Data[0] == enabled
		case updateSettingsTypeSetAutoJoinGroups:
			autoJoinGroups = int(us.Data[0])
		case updateSettingsTypeSetDefaultDMRetention:
			defaultDMRetention = int64(binary.LittleEndian.Uint64(us.Data))
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
		"auto_join_groups":                   autoJoinGroups,
		"default_dm_retention":               defaultDMRetention,
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
	go b.ui.SetSettings(
		Settings{
			DefaultGroupRetention:          defaultGroupRetention,
			DefaultSendReadReceipts:        defaultSendReadReceipts,
			DefaultSendTypingIndicators:    defaultSendTypingIndicators,
			NewGroupRestrictUserManagement: newGroupRestrictUserManagement,
			NewGroupRestrictGroupEdits:     newGroupRestrictGroupEdits,
			NewGroupRestrictPosting:        newGroupRestrictPosting,
			AutoJoinGroups:                 autoJoinGroups,
			DefaultDMRetention:             defaultDMRetention,
		},
	)
}

func (b *Bounce) SetNewGroupRetention(value int64) {
	payload := make([]byte, 8)
	binary.LittleEndian.PutUint64(payload, uint64(value))

	b.applyAndBroadcastUpdateSettings(updateSettings{
		ID:        uuid.New(),
		Timestamp: time.Now().Unix(),
		Type:      updateSettingsTypeSetDefaultGroupRetention,
		Data:      payload,
	})
}

func (b *Bounce) SetReadReceiptsByDefault(value bool) {
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

func (b *Bounce) SetTypingIndicatorsByDefault(value bool) {
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

func (b *Bounce) SetNewGroupRestrictUserManagement(value bool) {
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

func (b *Bounce) SetNewGroupRestrictGroupEdits(value bool) {
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

func (b *Bounce) SetNewGroupRestrictPosting(value bool) {
	payload := []byte{}
	if value {
		payload = append(payload, enabled)
	} else {
		payload = append(payload, disabled)
	}

	b.applyAndBroadcastUpdateSettings(updateSettings{
		ID:        uuid.New(),
		Timestamp: time.Now().Unix(),
		Type:      updateSettingsTypeSetNewGroupRestrictPosting,
		Data:      payload,
	})
}

func (b *Bounce) SetAutoJoinGroups(value int) {
	b.applyAndBroadcastUpdateSettings(updateSettings{
		ID:        uuid.New(),
		Timestamp: time.Now().Unix(),
		Type:      updateSettingsTypeSetAutoJoinGroups,
		Data:      []byte{byte(value)},
	})
}

func (b *Bounce) SetNewDMRetention(value int64) {
	payload := make([]byte, 8)
	binary.LittleEndian.PutUint64(payload, uint64(value))

	b.applyAndBroadcastUpdateSettings(updateSettings{
		ID:        uuid.New(),
		Timestamp: time.Now().Unix(),
		Type:      updateSettingsTypeSetDefaultDMRetention,
		Data:      payload,
	})
}

func (b *Bounce) applyAndBroadcastUpdateSettings(us updateSettings) error {
	// Save the update to the database
	err := b.saveUpdateSettings(us)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error applying update settings")
		return err
	}

	// Set the database and UI states
	b.updateSettingsState()

	// Broadcast
	b.broadcast(&us)

	return nil
}
