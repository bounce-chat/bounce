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
const updateGroupTypeInviteUser = uint16(1)
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
const updateGroupTypeSetImage = uint16(15)
const updateGroupTypeRevokeInvite = uint16(16)
const updateGroupTypeRespondToInvite = uint16(17)

const permissionUnrestricted = 0x00
const permissionRestricted = 0x01

const rejectInvite = 0x00
const acceptInvite = 0x01

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
var errCannotDemoteAdminWhoDeletedGroup = errors.New("admins who deleted group cannot be demoted")
var errAlreadyDeleted = errors.New("group already deleted")
var errMustBeInvitedToRespond = errors.New("user must have active invite to group in order to respond to invite")

// An updateGroup frame changes the settings and status of a group, such as permissions, membership, retention, or notification settings.
// Some settings, like retention and membership, must be observed by all participants of the group, where others like notification are only
// sent to sync devices.  The data field of the structure contains different data depending on the type of update.
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
	Notified        bool   `msgpack:"-"`
	Seen            bool   `msgpack:"-"`
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
	if ug.ID == uuid.Nil {
		return nil
	}
	if ug.CustomScope != uuid.Nil {
		err := tx.Clauses(clause.Returning{}).Where("id = ?", ug.CustomScope).Delete(&customScope{}).Error
		if err != nil {
			return err
		}
	}

	err := tx.Clauses(clause.Returning{}).Where("update_group_id = ?", ug.ID).Delete(&confirmation{}).Error
	if err != nil {
		return err
	}

	return tx.Clauses(clause.Returning{}).Where("frame_id = ? AND frame_type = ?", ug.ID, typeUpdateGroup).Delete(&deliveryRecord{}).Error
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

	return scopeGroupWithInvites
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

func (ug *updateGroup) confirmingUsers(possibleUsers []uuid.UUID) int {
	possible := map[uuid.UUID]bool{}
	for _, id := range possibleUsers {
		possible[id] = true
	}

	users := make(map[uuid.UUID]bool)
	for _, c := range ug.Confirmations {
		if possible[c.Author] {
			users[c.Author] = true
		} else {
			log.WithFields(log.Fields{
				"update_group_id": ug.ID,
				"offending_user":  c.Author,
			}).Warn("update group has a confirmation that wasn't created by a possible user")
		}
	}
	users[ug.getAuthor()] = true

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
	case updateGroupTypeSetImage:
		_, err := uuid.FromBytes(ug.Data)
		return err == nil
	case updateGroupTypeInviteUser:
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
	case updateGroupTypeRevokeInvite:
		_, err := uuid.FromBytes(ug.Data)
		return err == nil
	case updateGroupTypeRespondToInvite:
		if len(ug.Data) != 1 {
			return false
		}
		if !(ug.Data[0] == acceptInvite || ug.Data[0] == rejectInvite) {
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

func (b *Bounce) handleUpdateGroup(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	groupMutex.Lock()
	defer groupMutex.Unlock()

	// Unpack the signed container
	sc, err := b.unpackSignedContainer(payload)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unpacking signed container for update group")
		return nil, false
	}
	var ug updateGroup
	err = msgpack.Unmarshal(sc.Payload, &ug)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling update group")
		return nil, false
	}
	ug.OriginalPayload = sc.Payload
	ug.Signature = sc.Signature
	ug.Signer = sc.Signer

	// Ignore update groups for blocked groups
	for _, blockedGroup := range b.blockedGroups() {
		if ug.Target == blockedGroup {
			go b.sendAck(peer, typeUpdateGroup, ug.ID)
			return nil, false
		}
	}

	// If we already have this update, we just mark that this peer has it too and return
	var existingUG updateGroup
	err = b.database.Where("id = ?", ug.ID).First(&existingUG).Error
	if err == nil {
		return &existingUG, false
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
		return nil, false
	}

	// Save this update
	err = b.database.Create(&ug).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving update group")
	}

	// Update the group state without commiting to the database or sending to the UI
	err = b.reloadGroupConsensusSince(ug.Target, ug.Timestamp)
	if err != nil {
		// We are getting an update that isn't a part of a valid consensus stack.  This is probably an invite for
		// us to join a group that we aren't aware of it yet, but either way it's ok to save and move on here.
		return nil, false
	}

	// If there are confirmations for this update group already in the database, make sure their author
	// was allowed to confirm the update and set the broadcast info for the confirmation
	b.processEarlyConfirmations(ug)

	if !catchUp {
		// Update the group state in the database and UI
		b.writeGroupConsensus(ug.Target)

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
					return nil, false
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
						return nil, false
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
					if rd.connectedSockets.Load() > 0 {
						go b.sendDirect(dev.Address, &ug)
					}
				}
			}
		}
	}

	return &ug, true
}

func (b *Bounce) processEarlyConfirmations(ug updateGroup) {
	var confirmations []confirmation
	err := b.database.Find(&confirmations, "update_group_id = ?", ug.ID).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error finding all confirmations")
	}
	for _, c := range confirmations {
		err = b.database.Model(&confirmation{}).
			Where("id = ?", c.ID).
			Select("destination", "custom_scope").
			Updates(map[string]interface{}{
				"destination":  ug.Target,
				"custom_scope": ug.CustomScope,
			}).Error
		if err != nil {
			log.WithFields(log.Fields{
				"confirmation_id": c.ID,
				"error":           err.Error(),
			}).Fatal("error updating confirmation destination and custom scope")
		}
		c.Destination = ug.Target
		c.CustomScope = ug.CustomScope
		go b.broadcast(&c)
	}
}

func (b *Bounce) RenameGroup(groupID uuid.UUID, newName string) error {
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

func (b *Bounce) SetGroupImage(groupID uuid.UUID, image []byte) error {
	if !validImage(image) {
		return errInvalidImage
	}

	fileID := uuid.New()
	err := b.embedFile(fileID, image, scopeGroupWithInvites, groupID, fileTypeGroupImage, groupID)
	if err != nil {
		return err
	}

	return b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeSetImage,
		Data:      fileID[:],
	})
}

func (b *Bounce) SetGroupMutedUntil(groupID uuid.UUID, mutedUntil int64) error {
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

func (b *Bounce) SetGroupRetention(groupID uuid.UUID, retention int64) error {
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

func (b *Bounce) ClearGroupChatHistory(groupID uuid.UUID) error {
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

func (b *Bounce) InviteUserToGroup(groupID, userID uuid.UUID) error {
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
		Type:      updateGroupTypeInviteUser,
		Data:      newUserBytes,
	})
	if err != nil {
		return err
	}

	// Connect to this new user to send the new group
	b.UserConnectionDesired(userID)

	// Do a reference flow with any devices we're currently connected to
	for _, dev := range newUser.Devices {
		go b.sendReferences(dev.Address)
	}

	return nil
}

func (b *Bounce) RemoveUserFromGroup(groupID, userID uuid.UUID) error {
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
			if rd.connectedSockets.Load() > 0 {
				go b.sendDirect(dev.Address, ug)
			}
		}
	}

	return nil
}

func (b *Bounce) DeleteGroup(groupID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeDelete,
	})
}

func (b *Bounce) PromoteGroupAdmin(groupID, userID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypePromoteAdmin,
		Data:      userID[:],
	})
}

func (b *Bounce) DemoteGroupAdmin(groupID, userID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeDemoteAdmin,
		Data:      userID[:],
	})
}

func (b *Bounce) RestrictUserManagement(groupID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeChangeUserManagementPermission,
		Data:      []byte{permissionRestricted},
	})
}

func (b *Bounce) UnrestrictUserManagement(groupID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeChangeUserManagementPermission,
		Data:      []byte{permissionUnrestricted},
	})
}

func (b *Bounce) RestrictGroupEdits(groupID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeChangeGroupEditsPermission,
		Data:      []byte{permissionRestricted},
	})
}

func (b *Bounce) UnrestrictGroupEdits(groupID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeChangeGroupEditsPermission,
		Data:      []byte{permissionUnrestricted},
	})
}

func (b *Bounce) RestrictPosting(groupID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeChangePostingPermission,
		Data:      []byte{permissionRestricted},
	})
}

func (b *Bounce) UnrestrictPosting(groupID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeChangePostingPermission,
		Data:      []byte{permissionUnrestricted},
	})
}

func (b *Bounce) BlockGroup(groupID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeBlock,
	})
}

func (b *Bounce) SetGroupReadReceiptSettings(groupID uuid.UUID, override bool, enabled bool) error {
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

func (b *Bounce) SetGroupTypingIndicatorSettings(groupID uuid.UUID, override bool, enabled bool) error {
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

func (b *Bounce) RevokeInvite(groupID, userID uuid.UUID) error {
	ug := &updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeRevokeInvite,
		Data:      userID[:],
	}
	err := b.applyAndBroadcastUpdateGroup(ug)
	if err != nil {
		return err
	}

	// Broadcast this removal directly to the removed device since it is no longer in scope
	var u user
	err = b.database.Preload(clause.Associations).Where("id = ?", userID).First(&u).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"user_id": userID,
			}).Error("user not found for direct revoked invite broadcast")
			return err
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error looking up user")
		}
	}

	for _, dev := range u.Devices {
		rd := b.getRemoteDevice(dev.Address)
		if rd.connectedSockets.Load() > 0 {
			go b.sendDirect(dev.Address, ug)
		}
	}

	return nil
}

func (b *Bounce) AcceptInvite(groupID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeRespondToInvite,
		Data:      []byte{acceptInvite},
	})
}

func (b *Bounce) RejectInvite(groupID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(&updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeRespondToInvite,
		Data:      []byte{rejectInvite},
	})
}

func (b *Bounce) applyAndBroadcastUpdateGroup(ug *updateGroup) error {
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
	b.reloadGroupConsensusSince(ug.Target, ug.Timestamp)
	b.writeGroupConsensus(ug.Target)

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

func (b *Bounce) informUIOfUpdateGroup(ug updateGroup) {
	switch ug.Type {
	case updateGroupTypeChangeName:
		b.informUIUpdateGroupChangeName(ug)
	case updateGroupTypeInviteUser:
		b.informUIUpdateGroupInviteUser(ug)
	case updateGroupTypeRemoveUser:
		b.informUIUpdateGroupRemoveUser(ug)
	case updateGroupTypeChangeRetention:
		b.informUIUpdateGroupChangeRetention(ug)
	case updateGroupTypeChangeMutedUntil:
		return
	case updateGroupTypeSetClearBefore:
		b.informUIUpdateGroupSetClearBefore(ug)
	case updateGroupTypePromoteAdmin:
		b.informUIUpdateGroupPromoteAdmin(ug)
	case updateGroupTypeDemoteAdmin:
		b.informUIUpdateGroupDemoteAdmin(ug)
	case updateGroupTypeChangeUserManagementPermission:
		b.informUIUpdateGroupChangeUserManagementPermission(ug)
	case updateGroupTypeChangeGroupEditsPermission:
		b.informUIUpdateGroupChangeGroupEditsPermission(ug)
	case updateGroupTypeChangePostingPermission:
		b.informUIUpdateGroupChangePostingPermission(ug)
	case updateGroupTypeDelete:
		return
	case updateGroupTypeBlock:
		b.informUIUpdateGroupBlock(ug)
	case updateGroupTypeSetReadReceiptSettings:
		return
	case updateGroupTypeSetTypingIndicatorSettings:
		return
	case updateGroupTypeSetImage:
		b.informUIUpdateGroupSetImage(ug)
	case updateGroupTypeRevokeInvite:
		b.informUIUpdateGroupRevokeInvite(ug)
	case updateGroupTypeRespondToInvite:
		b.informUIUpdateGroupRespondToInvite(ug)
	default:
		log.WithFields(log.Fields{
			"type": ug.Type,
		}).Warn("cannot inform UI of update group with unknown type")
	}
}

func (b *Bounce) informUIUpdateGroupChangeName(ug updateGroup) {
	newName := string(ug.Data)
	if !validGroupName(newName) {
		log.WithFields(log.Fields{
			"name": newName,
		}).Error("cannot inform UI of update group with invalid name")
		return
	}

	b.ui.RenameGroup(UpdateGroupName{
		ID:        ug.ID,
		Thread:    ug.Target,
		Actor:     ug.Actor,
		Timestamp: ug.Timestamp,
		Name:      newName,
	})
}

func (b *Bounce) informUIUpdateGroupInviteUser(ug updateGroup) {
	// Unmarshall the new user
	var u user
	err := msgpack.Unmarshal(ug.Data, &u)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling user")
		return
	}

	// Notify the UI
	b.ui.InviteUser(
		UpdateGroupInviteUser{
			ID:        ug.ID,
			Thread:    ug.Target,
			Actor:     ug.Actor,
			Timestamp: ug.Timestamp,
			User: User{
				ID:   u.ID,
				Name: u.Name,
			},
		})
}

func (b *Bounce) informUIUpdateGroupRemoveUser(ug updateGroup) {
	// Parse the user ID
	userID, err := uuid.FromBytes(ug.Data)
	if err != nil {
		log.WithFields(log.Fields{
			"error":   err.Error(),
			"actor":   ug.Actor,
			"user_id": ug.Data,
		}).Error("update group attempted to remove user with invalid UUID")
		return
	}

	// Notify the UI
	b.ui.RemoveUser(
		UpdateGroupRemoveUser{
			ID:        ug.ID,
			Thread:    ug.Target,
			Actor:     ug.Actor,
			Timestamp: ug.Timestamp,
			User:      userID,
		})
}

func (b *Bounce) informUIUpdateGroupChangeRetention(ug updateGroup) {
	b.ui.GroupRetentionChanged(UpdateGroupRetention{
		ID:        ug.ID,
		Thread:    ug.Target,
		Actor:     ug.Actor,
		Timestamp: ug.Timestamp,
		Retention: int64(binary.LittleEndian.Uint64(ug.Data)),
	})
}

func (b *Bounce) informUIUpdateGroupSetClearBefore(ug updateGroup) {
	b.ui.GroupChatHistoryCleared(UpdateGroupClearHistory{
		ID:        ug.ID,
		Thread:    ug.Target,
		Actor:     ug.Actor,
		Timestamp: ug.Timestamp,
		ClearTime: int64(binary.LittleEndian.Uint64(ug.Data)),
	})
}

func (b *Bounce) informUIUpdateGroupPromoteAdmin(ug updateGroup) {
	// Parse the UUID
	userID, err := uuid.FromBytes(ug.Data)
	if err != nil {
		log.WithFields(log.Fields{
			"error":   err.Error(),
			"actor":   ug.Actor,
			"user_id": ug.Data,
		}).Error("update group attempted to promote admin with invalid UUID")
		return
	}

	// Notify the UI
	b.ui.AdminPromoted(UpdateGroupAdminPromoted{
		ID:        ug.ID,
		Thread:    ug.Target,
		Actor:     ug.Actor,
		Timestamp: ug.Timestamp,
		UserID:    userID,
	})
}

func (b *Bounce) informUIUpdateGroupDemoteAdmin(ug updateGroup) {
	// Parse the UUID
	userID, err := uuid.FromBytes(ug.Data)
	if err != nil {
		log.WithFields(log.Fields{
			"error":   err.Error(),
			"actor":   ug.Actor,
			"user_id": ug.Data,
		}).Error("update group attempted to promote admin with invalid UUID")
		return
	}

	// Notify the UI
	b.ui.AdminDemoted(UpdateGroupAdminDemoted{
		ID:        ug.ID,
		Thread:    ug.Target,
		Actor:     ug.Actor,
		Timestamp: ug.Timestamp,
		UserID:    userID,
	})
}

func (b *Bounce) informUIUpdateGroupChangeUserManagementPermission(ug updateGroup) {
	restricted, err := ug.permissionPayloadIsRestricted()
	if err != nil {
		return
	}
	if restricted {
		b.ui.UserManagementRestricted(UpdateGroupUserManagementRestricted{
			ID:        ug.ID,
			Thread:    ug.Target,
			Actor:     ug.Actor,
			Timestamp: ug.Timestamp,
		})
	} else {
		b.ui.UserManagementUnrestricted(UpdateGroupUserManagementUnrestricted{
			ID:        ug.ID,
			Thread:    ug.Target,
			Actor:     ug.Actor,
			Timestamp: ug.Timestamp,
		})
	}
}

func (b *Bounce) informUIUpdateGroupChangeGroupEditsPermission(ug updateGroup) {
	restricted, err := ug.permissionPayloadIsRestricted()
	if err != nil {
		return
	}
	if restricted {
		b.ui.GroupEditsRestricted(UpdateGroupEditsRestricted{
			ID:        ug.ID,
			Thread:    ug.Target,
			Actor:     ug.Actor,
			Timestamp: ug.Timestamp,
		})
	} else {
		b.ui.GroupEditsUnrestricted(UpdateGroupEditsUnrestricted{
			ID:        ug.ID,
			Thread:    ug.Target,
			Actor:     ug.Actor,
			Timestamp: ug.Timestamp,
		})
	}
}

func (b *Bounce) informUIUpdateGroupChangePostingPermission(ug updateGroup) {
	restricted, err := ug.permissionPayloadIsRestricted()
	if err != nil {
		return
	}
	if restricted {
		b.ui.PostingRestricted(UpdateGroupPostingRestricted{
			ID:        ug.ID,
			Thread:    ug.Target,
			Actor:     ug.Actor,
			Timestamp: ug.Timestamp,
		})
	} else {
		b.ui.PostingUnrestricted(UpdateGroupPostingUnrestricted{
			ID:        ug.ID,
			Thread:    ug.Target,
			Actor:     ug.Actor,
			Timestamp: ug.Timestamp,
		})
	}
}

func (b *Bounce) informUIUpdateGroupBlock(ug updateGroup) {
	b.ui.UserBlockedGroup(UserBlockedGroup{
		ID:        ug.ID,
		Thread:    ug.Target,
		Actor:     ug.Actor,
		Timestamp: ug.Timestamp,
	})
}

func (b *Bounce) informUIUpdateGroupSetImage(ug updateGroup) {
	b.ui.UserChangedGroupImage(UpdateGroupUserChangedGroupImage{
		ID:        ug.ID,
		Thread:    ug.Target,
		Actor:     ug.Actor,
		Timestamp: ug.Timestamp,
	})
}

func (b *Bounce) informUIUpdateGroupRevokeInvite(ug updateGroup) {
	// Parse the UUID
	userID, err := uuid.FromBytes(ug.Data)
	if err != nil {
		log.WithFields(log.Fields{
			"error":   err.Error(),
			"actor":   ug.Actor,
			"user_id": ug.Data,
		}).Error("update group attempted to revoke invite with invalid UUID")
		return
	}

	b.ui.GroupInviteRevoked(UpdateGroupInviteRevoked{
		ID:        ug.ID,
		Thread:    ug.Target,
		Actor:     ug.Actor,
		Timestamp: ug.Timestamp,
		UserID:    userID,
	})
}

func (b *Bounce) informUIUpdateGroupRespondToInvite(ug updateGroup) {
	if len(ug.Data) != 1 {
		log.WithFields(log.Fields{
			"id": ug.ID,
		}).Error("invalid payload length for update group respond to invite")
		return
	}
	rejected := ug.Data[0] == rejectInvite
	accepted := ug.Data[0] == acceptInvite

	if !accepted && !rejected {
		log.WithFields(log.Fields{
			"id":      ug.ID,
			"payload": ug.Data[0],
		}).Error("invalid payload for update group respond to invite")
		return
	}

	if accepted {
		b.ui.GroupInviteAccepted(UpdateGroupInviteAccepted{
			ID:        ug.ID,
			Thread:    ug.Target,
			Actor:     ug.Actor,
			Timestamp: ug.Timestamp,
		})
	}

	if rejected {
		b.ui.GroupInviteRejected(UpdateGroupInviteRejected{
			ID:        ug.ID,
			Thread:    ug.Target,
			Actor:     ug.Actor,
			Timestamp: ug.Timestamp,
		})
	}
}
