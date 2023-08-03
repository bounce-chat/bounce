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

const permissionUnrestricted = 0x00
const permissionRestricted = 0x01

var errUpdateGroupWithUnknownType = errors.New("update group has unknown update type")
var errInvalidGroupName = errors.New("invalid group name")
var errMutedUntilOnlyMutableBySelf = errors.New("group muted until settings can only be modified by current user")
var errUserNotFound = errors.New("no user found with that ID")
var errUserHasInvalidDeviceGroup = errors.New("user has invalid device group")
var errNoPermissionToEditGroup = errors.New("user does not have permission to edit group")
var errNoPermissionToManageUsers = errors.New("user does not have permission to manage users")
var errCannotPromoteAdminNotInGroup = errors.New("cannot promote a user that is not a member of a group to admin")
var errNoPermissionToChangePermissions = errors.New("group permissions can only be modified by admins")
var errInvalidPermissionPayloadLength = errors.New("permission payload must be one byte")
var errInvalidPermissionByte = errors.New("invalid permission byte")

var updateGroupMutex sync.Mutex

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

	return nil
}

func (ug *updateGroup) AfterDelete(tx *gorm.DB) error {
	return tx.Where("frame_id = ? AND frame_type = ?", ug.ID, typeUpdateGroup).Delete(&deliveryRecord{}).Error
}

func (ug *updateGroup) getID() uuid.UUID {
	return ug.ID
}

func (ug *updateGroup) getScope(myID uuid.UUID) int {
	if ug.Type == updateGroupTypeChangeMutedUntil {
		return scopeSync
	}

	return scopeGroup
}

func (ug *updateGroup) getDestination(myID uuid.UUID) uuid.UUID {
	if ug.Type == updateGroupTypeChangeMutedUntil {
		return myID
	}

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

func (b *bounce) handleUpdateGroup(peer string, payload []byte) {
	updateGroupMutex.Lock()
	defer updateGroupMutex.Unlock()

	// Unpack the signed container
	sc, err := b.unpackSignedContainer(payload)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unpacking signed container for update group")
		return
	}
	var ug updateGroup
	err = msgpack.Unmarshal(sc.Payload, &ug)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error unmarshalling update group")
	}
	ug.OriginalPayload = sc.Payload
	ug.Signature = sc.Signature
	ug.Signer = sc.Signer

	// Make sure that the user that created this signed container is the actor
	if !b.signedByUser(sc, ug.Actor) {
		log.WithFields(log.Fields{
			"peer":           peer,
			"actor":          ug.Actor,
			"signing_device": sc.Signer,
			"group":          ug.Target,
		}).Warn("ignoring group update that was not signed by the supposed actor")
		return
	}

	// Make sure the actor is in the group
	if !b.userIsInGroup(ug.Actor, ug.Target) {
		log.WithFields(log.Fields{
			"peer":  peer,
			"actor": ug.Actor,
			"group": ug.Target,
		}).Warn("device sent an update for a group where the actor is not a part of the group, ignoring")
		return
	}

	// If we already have this update, we just mark that this peer has it too and return
	var existingUG updateGroup
	err = b.database.Where("id = ?", ug.ID).First(&existingUG).Error
	if err == nil {
		b.markDeliveredTo(&existingUG, peer)
		go b.sendAck(peer, typeUpdateGroup, ug.ID)
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up update group")
	}

	// Apply this update locally
	err = b.saveAndApplyUpdateGroup(peer, ug)
	if err != nil {
		log.WithFields(log.Fields{
			"peer":  peer,
			"type":  ug.Type,
			"error": err.Error(),
		}).Error("error applying update group")
		return
	}

	// Ack it
	go b.sendAck(peer, typeUpdateGroup, ug.ID)

	// Mark that the peer that send this update already has it
	b.markDeliveredTo(&ug, peer)

	// Broadcast it
	b.broadcast(&ug)
}

func (b *bounce) saveAndApplyUpdateGroup(peer string, ug updateGroup) error {
	// Look up the group that we're updating
	var g group
	err := b.database.Where("id = ?", ug.Target).First(&g).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"group": ug.Target,
			}).Error("update group specifies group not found in database")
			return err
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up group")
		}
	}

	// Apply the function that handles this type of update
	switch ug.Type {
	case updateGroupTypeChangeName:
		return b.saveAndApplyUpdateGroupChangeName(g, ug)
	case updateGroupTypeAddUser:
		return b.saveAndApplyUpdateGroupAddUser(peer, g, ug)
	case updateGroupTypeRemoveUser:
		return b.saveAndApplyUpdateGroupRemoveUser(g, ug)
	case updateGroupTypeChangeMutedUntil:
		return b.saveAndApplyUpdateGroupChangeMutedUntil(g, ug)
	case updateGroupTypeChangeRetention:
		return b.saveAndApplyUpdateGroupChangeRetention(g, ug)
	case updateGroupTypeSetClearBefore:
		return b.saveAndApplyUpdateGroupSetClearBefore(g, ug)
	case updateGroupTypePromoteAdmin:
		return b.saveAndApplyUpdateGroupPromoteAdmin(g, ug)
	case updateGroupTypeDemoteAdmin:
		return b.saveAndApplyUpdateGroupDemoteAdmin(g, ug)
	case updateGroupTypeChangeUserManagementPermission:
		return b.saveAndApplyUpdateGroupChangeUserManagementPermission(g, ug)
	case updateGroupTypeChangeGroupEditsPermission:
		return b.saveAndApplyUpdateGroupChangeGroupEditsPermission(g, ug)
	case updateGroupTypeChangePostingPermission:
		return b.saveAndApplyUpdateGroupChangePostingPermission(g, ug)
	default:
		log.WithFields(log.Fields{
			"type": ug.Type,
		}).Warn("received update group with unknown type")
		return errUpdateGroupWithUnknownType
	}

	// Update the activity timestamp on the group model
	b.updateLastGroupActivity(ug.Target, ug.Timestamp)

	return nil
}

func (b *bounce) saveAndApplyUpdateGroupChangeName(g group, ug updateGroup) error {
	// Make sure the name is valid
	newName := string(ug.Data)
	if !b.validGroupName(newName) {
		log.WithFields(log.Fields{
			"name": newName,
		}).Error("cannot apply update group with invalid name")
		return errInvalidGroupName
	}

	// Make sure the user has the permissions needed to change the group name
	if g.hasAdmins() && g.RestrictGroupEdits && !b.isGroupAdmin(g.ID, ug.Actor) {
		log.WithFields(log.Fields{
			"user_id": ug.Actor,
		}).Warn("user attempted to change group name without permission")
		return errNoPermissionToEditGroup
	}

	// Save the update group
	err := b.database.Create(&ug).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving update group")
	}

	// Apply the update if it is the most recent one
	if !b.moreRecentUpdateGroup(ug) {
		err = b.database.Model(&g).Update("name", newName).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error updating group name")
		}

		// Inform the UI
		b.userInterface.RenameGroup(UpdateGroupName{
			ID:        ug.ID,
			Thread:    ug.Target,
			Actor:     ug.Actor,
			Timestamp: ug.Timestamp,
			Name:      newName,
		})
	}

	return nil
}

func (b *bounce) saveAndApplyUpdateGroupChangeMutedUntil(g group, ug updateGroup) error {
	// Notification settings can only be changed by sync devices
	if ug.Actor != b.currentUserID() {
		return errMutedUntilOnlyMutableBySelf
	}

	// Save the update group
	err := b.database.Create(&ug).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving update group")
	}

	// Apply the update if it is the most recent one
	if !b.moreRecentUpdateGroup(ug) {
		// Decode the new muted until value
		mutedUntil := int64(binary.LittleEndian.Uint64(ug.Data))

		// Update the database
		err = b.database.Model(&g).Update("muted_until", mutedUntil).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error updating group muted until")
		}

		// Inform the UI
		b.userInterface.GroupMutedUntilChanged(g.ID, mutedUntil)
	}

	return nil
}

func (b *bounce) saveAndApplyUpdateGroupChangeRetention(g group, ug updateGroup) error {
	// Make sure the user has the permissions needed to change the group retention
	if g.hasAdmins() && g.RestrictGroupEdits && !b.isGroupAdmin(g.ID, ug.Actor) {
		log.WithFields(log.Fields{
			"user_id": ug.Actor,
		}).Warn("user attempted to change group retention without permission")
		return errNoPermissionToEditGroup
	}

	// Save the update group
	err := b.database.Create(&ug).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving update group")
	}

	// Decode the new retention value
	retention := int64(binary.LittleEndian.Uint64(ug.Data))

	// Inform the UI
	b.userInterface.GroupRetentionChanged(UpdateGroupRetention{
		ID:        ug.ID,
		Thread:    ug.Target,
		Actor:     ug.Actor,
		Timestamp: ug.Timestamp,
		Retention: retention,
	})

	// Apply the update if it is the most recent one
	if !b.moreRecentUpdateGroup(ug) {
		err = b.database.Model(&g).Update("retention", retention).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error updating group retention")
		}
	}

	return nil
}

func (b *bounce) saveAndApplyUpdateGroupSetClearBefore(g group, ug updateGroup) error {
	// Make sure the actor has the correct permissions to clear the chat history
	if g.hasAdmins() && g.RestrictGroupEdits && !b.isGroupAdmin(g.ID, ug.Actor) {
		log.WithFields(log.Fields{
			"user_id": ug.Actor,
		}).Warn("user attempted to clear group history without permission")
		return errNoPermissionToEditGroup
	}

	// Save the update group
	err := b.database.Create(&ug).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving update group")
	}

	// Decode the new retention value
	clearBefore := int64(binary.LittleEndian.Uint64(ug.Data))

	gms := []groupMessage{}
	err = b.database.Select("id").Where("written_at <= ? AND destination = ?", clearBefore, g.ID).Find(&gms).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error selecting group messages to delete while clearing chat history")
	}
	for _, gm := range gms {
		err := b.database.Delete(&gm).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
				"id":    gm.ID,
			}).Fatal("error deleting group message while clearing chat history")
		}
		b.userInterface.DeleteMessage(gm.ID)
	}
	b.userInterface.GroupChatHistoryCleared(UpdateGroupClearHistory{
		ID:        ug.ID,
		Thread:    ug.Target,
		Actor:     ug.Actor,
		Timestamp: ug.Timestamp,
		ClearTime: clearBefore,
	})

	// Update the clear before value on the group if this one is newer
	if g.ClearBefore < clearBefore {
		err := b.database.Model(&g).Update("clear_before", clearBefore).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":        err.Error(),
				"group_id":     g.ID,
				"clear_before": clearBefore,
			}).Fatal("database error updating group clear before")
		}
	}

	return nil
}

func (b *bounce) saveAndApplyUpdateGroupAddUser(peer string, g group, ug updateGroup) error {
	// Make sure this user has permission to manage group members
	if g.hasAdmins() && g.RestrictUserManagement && !b.isGroupAdmin(g.ID, ug.Actor) {
		log.WithFields(log.Fields{
			"user_id": ug.Actor,
		}).Warn("user attempted to add user to group without permission")
		return errNoPermissionToManageUsers
	}

	// Unmarshall the new user
	var u user
	err := msgpack.Unmarshal(ug.Data, &u)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling user")
		return err
	}

	if u.ID == b.currentUserID() {
		// This update group adds us to the group
		userIDs := []uuid.UUID{}
		for _, u := range g.Users {
			userIDs = append(userIDs, u.ID)
		}
		b.userInterface.NewGroupChat(Group{
			ID:      g.ID,
			Name:    g.Name,
			UserIDs: userIDs,
		})
	} else {
		// Ensure the user is valid
		if !b.hasValidDeviceGroup(u) {
			return errUserHasInvalidDeviceGroup
		}

		// Save the user and their devices if we don't have them
		err = b.database.Transaction(func(tx *gorm.DB) error {
			for _, dev := range u.Devices {
				err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&dev).Error
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error saving device that belongs to a user being added to a group")
					return err
				}
			}
			err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&u).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error saving user that is being added to a group")
				return err
			}

			return nil
		})
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error in transaction creating new user added to group")
		}

		// Ack and delivery track the user's devices unless we are sending this
		if peer != b.network.Address() {
			for _, dev := range u.Devices {
				go b.sendAck(peer, typeDevice, dev.ID)
				b.markDeliveredTo(&dev, peer)
			}
		}

		// Attempt to make a connection to the user
		b.userConnectionDesired(u.ID)
	}

	// Associate the user with the group
	err = b.database.Exec("INSERT INTO group_users VALUES(?, ?)", ug.Target, u.ID).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error adding user to group")
	}

	// Inform the UI
	b.userInterface.AddUser(
		UpdateGroupAddUser{
			ID:        ug.ID,
			Thread:    ug.Target,
			Actor:     ug.Actor,
			Timestamp: ug.Timestamp,
			User: User{
				ID:   u.ID,
				Name: u.Name,
			},
		})

	return nil
}

func (b *bounce) saveAndApplyUpdateGroupRemoveUser(g group, ug updateGroup) error {
	// Make sure this user has permission to manage group members
	if g.hasAdmins() && g.RestrictUserManagement && !b.isGroupAdmin(g.ID, ug.Actor) {
		log.WithFields(log.Fields{
			"user_id": ug.Actor,
		}).Warn("user attempted to remove user from group without permission")
		return errNoPermissionToManageUsers
	}

	// TODO
	return nil
}

func (b *bounce) saveAndApplyUpdateGroupPromoteAdmin(g group, ug updateGroup) error {
	// Make sure this user has permission to manage group members
	if g.hasAdmins() && g.RestrictUserManagement && !b.isGroupAdmin(g.ID, ug.Actor) {
		log.WithFields(log.Fields{
			"user_id": ug.Actor,
		}).Warn("user attempted to add user to group without permission")
		return errNoPermissionToManageUsers
	}

	// Save the update group
	err := b.database.Create(&ug).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving update group")
	}

	// Parse the UUID
	userID, err := uuid.FromBytes(ug.Data)
	if err != nil {
		log.WithFields(log.Fields{
			"error":   err.Error(),
			"actor":   ug.Actor,
			"user_id": ug.Data,
		}).Error("update group attempted to promote admin with invalid UUID")
		return err
	}

	// Make sure this user is already in the group
	if !b.userIsInGroup(userID, ug.Target) {
		log.WithFields(log.Fields{
			"error":   err.Error(),
			"actor":   ug.Actor,
			"user_id": userID,
		}).Error("update group attempted to promote admin that is not in the group")
		return errCannotPromoteAdminNotInGroup
	}

	// Make sure that this is the latest promotion or demotion for this user
	var moreRecentUpdates bool
	err = b.database.Table("update_groups").
		Select("count(*) >= 1").
		Where("target = ? AND type IN (?, ?) AND timestamp > ? AND data = ?", ug.Target, updateGroupTypePromoteAdmin, updateGroupTypeDemoteAdmin, ug.Timestamp, ug.Data).
		Find(&moreRecentUpdates).
		Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error checking for more recent update groups")
	}

	// Add this user as an admin if this is the latest update
	if !moreRecentUpdates {
		b.addGroupAdmin(ug.Target, userID)
	}

	// Notify the UI
	b.userInterface.AdminPromoted(UpdateGroupAdminPromoted{
		ID:        ug.ID,
		Thread:    ug.Target,
		Actor:     ug.Actor,
		Timestamp: ug.Timestamp,
		UserID:    userID,
	})

	return nil
}

func (b *bounce) saveAndApplyUpdateGroupDemoteAdmin(g group, ug updateGroup) error {
	// Make sure this user has permission to manage group members
	if g.hasAdmins() && g.RestrictUserManagement && !b.isGroupAdmin(g.ID, ug.Actor) {
		log.WithFields(log.Fields{
			"user_id": ug.Actor,
		}).Warn("user attempted to add user to group without permission")
		return errNoPermissionToManageUsers
	}

	// Save the update group
	err := b.database.Create(&ug).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving update group")
	}

	// Parse the UUID
	userID, err := uuid.FromBytes(ug.Data)
	if err != nil {
		log.WithFields(log.Fields{
			"error":   err.Error(),
			"actor":   ug.Actor,
			"user_id": ug.Data,
		}).Error("update group attempted to promote admin with invalid UUID")
		return err
	}

	// Make sure that this is the latest promotion or demotion for this user
	var moreRecentUpdates bool
	err = b.database.Table("update_groups").
		Select("count(*) >= 1").
		Where("target = ? AND type IN (?, ?) AND timestamp > ? AND data = ?", ug.Target, updateGroupTypePromoteAdmin, updateGroupTypeDemoteAdmin, ug.Timestamp, ug.Data).
		Find(&moreRecentUpdates).
		Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error checking for more recent update groups")
	}

	// Remove this user as an admin if this is the latest update
	if !moreRecentUpdates {
		b.removeGroupAdmin(ug.Target, userID)
	}

	// Notify the UI
	b.userInterface.AdminDemoted(UpdateGroupAdminDemoted{
		ID:        ug.ID,
		Thread:    ug.Target,
		Actor:     ug.Actor,
		Timestamp: ug.Timestamp,
		UserID:    userID,
	})

	return nil
}

func (b *bounce) saveAndApplyUpdateGroupChangeUserManagementPermission(g group, ug updateGroup) error {
	// Apply the update
	err := b.saveAndApplyUpdateGroupChangePermission(g, ug, "restrict_user_management")
	if err != nil {
		return err
	}

	// Inform the UI
	restricted, err := ug.permissionPayloadIsRestricted()
	if err != nil {
		return nil
	}
	if restricted {
		b.userInterface.UserManagementRestricted(UpdateGroupUserManagementRestricted{
			ID:        ug.ID,
			Thread:    ug.Target,
			Actor:     ug.Actor,
			Timestamp: ug.Timestamp,
		})
	} else {
		b.userInterface.UserManagementUnrestricted(UpdateGroupUserManagementUnrestricted{
			ID:        ug.ID,
			Thread:    ug.Target,
			Actor:     ug.Actor,
			Timestamp: ug.Timestamp,
		})
	}

	return nil
}

func (b *bounce) saveAndApplyUpdateGroupChangeGroupEditsPermission(g group, ug updateGroup) error {
	// Apply the update
	err := b.saveAndApplyUpdateGroupChangePermission(g, ug, "restrict_group_edits")
	if err != nil {
		return err
	}

	// Inform the UI
	restricted, err := ug.permissionPayloadIsRestricted()
	if err != nil {
		return nil
	}
	if restricted {
		b.userInterface.GroupEditsRestricted(UpdateGroupEditsRestricted{
			ID:        ug.ID,
			Thread:    ug.Target,
			Actor:     ug.Actor,
			Timestamp: ug.Timestamp,
		})
	} else {
		b.userInterface.GroupEditsUnrestricted(UpdateGroupEditsUnrestricted{
			ID:        ug.ID,
			Thread:    ug.Target,
			Actor:     ug.Actor,
			Timestamp: ug.Timestamp,
		})
	}

	return nil
}

func (b *bounce) saveAndApplyUpdateGroupChangePostingPermission(g group, ug updateGroup) error {
	// Apply the update
	err := b.saveAndApplyUpdateGroupChangePermission(g, ug, "restrict_posting")
	if err != nil {
		return err
	}

	// Inform the UI
	restricted, err := ug.permissionPayloadIsRestricted()
	if err != nil {
		return nil
	}
	if restricted {
		b.userInterface.PostingRestricted(UpdateGroupPostingRestricted{
			ID:        ug.ID,
			Thread:    ug.Target,
			Actor:     ug.Actor,
			Timestamp: ug.Timestamp,
		})
	} else {
		b.userInterface.PostingUnrestricted(UpdateGroupPostingUnrestricted{
			ID:        ug.ID,
			Thread:    ug.Target,
			Actor:     ug.Actor,
			Timestamp: ug.Timestamp,
		})
	}

	return nil
}

func (b *bounce) saveAndApplyUpdateGroupChangePermission(g group, ug updateGroup, field string) error {
	if g.hasAdmins() && !b.isGroupAdmin(g.ID, ug.Actor) {
		log.WithFields(log.Fields{
			"user_id":  ug.Actor,
			"group_id": ug.Target,
			"field":    field,
			"state":    ug.Data,
		}).Warn("user who is not an admin attemped to change permission")
		return errNoPermissionToChangePermissions
	}

	err := b.database.Create(&ug).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving update group")
	}

	if !b.moreRecentUpdateGroup(ug) {
		restricted, err := ug.permissionPayloadIsRestricted()
		if err != nil {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"user_id":  ug.Actor,
				"group_id": ug.Target,
				"field":    field,
				"state":    ug.Data,
			}).Error("invalid permission byte on update group")
			return err
		}

		err = b.database.Model(&g).Update(field, restricted).Error
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"group_id": g.ID,
				}).Error("group not found when restricting user management")
				return err
			} else {
				log.WithFields(log.Fields{
					"error":    err.Error(),
					"group_id": g.ID,
					"field":    field,
				}).Fatal("database error updating permission field on group")
			}
		}
	}

	return nil
}

func (b *bounce) renameGroup(groupID uuid.UUID, newName string) error {
	return b.applyAndBroadcastUpdateGroup(updateGroup{
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

	return b.applyAndBroadcastUpdateGroup(updateGroup{
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

	return b.applyAndBroadcastUpdateGroup(updateGroup{
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

	return b.applyAndBroadcastUpdateGroup(updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeSetClearBefore,
		Data:      payload,
	})
}

func (b *bounce) addUserToGroup(groupID, userID uuid.UUID) error {
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
	err = b.applyAndBroadcastUpdateGroup(updateGroup{
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

	// Connect to this new user to send the new group and do a reference flow
	b.userConnectionDesired(userID)

	return nil
}

func (b *bounce) removeUserFromGroup(groupID, userID uuid.UUID) error {
	// TODO
	// TODO: also remove them from admin list if they are an admin
	return nil
}

func (b *bounce) promoteAdmin(groupID, userID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypePromoteAdmin,
		Data:      userID[:],
	})
}

func (b *bounce) demoteAdmin(groupID, userID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeDemoteAdmin,
		Data:      userID[:],
	})
}

func (b *bounce) restrictUserManagement(groupID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeChangeUserManagementPermission,
		Data:      []byte{permissionRestricted},
	})
}

func (b *bounce) unrestrictUserManagement(groupID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeChangeUserManagementPermission,
		Data:      []byte{permissionUnrestricted},
	})
}

func (b *bounce) restrictGroupEdits(groupID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeChangeGroupEditsPermission,
		Data:      []byte{permissionRestricted},
	})
}

func (b *bounce) unrestrictGroupEdits(groupID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeChangeGroupEditsPermission,
		Data:      []byte{permissionUnrestricted},
	})
}

func (b *bounce) restrictPosting(groupID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeChangePostingPermission,
		Data:      []byte{permissionRestricted},
	})
}

func (b *bounce) unrestrictPosting(groupID uuid.UUID) error {
	return b.applyAndBroadcastUpdateGroup(updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      updateGroupTypeChangePostingPermission,
		Data:      []byte{permissionUnrestricted},
	})
}

func (b *bounce) moreRecentUpdateGroup(ug updateGroup) bool {
	var moreRecentUpdates bool

	err := b.database.Table("update_groups").
		Select("count(*) >= 1").
		Where("target = ? AND type = ? AND timestamp > ?", ug.Target, ug.Type, ug.Timestamp).
		Find(&moreRecentUpdates).
		Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error checking for more recent update groups")
	}

	return moreRecentUpdates
}

func (b *bounce) applyAndBroadcastUpdateGroup(ug updateGroup) error {
	// Create the signed container for this update
	var err error
	ug.OriginalPayload, err = msgpack.Marshal(&ug)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling group update")
	}
	sc := b.createSignedContainer(ug.OriginalPayload)
	ug.Signature = sc.Signature
	ug.Signer = sc.Signer

	// Apply the update locally
	err = b.saveAndApplyUpdateGroup(b.network.Address(), ug)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error applying update group")
		return err
	}

	// Broadcast
	b.broadcast(&ug)

	return nil
}
