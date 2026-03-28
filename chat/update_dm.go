package chat

import (
	"encoding/binary"
	"errors"
	"strings"
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
const updateDMTypeSetOpen = uint16(5)
const updateDMTypeSetAlias = uint16(6)
const updateDMTypeSetNotes = uint16(7)
const updateDMTypeSetBlocked = uint16(8)
const updateDMTypeOfferRetention = uint16(9)

var errUpdateDMWithUnknownType = errors.New("update DM has unknown update type")
var errInvalidPayloadLength = errors.New("invalid payload length")
var errInvalidOverriddenValue = errors.New("invalid value for overridden byte")
var errInvalidEnabledValue = errors.New("invalid value for enabled byte")
var errSyncScopedMessageFromNonSyncSource = errors.New("sync scoped frame can only come from sync device")
var errInvalidSetOpenValue = errors.New("invalid value for setting DM open state")
var errInvalidSetBlockedValue = errors.New("invalid value for setting user blocked state")
var errCannotBlockSelf = errors.New("cannot block or unblock self")

const readReceiptsDefaultValue = 0x00
const readReceiptsOverriddenValue = 0x01
const readReceiptsEnabledValue = 0x00
const readReceiptsDisabledValue = 0x01
const typingIndicatorsDefaultValue = 0x00
const typingIndicatorsOverriddenValue = 0x01
const typingIndicatorsEnabledValue = 0x00
const typingIndicatorsDisabledValue = 0x01
const dmClosed = 0x00
const dmOpen = 0x01
const userNotBlocked = 0x00
const userBlocked = 0x01

var updateDMMutex sync.Mutex

// An updateDM frame changes the settings of a direct message thread, such as retention or notification settings.
// Some settings, like retention, must be observed by both participants of the DM, where others like notification
// settings are only sent to sync devices.  The data field of the structure contains different data depending on
// the type of update.
type updateDM struct {
	SignedFrame
	cachedEncoding
	ID        uuid.UUID `gorm:"type:uuid;primary_key;"`
	Actor     uuid.UUID
	Target    uuid.UUID // XOR of two users in the DM
	Timestamp int64
	Seen      bool `msgpack:"-"`
	Type      uint16
	Data      []byte
}

func (ud *updateDM) BeforeCreate(tx *gorm.DB) error {
	if ud.ID == uuid.Nil {
		return errors.New("update DM ID must be set before creation")
	}

	return nil
}

func (ud *updateDM) AfterDelete(tx *gorm.DB) error {
	if ud.ID == uuid.Nil {
		return nil
	}
	return tx.Clauses(clause.Returning{}).Where("frame_id = ? AND frame_type = ?", ud.ID, typeUpdateDM).Delete(&deliveryRecord{}).Error
}

func (ud *updateDM) getID() uuid.UUID {
	return ud.ID
}

func (ud *updateDM) getScope(_ uuid.UUID) int {
	if ud.Target != uuid.Nil && (ud.Type == updateDMTypeChangeRetention || ud.Type == updateDMTypeSetClearBefore || ud.Type == updateDMTypeOfferRetention) {
		return scopeUser
	}

	return scopeSync
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
		bytes, err := msgpack.Marshal(signedContainer{
			Payload:   ud.OriginalPayload,
			Signature: ud.Signature,
			Signer:    ud.Signer,
		})
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error marshalling update direct message's signed container")
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
	case updateDMTypeSetOpen:
		if len(ud.Data) != 1 {
			return errInvalidPayloadLength
		}
		if !(ud.Data[0] == dmOpen || ud.Data[0] == dmClosed) {
			return errInvalidSetOpenValue
		}
	case updateDMTypeSetAlias:
		if !(validUserName(string(ud.Data)) || string(ud.Data) == "") {
			return errInvalidUserName
		}
	case updateDMTypeSetBlocked:
		if len(ud.Data) != 1 {
			return errInvalidPayloadLength
		}
		if !(ud.Data[0] == userNotBlocked || ud.Data[0] == userBlocked) {
			return errInvalidSetBlockedValue
		}
		if ud.Target == uuid.Nil {
			return errCannotBlockSelf
		}
	}

	return nil
}

func (b *Bounce) handleUpdateDM(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	updateDMMutex.Lock()
	defer updateDMMutex.Unlock()

	// Verify the signature
	sc, err := b.unpackSignedContainer(payload)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unpacking signed container for group message")
		return nil, false
	}
	var ud updateDM
	err = msgpack.Unmarshal(sc.Payload, &ud)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling update dm")
		return nil, false
	}
	ud.OriginalPayload = sc.Payload
	ud.Signature = sc.Signature
	ud.Signer = sc.Signer

	// Make sure the signing device was not revoked before creating this
	var signerDevice device
	err = b.database.Select("revoked_at").Where("address = ?", ud.Signer).First(&signerDevice).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"address": ud.Signer,
			}).Error("signer device not found for update device")
			return nil, false
		} else {
			log.WithFields(log.Fields{
				"address": ud.Signer,
				"error":   err.Error(),
			}).Fatal("database error looking up signing device")
		}
	}
	if signerDevice.RevokedAt != 0 && signerDevice.RevokedAt < ud.Timestamp {
		log.WithFields(log.Fields{
			"id":     ud.ID,
			"signer": ud.Signer,
		}).Warn("ignoring direct message signed by revoked device")
		go b.sendAck(peer, typeUpdateDM, ud.ID)
		return nil, false
	}

	// Make sure the device that signed this message belongs to the author
	if !b.signedByUser(sc, ud.Actor) {
		log.WithFields(log.Fields{
			"id":     ud.ID,
			"signer": sc.Signer,
			"author": ud.Actor,
		}).Warn("received update DM signed by a different user than the author, ignoring")
		return nil, false
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
			return nil, false
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up user for DM settings update")
		}
	}

	// If we already have this update, we just mark that this peer has it too, ack it, and return
	var existingUD updateDM
	err = b.database.Where("id = ?", ud.ID).First(&existingUD).Error
	if err == nil {
		return &existingUD, false
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up update DM")
	}

	// Apply this update locally
	err = b.saveAndDisplayUpdateDM(&ud)
	if err != nil {
		log.WithFields(log.Fields{
			"user":  xor(ud.Target, b.currentUserID()),
			"type":  ud.Type,
			"error": err.Error(),
		}).Error("error applying update DM")
		return nil, false
	}

	// If we're not in a catchup, set the state now
	if !catchUp || ud.Type == updateDMTypeSetBlocked {
		b.updateDMState(xor(ud.Target, b.currentUserID()))
	}

	return &ud, true
}

func (b *Bounce) updateDMState(userID uuid.UUID) {
	var u user
	err := b.database.First(&u, "id = ?", userID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"user_id": userID,
				"error":   err.Error(),
			}).Error("user not found when updating DM state")
			return
		} else {
			log.WithFields(log.Fields{
				"user_id": userID,
				"error":   err.Error(),
			}).Fatal("database error looking up user")
		}
	}

	profile, ok := b.currentUser()
	if !ok {
		log.Fatal("cannot set DM state before profile exists")
	}

	// Set the initial values for the DM
	retention := profile.ProfileSettings.DefaultDMRetention
	if userID == b.currentUserID() {
		retention = int64(0) // Note to self lasts forever by default
	}
	mutedUntil := int64(0)
	clearBefore := int64(0)
	readReceiptsOverridden := false
	readReceiptsEnabled := true
	typingIndicatorsOverridden := false
	typingIndicatorsEnabled := true
	open := b.dmOpenByDefault(userID)
	alias := ""
	notes := ""
	blocked := false

	// Track states related to setting retention for the first time
	anyoneEverSetRetention := false
	profileOfferedRetention := false
	profileDefaultRetention := int64(0)
	counterpartyOfferedRetention := false
	counterpartyDefaultRetention := int64(0)

	// Find all updates
	uds := []updateDM{}
	err = b.database.Where("target =  ?", xor(userID, b.currentUserID())).Order("timestamp asc").Find(&uds).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up updateDMs")
	}

	// Update the DM fields in the orders of the updates
	for _, ud := range uds {
		if b.deviceWasRevokedAt(ud.Signer, ud.Timestamp) {
			continue
		}

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
			anyoneEverSetRetention = true
		case updateDMTypeSetClearBefore:
			clearBefore = int64(binary.LittleEndian.Uint64(ud.Data))
		case updateDMTypeSetReadReceipts:
			readReceiptsOverridden = ud.Data[0] == readReceiptsOverriddenValue
			readReceiptsEnabled = ud.Data[1] == readReceiptsEnabledValue
		case updateDMTypeSetTypingIndicators:
			typingIndicatorsOverridden = ud.Data[0] == typingIndicatorsOverriddenValue
			typingIndicatorsEnabled = ud.Data[1] == typingIndicatorsEnabledValue
		case updateDMTypeSetOpen:
			open = ud.Data[0] == dmOpen
		case updateDMTypeSetAlias:
			alias = string(ud.Data)
		case updateDMTypeSetNotes:
			notes = string(ud.Data)
		case updateDMTypeSetBlocked:
			blocked = ud.Data[0] == userBlocked
			open = !blocked
		case updateDMTypeOfferRetention:
			if ud.Actor == profile.ID {
				profileOfferedRetention = true
				profileDefaultRetention = int64(binary.LittleEndian.Uint64(ud.Data))
			} else {
				counterpartyOfferedRetention = true
				counterpartyDefaultRetention = int64(binary.LittleEndian.Uint64(ud.Data))
			}
		default:
			log.WithFields(log.Fields{
				"type": ud.Type,
			}).Warn("ignoring update DM with unknown type")
		}
	}

	// If retention has never been set, advertize our default retention and see if one needs to be set
	bothSharedSameDefault := profileOfferedRetention && counterpartyOfferedRetention && profileDefaultRetention == counterpartyDefaultRetention
	if !anyoneEverSetRetention && userID != b.currentUserID() {
		if bothSharedSameDefault {
			retention = profileDefaultRetention
		} else {
			if counterpartyOfferedRetention {
				if profile.ProfileSettings.DefaultDMRetention != 0 && profile.ProfileSettings.DefaultDMRetention < counterpartyDefaultRetention {
					// Set the retention to my default
					payload := make([]byte, 8)
					binary.LittleEndian.PutUint64(payload, uint64(profile.ProfileSettings.DefaultDMRetention))

					set := &updateDM{
						ID:        uuid.New(),
						Actor:     b.currentUserID(),
						Target:    xor(userID, b.currentUserID()),
						Timestamp: time.Now().Unix(),
						Type:      updateDMTypeChangeRetention,
						Data:      payload,
					}

					set.OriginalPayload, err = msgpack.Marshal(&set)
					if err != nil {
						log.WithFields(log.Fields{
							"error": err.Error(),
						}).Fatal("error marshalling update DM")
					}
					sc := b.createSignedContainer(set.OriginalPayload)
					set.Signature = sc.Signature
					set.Signer = sc.Signer

					err = b.database.Create(set).Error
					if err != nil {
						log.WithFields(log.Fields{
							"error": err.Error(),
						}).Fatal("database error saving update DM")
					}

					b.broadcast(set)
					b.updateDMState(userID)
					b.informUIUpdateDMChangeRetention(u, set)

					return
				}
			}

			if !profileOfferedRetention {
				// Offer my retention
				payload := make([]byte, 8)
				binary.LittleEndian.PutUint64(payload, uint64(profile.ProfileSettings.DefaultDMRetention))

				offer := &updateDM{
					ID:        uuid.New(),
					Actor:     b.currentUserID(),
					Target:    xor(userID, b.currentUserID()),
					Timestamp: time.Now().Unix(),
					Type:      updateDMTypeOfferRetention,
					Data:      payload,
				}

				offer.OriginalPayload, err = msgpack.Marshal(offer)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Fatal("error marshalling update DM")
				}
				sc := b.createSignedContainer(offer.OriginalPayload)
				offer.Signature = sc.Signature
				offer.Signer = sc.Signer

				err = b.database.Create(offer).Error
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Fatal("database error saving update DM")
				}

				b.broadcast(offer)
			}
		}
	}

	// Set the values in the database
	err = b.database.Model(&user{}).Where("id = ?", userID).Updates(map[string]interface{}{
		"open_dm":                      open,
		"retention":                    retention,
		"muted_until":                  mutedUntil,
		"clear_before":                 clearBefore,
		"read_receipts_overridden":     readReceiptsOverridden,
		"read_receipts_enabled":        readReceiptsEnabled,
		"typing_indicators_overridden": typingIndicatorsOverridden,
		"typing_indicators_enabled":    typingIndicatorsEnabled,
		"alias":                        alias,
		"notes":                        notes,
		"blocked":                      blocked,
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

	if blocked {
		cacheBlockedUser(userID)
	} else {
		cacheUnblockedUser(userID)
	}

	// Inform the UI of the current state
	b.ui.SetDMState(
		userID,
		DMState{
			Open:                           open,
			Retention:                      retention,
			MutedUntil:                     mutedUntil,
			OverrideReadReceiptSetting:     readReceiptsOverridden,
			ReadReceiptsEnabled:            readReceiptsEnabled,
			OverrideTypingIndicatorSetting: typingIndicatorsOverridden,
			TypingIndicatorsEnabled:        typingIndicatorsEnabled,
		},
	)

	// Update the user in case the alias or notes changed
	b.ui.SetUserState(User{
		ID:               u.ID,
		Name:             u.Name,
		IntroductionTime: u.IntroductionTime,
		Images:           u.images(),
		Alias:            alias,
		Notes:            notes,
		Blocked:          blocked,
	})
}

func (b *Bounce) saveAndDisplayUpdateDM(ud *updateDM) error {
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

	ud.OriginalPayload, err = msgpack.Marshal(&ud)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling update DM")
	}
	sc := b.createSignedContainer(ud.OriginalPayload)
	ud.Signature = sc.Signature
	ud.Signer = sc.Signer

	// Save the update DM
	err = b.database.Create(ud).Error
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
		b.informUIUpdateDMChangeRetention(u, ud)
	case updateDMTypeSetClearBefore:
		b.informUIUpdateDMSetClearBefore(u, ud)
	case updateDMTypeSetReadReceipts:
		// No UI status changes for read receipt settings
	case updateDMTypeSetTypingIndicators:
		// No UI status changes for read receipt settings
	case updateDMTypeSetOpen:
		// No UI status changes for opening and closing DMs
	case updateDMTypeSetAlias:
		b.informUIUpdateDMSetAlias(u, ud)
	case updateDMTypeSetNotes:
		// No UI status changes for notes
	case updateDMTypeSetBlocked:
		// No UI status changes for blocking users
	case updateDMTypeOfferRetention:
		// No UI changes for determining retention
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

	return nil
}

func (b *Bounce) informUIUpdateDMChangeRetention(u user, ud *updateDM) {
	// Decode the new retention value
	retention := int64(binary.LittleEndian.Uint64(ud.Data))

	// Inform the UI
	b.ui.DMRetentionChanged(UpdateDMRetention{
		ID:        ud.ID,
		Thread:    u.ID,
		Actor:     ud.Actor,
		Timestamp: ud.Timestamp,
		Retention: retention,
	})
}

func (b *Bounce) informUIUpdateDMSetClearBefore(u user, ud *updateDM) {
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
		b.ui.DeleteItem(dm.ID)
	}
	b.ui.DMChatHistoryCleared(UpdateDMClearHistory{
		ID:        ud.ID,
		Thread:    u.ID,
		Actor:     ud.Actor,
		Timestamp: ud.Timestamp,
		ClearTime: clearBefore,
	})
}

func (b *Bounce) informUIUpdateDMSetAlias(u user, ud *updateDM) {
	b.ui.UserAliased(UpdateDMSetAlias{
		ID:        ud.ID,
		User:      ud.Target,
		Timestamp: ud.Timestamp,
		Alias:     string(ud.Data),
	})
}

func (b *Bounce) SetDMMutedUntil(userID uuid.UUID, mutedUntil int64) error {
	payload := make([]byte, 8)
	binary.LittleEndian.PutUint64(payload, uint64(mutedUntil))

	return b.applyAndBroadcastUpdateDM(&updateDM{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    xor(userID, b.currentUserID()),
		Timestamp: time.Now().Unix(),
		Type:      updateDMTypeChangeMutedUntil,
		Data:      payload,
	})
}

func (b *Bounce) SetDMRetention(userID uuid.UUID, retention int64) error {
	payload := make([]byte, 8)
	binary.LittleEndian.PutUint64(payload, uint64(retention))

	return b.applyAndBroadcastUpdateDM(&updateDM{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    xor(userID, b.currentUserID()),
		Timestamp: time.Now().Unix(),
		Type:      updateDMTypeChangeRetention,
		Data:      payload,
	})
}

func (b *Bounce) ClearDMChatHistory(userID uuid.UUID) error {
	payload := make([]byte, 8)
	binary.LittleEndian.PutUint64(payload, uint64(time.Now().Unix()))

	return b.applyAndBroadcastUpdateDM(&updateDM{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    xor(userID, b.currentUserID()),
		Timestamp: time.Now().Unix(),
		Type:      updateDMTypeSetClearBefore,
		Data:      payload,
	})
}

func (b *Bounce) SetDMReadReceiptSettings(userID uuid.UUID, override bool, enabled bool) error {
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

	return b.applyAndBroadcastUpdateDM(&updateDM{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    xor(userID, b.currentUserID()),
		Timestamp: time.Now().Unix(),
		Type:      updateDMTypeSetReadReceipts,
		Data:      payload,
	})
}

func (b *Bounce) SetDMTypingIndicatorSettings(userID uuid.UUID, override bool, enabled bool) error {
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

	return b.applyAndBroadcastUpdateDM(&updateDM{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    xor(userID, b.currentUserID()),
		Timestamp: time.Now().Unix(),
		Type:      updateDMTypeSetTypingIndicators,
		Data:      payload,
	})
}

func (b *Bounce) SetOpenDM(userID uuid.UUID, open bool) error {
	payload := []byte{}
	if open {
		payload = append(payload, dmOpen)
	} else {
		payload = append(payload, dmClosed)
	}

	return b.applyAndBroadcastUpdateDM(&updateDM{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    xor(userID, b.currentUserID()),
		Timestamp: time.Now().Unix(),
		Type:      updateDMTypeSetOpen,
		Data:      payload,
	})
}

func (b *Bounce) AliasUser(userID uuid.UUID, alias string) error {
	return b.applyAndBroadcastUpdateDM(&updateDM{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    xor(userID, b.currentUserID()),
		Timestamp: time.Now().Unix(),
		Type:      updateDMTypeSetAlias,
		Data:      []byte(strings.TrimSpace(alias)),
	})
}

func (b *Bounce) SetUserNotes(userID uuid.UUID, notes string) error {
	return b.applyAndBroadcastUpdateDM(&updateDM{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    xor(userID, b.currentUserID()),
		Timestamp: time.Now().Unix(),
		Type:      updateDMTypeSetNotes,
		Data:      []byte(notes),
	})
}

func (b *Bounce) BlockUser(userID uuid.UUID) error {
	if userID == b.currentUserID() {
		return errCannotBlockSelf
	}

	err := b.applyAndBroadcastUpdateDM(&updateDM{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    xor(userID, b.currentUserID()),
		Timestamp: time.Now().Unix(),
		Type:      updateDMTypeSetBlocked,
		Data:      []byte{userBlocked},
	})
	if err != nil {
		return err
	}

	b.updateConsensusForGroupsWithUser(userID)

	return nil
}

func (b *Bounce) UnblockUser(userID uuid.UUID) error {
	if userID == b.currentUserID() {
		return errCannotBlockSelf
	}

	return b.applyAndBroadcastUpdateDM(&updateDM{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    xor(userID, b.currentUserID()),
		Timestamp: time.Now().Unix(),
		Type:      updateDMTypeSetBlocked,
		Data:      []byte{userNotBlocked},
	})
}

func (b *Bounce) applyAndBroadcastUpdateDM(ud *updateDM) error {
	// Called in a goroutine since the UI can't be called back from main
	go func() {
		// Save and display the update the UI
		err := b.saveAndDisplayUpdateDM(ud)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error applying update DM")
			return
		}

		// Update the database
		b.updateDMState(xor(ud.Target, b.currentUserID()))

		// Broadcast
		b.broadcast(ud)
	}()

	return nil
}
