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

const updateGroupTypeChangeName = uint16(0)
const updateGroupTypeAddUser = uint16(1)
const updateGroupTypeRemoveUser = uint16(2)
const updateGroupTypeChangeMutedUntil = uint16(3)
const updateGroupTypeChangeRetention = uint16(4)
const updateGroupTypeSetClearBefore = uint16(5)
const updateGroupTypePromoteAdmin = uint16(6)
const updateGroupTypeDemoteAdmin = uint16(7)
const updateGroupTypeChangeUserManagementPermission = uint16(8)
const updateGroupTypeChangeGroupEditsPermission = uint16(9)
const updateGroupTypeChangePostingPermission = uint16(10)
const updateGroupTypeDelete = uint16(11)
const updateGroupTypeBlock = uint16(12)
const updateGroupTypeSetReadReceiptSettings = uint16(13)
const updateGroupTypeSetTypingIndicatorSettings = uint16(14)

const permissionUnrestricted = 0x00
const permissionRestricted = 0x01

var errUpdateGroupWithUnknownType = errors.New("update group has unknown update type")
var errInvalidGroupName = errors.New("invalid group name")
var errMutedUntilOnlyMutableBySelf = errors.New("group muted until settings can only be modified by current user")
var errReadReceiptOnlyMutableBySelf = errors.New("group read receipt settings can only be modified by current user")
var errTypingIndicatorOnlyMutableBySelf = errors.New("group typing indicator settings can only be modified by current user")
var errUserNotFound = errors.New("no user found with that ID")
var errUserHasInvalidDeviceGroup = errors.New("user has invalid device group")
var errNoPermissionToEditGroup = errors.New("user does not have permission to edit group")
var errNoPermissionToManageUsers = errors.New("user does not have permission to manage users")
var errCannotPromoteAdminNotInGroup = errors.New("cannot promote a user that is not a member of a group to admin")
var errInvalidPermissionPayloadLength = errors.New("permission payload must be one byte")
var errInvalidPermissionByte = errors.New("invalid permission byte")
var errUpdateNotApplied = errors.New("update could not be applied")
var errGroupNotFound = errors.New("group not found")
var errUserNotInGroup = errors.New("user is not in group")
var errAdminRequired = errors.New("this action can only be performed by admins")
var errCannotRemoveLastAdmin = errors.New("cannot remove the last admin from a group")
var errCannotDemoteAdminWhoDeletedGroup = errors.New("admins who deleted group cannot be demoted")
var errAlreadyDeleted = errors.New("group already deleted")

//
// An updateGroup frame changes the settings and status of a group, such as permissions, membership, retention, or notification settings.
// Some settings, like retention and membership, must be observed by all participants of the group, where others like notification are only
// sent to sync devices.  The data field of the structure contains different data depending on the type of update.
//
type updateGroup struct {
	ID              uuid.UUID `gorm:"type:uuid;primary_key;"`
	Actor           uuid.UUID
	Target          uuid.UUID
	Timestamp       int64
	Type            uint16
	Data            []byte
	CustomScope     uuid.UUID `msgpack:"-"`
	Confirmations   []confirmation
	Applied         bool   `msgpack:"-"`
	Read            bool   `msgpack:"-"`
	Signer          string `msgpack:"-" gorm:"not null"`
	OriginalPayload []byte `msgpack:"-" gorm:"not null"`
	Signature       []byte `msgpack:"-" gorm:"not null"`
	payload         []byte
	payloadMutex    sync.Mutex
}

func (ug *updateGroup) BeforeCreate(tx *gorm.DB) error {
	if ug.ID == uuid.Nil {
		return errors.New("update group ID must be set before creation")
	}

	ug.CustomScope = uuid.Nil

	return nil
}

func (ug *updateGroup) AfterDelete(tx *gorm.DB) error {
	if ug.CustomScope != uuid.Nil {
		err := tx.Where("id = ?", ug.CustomScope).Delete(&customScope{}).Error
		if err != nil {
			return err
		}
	}

	err := tx.Where("update_group_id = ?", ug.ID).Delete(&confirmation{}).Error
	if err != nil {
		return err
	}

	return tx.Where("frame_id = ? AND frame_type = ?", ug.ID, typeUpdateGroup).Delete(&deliveryRecord{}).Error
}

func (ug *updateGroup) getID() uuid.UUID {
	return ug.ID
}

func (ug *updateGroup) getScope(myID uuid.UUID) int {
	if ug.Type == updateGroupTypeChangeMutedUntil || ug.Type == updateGroupTypeSetReadReceiptSettings || ug.Type == updateGroupTypeSetTypingIndicatorSettings {
		return scopeSync
	}

	if ug.CustomScope != uuid.Nil {
		return scopeCustom
	}

	return scopeGroup
}

func (ug *updateGroup) getDestination(myID uuid.UUID) uuid.UUID {
	return ug.Target
}

func (ug *updateGroup) getType() uint16 {
	return typeUpdateGroup
}

func (ug *updateGroup) getPayload() []byte {
	ug.payloadMutex.Lock()
	defer ug.payloadMutex.Unlock()

	if len(ug.payload) == 0 {
		bytes, err := msgpack.Marshal(signedContainer{
			Payload:   ug.OriginalPayload,
			Signature: ug.Signature,
			Signer:    ug.Signer,
		})
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error marshalling update group's signed container")
		}
		ug.payload = bytes
	}
	return ug.payload
}

func (ug *updateGroup) getAuthor() uuid.UUID {
	return ug.Actor
}

func (ug *updateGroup) getTimestamp() int64 {
	return ug.Timestamp
}

func (ug *updateGroup) confirmingUsers(myID uuid.UUID) int {
	users := make(map[uuid.UUID]bool)
	for _, c := range ug.Confirmations {
		users[c.Author] = true
	}
	users[myID] = true

	return len(users)
}

func (ug *updateGroup) permissionPayloadIsRestricted() (bool, error) {
	if len(ug.Data) != 1 {
		return true, errInvalidPermissionPayloadLength
	}

	if ug.Data[0] == permissionRestricted {
		return true, nil
	} else if ug.Data[0] == permissionUnrestricted {
		return false, nil
	}

	return true, errInvalidPermissionByte
}

func (ug *updateGroup) validPayloadFormat() bool {
	switch ug.Type {
	case updateGroupTypeChangeName:
		return validGroupName(string(ug.Data))
	case updateGroupTypeAddUser:
		var u user
		err := msgpack.Unmarshal(ug.Data, &u)
		return err == nil
	case updateGroupTypeRemoveUser:
		_, err := uuid.FromBytes(ug.Data)
		return err == nil
	case updateGroupTypeChangeRetention:
		return true
	case updateGroupTypeChangeMutedUntil:
		return true
	case updateGroupTypeSetClearBefore:
		return true
	case updateGroupTypePromoteAdmin:
		_, err := uuid.FromBytes(ug.Data)
		return err == nil
	case updateGroupTypeDemoteAdmin:
		_, err := uuid.FromBytes(ug.Data)
		return err == nil
	case updateGroupTypeChangeUserManagementPermission:
		_, err := ug.permissionPayloadIsRestricted()
		return err == nil
	case updateGroupTypeChangeGroupEditsPermission:
		_, err := ug.permissionPayloadIsRestricted()
		return err == nil
	case updateGroupTypeChangePostingPermission:
		_, err := ug.permissionPayloadIsRestricted()
		return err == nil
	case updateGroupTypeDelete:
		return len(ug.Data) == 0
	case updateGroupTypeBlock:
		return len(ug.Data) == 0
	case updateGroupTypeSetReadReceiptSettings:
		if len(ug.Data) != 2 {
			return false
		}
		if !(ug.Data[0] == readReceiptsDefaultValue || ug.Data[0] == readReceiptsOverriddenValue) {
			return false
		}
		if !(ug.Data[1] == readReceiptsEnabledValue || ug.Data[1] == readReceiptsDisabledValue) {
			return false
		}
		return true
	case updateGroupTypeSetTypingIndicatorSettings:
		if len(ug.Data) != 2 {
			return false
		}
		if !(ug.Data[0] == typingIndicatorsDefaultValue || ug.Data[0] == typingIndicatorsOverriddenValue) {
			return false
		}
		if !(ug.Data[1] == typingIndicatorsEnabledValue || ug.Data[1] == typingIndicatorsDisabledValue) {
			return false
		}
		return true
	default:
		log.WithFields(log.Fields{
			"type": ug.Type,
		}).Warn("cannot validate payload format for update group with unknown type")
		return false
	}

	return false
}

func (b *bounce) handleUpdateGroup(peer string, payload []byte, catchUp bool) broadcastable {
	groupMutex.Lock()
	defer groupMutex.Unlock()

	// Unpack the signed container
	sc, err := b.unpackSignedContainer(payload)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unpacking signed container for update group")
		return nil
	}
	var ug updateGroup
	err = msgpack.Unmarshal(sc.Payload, &ug)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling update group")
		return nil
	}
	ug.OriginalPayload = sc.Payload
	ug.Signature = sc.Signature
	ug.Signer = sc.Signer

	// Ignore update groups for blocked groups
	for _, blockedGroup := range b.blockedGroups() {
		if ug.Target == blockedGroup {
			go b.sendAck(peer, typeUpdateGroup, ug.ID)
			return nil
		}
	}

	// Make sure that the user that created this signed container is the actor
	if !b.signedByUser(sc, ug.Actor) {
		log.WithFields(log.Fields{
			"peer":           peer,
			"actor":          ug.Actor,
			"signing_device": sc.Signer,
			"group":          ug.Target,
		}).Warn("ignoring group update that was not signed by the supposed actor")
		return nil
	}

	// Make sure the signing device was not revoked before creating this
	var signerDevice device
	err = b.database.Select("revoked_at").Where("address = ?", ug.Signer).First(&signerDevice).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"address": ug.Signer,
			}).Error("signer device not found for update group")
			return nil
		} else {
			log.WithFields(log.Fields{
				"address": ug.Signer,
				"error":   err.Error(),
			}).Fatal("database error looking up signing device")
		}
	}
	if signerDevice.RevokedAt != 0 && signerDevice.RevokedAt < ug.Timestamp {
		log.WithFields(log.Fields{
			"id":     ug.ID,
			"signer": ug.Signer,
		}).Warn("ignoring update group signed by revoked device")
		go b.sendAck(peer, typeUpdateGroup, ug.ID)
		return nil
	}

	// If we already have this update, we just mark that this peer has it too and return
	var existingUG updateGroup
	err = b.database.Where("id = ?", ug.ID).First(&existingUG).Error
	if err == nil {
		return &existingUG
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up update group")
	}

	// Make sure the payload of this update is valid for its type
	if !ug.validPayloadFormat() {
		log.WithFields(log.Fields{
			"id":   ug.ID,
			"peer": peer,
		}).Warn("ignoring update group with invalid data")
		return nil
	}

	// Save this update
	err = b.database.Create(&ug).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving update group")
	}

	if !catchUp {
		// Update the group state
		b.updateGroupConsensus(ug.Target)

		// Reload the update in case a custom scope was added
		err = b.database.First(&ug, "id = ?", ug.ID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"update_group_id": ug.ID,
					"error":           err.Error(),
				}).Error("update group not found after save in handler")
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error looking up update group")
			}
		}

		// If this is a remove user frame, make sure to send it to the devices of the user who was removed
		if ug.Type == updateGroupTypeRemoveUser || ug.Type == updateGroupTypeBlock {
			// Find the user ID that's being removed or is blocking the group
			userID := uuid.Nil
			switch ug.Type {
			case updateGroupTypeRemoveUser:
				userID, err = uuid.FromBytes(ug.Data)
				if err != nil {
					log.WithFields(log.Fields{
						"error":   err.Error(),
						"actor":   ug.Actor,
						"user_id": ug.Data,
					}).Error("update group attempted to remove user with invalid UUID")
					return nil
				}
			case updateGroupTypeBlock:
				userID = ug.Actor
			}

			// Broadcast directly to that user's devices since they are no longer in the group scope
			if userID != b.currentUserID() {
				var u user
				err := b.database.Preload(clause.Associations).Where("id = ?", userID).First(&u).Error
				if err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						log.WithFields(log.Fields{
							"user_id": userID,
						}).Error("user not found for direct remove from group broadcast")
						return nil
					} else {
						log.WithFields(log.Fields{
							"error": err.Error(),
						}).Fatal("error looking up user")
					}
				}

				for _, dev := range u.Devices {
					if dev.Address == peer {
						continue
					}
					rd := b.getRemoteDevice(dev.Address)
					if rd.connectedSockets > 0 {
						go b.sendDirect(dev.Address, &ug)
					}
				}
			}
		}
	}

	return &ug
}

func (b *bounce) renameGroup(groupID uuid.UUID, newName string) error {
	if !validGroupName(newName) {
		return errInvalidGroupName
	}

	return b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeChangeName,
		Data:      []byte(newName),
	})
}

func (b *bounce) setGroupMutedUntil(groupID uuid.UUID, mutedUntil int64) error {
	payload := make([]byte, 8)
	binary.LittleEndian.PutUint64(payload, uint64(mutedUntil))

	return b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeChangeMutedUntil,
		Data:      payload,
	})
}

func (b *bounce) setGroupRetention(groupID uuid.UUID, retention int64) error {
	payload := make([]byte, 8)
	binary.LittleEndian.PutUint64(payload, uint64(retention))

	return b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeChangeRetention,
		Data:      payload,
	})
}

func (b *bounce) clearGroupChatHistory(groupID uuid.UUID) error {
	payload := make([]byte, 8)
	binary.LittleEndian.PutUint64(payload, uint64(time.Now().Unix()))

	return b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeSetClearBefore,
		Data:      payload,
	})
}

func (b *bounce) addUser(groupID, userID uuid.UUID) error {
	// Look up the user to add with all associations
	var newUser user
	err := b.database.
		Preload("Devices.Signature").
		Preload(clause.Associations).
		Where("id = ?", userID).First(&newUser).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errUserNotFound
		} else {
			log.WithFields(log.Fields{
				"error":   err.Error(),
				"user_id": userID,
			}).Fatal("database error looking up user")
		}
	}

	// Create an update group that adds this user
	newUserBytes, err := msgpack.Marshal(newUser)
	if err != nil {
		log.WithFields(log.Fields{
			"user_id": newUser.ID,
			"error":   err.Error(),
		}).Fatal("error marshalling user while adding user to group")
	}
	err = b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeAddUser,
		Data:      newUserBytes,
	})
	if err != nil {
		return err
	}

	// Connect to this new user to send the new group
	b.userConnectionDesired(userID)

	// Do a reference flow with any devices we're currently connected to
	for _, dev := range newUser.Devices {
		go b.sendReferences(dev.Address)
	}

	return nil
}

func (b *bounce) removeUser(groupID, userID uuid.UUID) error {
	// Create an update group
	ug := &updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeRemoveUser,
		Data:      userID[:],
	}

	err := b.applyAndBroadcastUpdateGroup(ug)
	if err != nil {
		return err
	}

	// If we're not removing ourselves and are therefore using a group scope, since we're already removed the user from the group,
	// we need to directly broadcast this rfg to the user's online devices
	if userID != b.currentUserID() {
		var u user
		err := b.database.Preload(clause.Associations).Where("id = ?", userID).First(&u).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"user_id": userID,
				}).Error("user not found for direct remove from group broadcast")
				return err
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("error looking up user")
			}
		}

		for _, dev := range u.Devices {
			rd := b.getRemoteDevice(dev.Address)
			if rd.connectedSockets > 0 {
				go b.sendDirect(dev.Address, ug)
			}
		}
	}

	return nil
}

func (b *bounce) deleteGroup(groupID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeDelete,
	})
}

func (b *bounce) promoteAdmin(groupID, userID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypePromoteAdmin,
		Data:      userID[:],
	})
}

func (b *bounce) demoteAdmin(groupID, userID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeDemoteAdmin,
		Data:      userID[:],
	})
}

func (b *bounce) restrictUserManagement(groupID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeChangeUserManagementPermission,
		Data:      []byte{permissionRestricted},
	})
}

func (b *bounce) unrestrictUserManagement(groupID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeChangeUserManagementPermission,
		Data:      []byte{permissionUnrestricted},
	})
}

func (b *bounce) restrictGroupEdits(groupID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeChangeGroupEditsPermission,
		Data:      []byte{permissionRestricted},
	})
}

func (b *bounce) unrestrictGroupEdits(groupID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeChangeGroupEditsPermission,
		Data:      []byte{permissionUnrestricted},
	})
}

func (b *bounce) restrictPosting(groupID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeChangePostingPermission,
		Data:      []byte{permissionRestricted},
	})
}

func (b *bounce) unrestrictPosting(groupID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeChangePostingPermission,
		Data:      []byte{permissionUnrestricted},
	})
}

func (b *bounce) blockGroup(groupID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeBlock,
	})
}

func (b *bounce) setGroupReadReceiptSettings(groupID uuid.UUID, override bool, enabled bool) error {
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

	return b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeSetReadReceiptSettings,
		Data:      payload,
	})
}

func (b *bounce) setGroupTypingIndicatorSettings(groupID uuid.UUID, override bool, enabled bool) error {
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

	return b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeSetTypingIndicatorSettings,
		Data:      payload,
	})
}

func (b *bounce) applyAndBroadcastUpdateGroup(ug *updateGroup) error {
	// Find the group we're updating
	var g group
	err := b.database.Preload(clause.Associations).Where("id = ?", ug.Target).First(&g).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"group_id": ug.Target,
			}).Error("error looking up group for update")
			return errGroupNotFound
		} else {
			log.WithFields(log.Fields{
				"group_id": ug.Target,
			}).Fatal("database error looking up group")
		}
	}

	// Check to make sure we have permission to do this update right now
	if err = stateChangeAllowed(g.state(), *ug, b.currentUserID()); err != nil {
		return err
	}

	// Create the signed container for this update
	ug.OriginalPayload, err = msgpack.Marshal(ug)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling group update")
	}
	sc := b.createSignedContainer(ug.OriginalPayload)
	ug.Signature = sc.Signature
	ug.Signer = sc.Signer

	// Save this update
	err = b.database.Create(ug).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving update group")
	}

	// Update the group state
	b.updateGroupConsensus(ug.Target)

	// Check if this update was applied while evaluating group consensus and broadcast / ack if so
	err = b.database.First(ug, "id = ?", ug.ID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"update_group_id": ug.ID,
				"error":           err.Error(),
			}).Error("update group not found after save")
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up update group")
		}
	}
	if ug.Applied {
		// Broadcast it
		b.broadcast(ug)
	} else {
		return errUpdateNotApplied
	}

	return nil
}
