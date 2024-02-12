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
var errInvalidPermissionPayloadLength = errors.New("permission payload must be one byte")
var errInvalidPermissionByte = errors.New("invalid permission byte")
var errUpdateNotApplied = errors.New("update could not be applied")
var errGroupNotFound = errors.New("group not found")
var errUserNotInGroup = errors.New("user is not in group")
var errAdminRequired = errors.New("this action can only be performed by admins")

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
	if ug.Type == updateGroupTypeChangeMutedUntil {
		return scopeSync
	}

	if ug.CustomScope != uuid.Nil {
		return scopeCustom
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

	// Save this update
	err = b.database.Create(&ug).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving update group")
	}

	if ug.Type == updateGroupTypeAddUser {
		var newUser user
		err := msgpack.Unmarshal(ug.Data, &newUser)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error unmarshalling new user in update group")
			return nil
		}
		if newUser.ID == b.currentUserID() {
			var g group
			err = b.database.First(&g, "id = ?", ug.Target).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					// If we're handling an update group that adds us to a group we're not aware of,
					// we're going to get the group information from the reference flow, so we don't
					// need to try applying it now
					return nil
				} else {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Fatal("database error looking up group")
				}
			}
		}
	}

	if !catchUp {
		// Update the group state
		b.updateGroupConsensus(ug.Target)

		// If this is a remove user frame, make sure to send it to the devices of the user who was removed
		if ug.Type == updateGroupTypeRemoveUser {
			userID, err := uuid.FromBytes(ug.Data)
			if err != nil {
				log.WithFields(log.Fields{
					"error":   err.Error(),
					"actor":   ug.Actor,
					"user_id": ug.Data,
				}).Error("update group attempted to remove user with invalid UUID")
				return nil
			}

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
					rd := b.getRemoteDevice(dev.Address)
					if rd.connectedSockets > 0 {
						go b.sendDirect(dev.Address, &ug)
					}
				}
			} else {
				// Reload the update group before returning it to ensure the custom scope is set before broadcast
				err = b.database.First(&ug, "id = ?", ug.ID).Error
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
			}
		}
	}

	return &ug
}

func (b *bounce) renameGroup(groupID uuid.UUID, newName string) error {
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
