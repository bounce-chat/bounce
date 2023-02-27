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

var updateGroupMutex sync.Mutex

// TODO: don't export
const UPDATE_GROUP_TYPE_CHANGE_NAME = uint16(0)
const UPDATE_GROUP_TYPE_ADD_USER = uint16(1)
const UPDATE_GROUP_TYPE_REMOVE_USER = uint16(2)
const UPDATE_GROUP_TYPE_CHANGE_MUTED_UNTIL = uint16(3)
const UPDATE_GROUP_TYPE_CHANGE_RETENTION = uint16(4)
const UPDATE_GROUP_TYPE_SET_CLEAR_BEFORE = uint16(5)

var ERR_UPDATE_GROUP_WITH_UNKNOWN_TYPE = errors.New("update group has unknown update type")
var ERR_INVALID_GROUP_NAME = errors.New("invalid group name")
var ERR_MUTED_UNTIL_ONLY_MUTABLE_BY_SELF = errors.New("group muted until settings can only be modified by current user")
var ERR_USER_NOT_FOUND = errors.New("no user found with that ID")
var ERR_USER_HAS_INVALID_DEVICE_GROUP = errors.New("user has invalid device group")

type updateGroup struct {
	ID           uuid.UUID `gorm:"type:uuid;primary_key;"`
	Actor        uuid.UUID
	Target       uuid.UUID
	Timestamp    int64
	Type         uint16
	Data         []byte
	Signer       string `msgpack:"-"`
	Payload      []byte `msgpack:"-"` // TODO: rename to marshalled or something
	Signature    []byte `msgpack:"-"`
	payload      []byte
	payloadMutex sync.Mutex
	// should also have a string method on here for the frontend?  internationalization issues there though
	//   each type could have it's own structure exported to the frontend for this too
}

func (ug *updateGroup) BeforeCreate(tx *gorm.DB) error {
	if ug.ID == uuid.Nil {
		log.Fatal("attempt to create update group with nil ID, ID must be set before creation")
	}

	return nil
}

func (ug *updateGroup) getID() uuid.UUID {
	return ug.ID
}

func (ug *updateGroup) getScope(myID uuid.UUID) int {
	if ug.Type == UPDATE_GROUP_TYPE_CHANGE_MUTED_UNTIL {
		return scopeSync
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
			Payload:   ug.Payload,
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

func (ug *updateGroup) getTimestamp() int64 {
	return ug.Timestamp
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
	ug.Payload = sc.Payload
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

	// Make sure the author is in the group
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
		go b.sendDirectAck(peer, &ack{
			UpdateGroups: ug.ID.String(),
		})
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up update group")
	}

	// Apply this update locally
	err = b.saveAndApplyUpdateGroup(ug)
	if err != nil {
		log.WithFields(log.Fields{
			"peer":  peer,
			"type":  ug.Type,
			"error": err.Error(),
		}).Error("error applying update group")
		return
	}

	// Ack it
	go b.sendDirectAck(peer, &ack{
		UpdateGroups: ug.ID.String(),
	})

	// Mark that the peer that send this update already has it
	b.markDeliveredTo(&ug, peer)

	// Broadcast it
	go b.broadcast(&ug)
}

// TODO: does this need to be mutexed, as opposed to the handler, since both are using it?
func (b *bounce) saveAndApplyUpdateGroup(ug updateGroup) error {
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

	switch ug.Type {
	case UPDATE_GROUP_TYPE_CHANGE_NAME:
		return b.saveAndApplyUpdateGroupChangeName(g, ug)
	case UPDATE_GROUP_TYPE_ADD_USER:
		return b.saveAndApplyUpdateGroupAddUser(g, ug)
	case UPDATE_GROUP_TYPE_REMOVE_USER:
	case UPDATE_GROUP_TYPE_CHANGE_MUTED_UNTIL:
		return b.saveAndApplyUpdateGroupChangeMutedUntil(g, ug)
	case UPDATE_GROUP_TYPE_CHANGE_RETENTION:
		return b.saveAndApplyUpdateGroupChangeRetention(g, ug)
	case UPDATE_GROUP_TYPE_SET_CLEAR_BEFORE:
		return b.saveAndApplyUpdateGroupSetClearBefore(g, ug)
	default:
		log.WithFields(log.Fields{
			"type": ug.Type,
		}).Warn("received update group with unknown type")
		return ERR_UPDATE_GROUP_WITH_UNKNOWN_TYPE
	}

	return nil
}

func (b *bounce) saveAndApplyUpdateGroupChangeName(g group, ug updateGroup) error {
	// Make sure the name is valid
	newName := string(ug.Data)
	if !b.validGroupName(newName) {
		log.WithFields(log.Fields{
			"name": newName,
		}).Error("cannot apply update group with invalid name")
		return ERR_INVALID_GROUP_NAME
	}

	// Make sure the user has the permissions needed to change the group name
	//TODO

	// Save the update group
	err := b.database.Create(&ug).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving update group")
	}

	// Check to make sure there isn't a more recent change we're already aware of
	var moreRecentUpdates bool
	err = b.database.Table("update_groups").
		Select("count(*) >= 1").
		Where("target = ? AND type = ? AND timestamp > ?", ug.Target, ug.Type, ug.Timestamp).
		Find(&moreRecentUpdates).
		Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error checking for more recent update groups")
	} // TODO: DRY?

	// Apply the update if it is the most recent one
	if !moreRecentUpdates {
		err = b.database.Model(&g).Update("name", newName).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error updating group name")
		}

		// Inform the UI
		b.userInterface.RenameGroup(g.ID, ug.Actor, newName)
	}

	return nil
}

func (b *bounce) saveAndApplyUpdateGroupChangeMutedUntil(g group, ug updateGroup) error {
	// Notification settings can only be changed by sync devices
	if ug.Actor != b.currentUserID() {
		return ERR_MUTED_UNTIL_ONLY_MUTABLE_BY_SELF
	}

	// Save the update group
	err := b.database.Create(&ug).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving update group")
	}

	// Check to make sure there isn't a more recent change we're already aware of
	var moreRecentUpdates bool
	err = b.database.Table("update_groups").
		Select("count(*) >= 1").
		Where("target = ? AND type = ? AND timestamp > ?", ug.Target, ug.Type, ug.Timestamp).
		Find(&moreRecentUpdates).
		Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error checking for more recent update groups")
	} // TODO: DRY?

	// Decode the new muted until value
	mutedUntil := int64(binary.LittleEndian.Uint64(ug.Data))

	// Apply the update if it is the most recent one
	if !moreRecentUpdates {
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
	//TODO

	// Save the update group
	err := b.database.Create(&ug).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving update group")
	}

	// Check to make sure there isn't a more recent change we're already aware of
	var moreRecentUpdates bool
	err = b.database.Table("update_groups").
		Select("count(*) >= 1").
		Where("target = ? AND type = ? AND timestamp > ?", ug.Target, ug.Type, ug.Timestamp).
		Find(&moreRecentUpdates).
		Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error checking for more recent update groups")
	} // TODO: DRY?

	// Decode the new retention value
	retention := int64(binary.LittleEndian.Uint64(ug.Data))

	// Apply the update if it is the most recent one
	if !moreRecentUpdates {
		err = b.database.Model(&g).Update("retention", retention).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error updating group retention")
		}

		// Inform the UI
		b.userInterface.GroupRetentionChanged(g.ID, ug.Actor, retention)

		// TODO: we should notify the UI even if there are more recent updates once the UI understands how to
		// insetion sort everything by timestamp
	}

	return nil
}

func (b *bounce) saveAndApplyUpdateGroupSetClearBefore(g group, ug updateGroup) error {
	// TODO: make sure permissions are correct, who can clear chat history?  probably best to match message sending permissions

	// Save the update group
	err := b.database.Create(&ug).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving update group")
	}

	// Decode the new retention value
	clearBefore := int64(binary.LittleEndian.Uint64(ug.Data))

	gms := []GroupMessage{}
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
	b.userInterface.GroupChatHistoryCleared(g.ID, ug.Actor)

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

func (b *bounce) saveAndApplyUpdateGroupAddUser(g group, ug updateGroup) error {
	// Unmarshall the new user
	var u user
	err := msgpack.Unmarshal(ug.Payload, &u)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling user")
		return err
	}

	if u.ID == b.currentUserID() {
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
			return ERR_USER_HAS_INVALID_DEVICE_GROUP
		}

		// Save the user and their devices if we don't have them
		err = b.database.Transaction(func(tx *gorm.DB) error {
			// TODO: if the users are new, shouldn't we ack them as well?
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

	return nil
}

func (b *bounce) renameGroup(groupID uuid.UUID, newName string) error {
	return b.applyAndBroadcastUpdateGroup(updateGroup{
		ID:        uuid.New(),
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      UPDATE_GROUP_TYPE_CHANGE_NAME,
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
		Type:      UPDATE_GROUP_TYPE_CHANGE_MUTED_UNTIL,
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
		Type:      UPDATE_GROUP_TYPE_CHANGE_RETENTION,
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
		Type:      UPDATE_GROUP_TYPE_SET_CLEAR_BEFORE,
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
			return ERR_USER_NOT_FOUND
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
		Type:      UPDATE_GROUP_TYPE_ADD_USER,
		Data:      newUserBytes,
	})
	if err != nil {
		return err
	}

	// Do a reference flow with this new user to send over all the details of this group
	addresses, err := b.getOnlinePeerAddresses(newUser.ID)
	if err != nil {
		log.WithFields(log.Fields{
			"user_id": newUser.ID,
			"error":   err.Error(),
		}).Error("error looking up online addresses for user")
	}
	for _, address := range addresses {
		b.sendReferences(address)
	}

	return nil
}

func (b *bounce) changeGroupNotificationSettings(group uuid.UUID, enabled bool) {
	log.WithFields(log.Fields{
		"thread":                group,
		"notifications_enabled": enabled,
	}).Info("UI wants to change notification settings")
}

func (b *bounce) applyAndBroadcastUpdateGroup(ug updateGroup) error {
	// Create the signed container for this update
	var err error
	ug.Payload, err = msgpack.Marshal(&ug)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling group update")
	}
	sc := b.createSignedContainer(ug.Payload)
	ug.Signature = sc.Signature
	ug.Signer = sc.Signer

	// Apply the update locally
	err = b.saveAndApplyUpdateGroup(ug)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error applying update group")
		return err
	}

	// Broadcast
	go b.broadcast(&ug)

	return nil
}
