package chat

import (
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
)

var updateGroupMutex sync.Mutex

const UPDATE_GROUP_TYPE_CHANGE_NAME = uint16(0)
const UPDATE_GROUP_TYPE_ADD_USER = uint16(1)
const UPDATE_GROUP_TYPE_REMOVE_USER = uint16(2)
const UPDATE_GROUP_TYPE_CHANGE_NOTIFICATION_SETTINGS = uint16(3)

var ERR_UPDATE_GROUP_WITH_UNKNOWN_TYPE = errors.New("update group has unknown update type")
var ERR_INVALID_GROUP_NAME = errors.New("invalid group name")

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
	ug.ID = uuid.New()

	return nil
}

func (ug *updateGroup) getID() uuid.UUID {
	return ug.ID
}

func (ug *updateGroup) getScope(myID uuid.UUID) int {
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

	// Look up the device that sent it
	srcDevice, exists := b.getDeviceFromAddress(peer)
	if !exists {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("ignoring a group update sent from an unknown device")
		return
	}

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

	// Make sure the peer that delivered this message is part of the group
	if !b.userIsInGroup(srcDevice.UserID, ug.Target) {
		log.WithFields(log.Fields{
			"user":   srcDevice.UserID,
			"device": srcDevice.ID,
			"group":  ug.Target,
		}).Warn("device sent an update for a group that the device's user is not a part of, ignoring")
		return
	}

	// Make sure the author is in the group
	if !b.userIsInGroup(ug.Actor, ug.Target) {
		log.WithFields(log.Fields{
			"user":   srcDevice.UserID,
			"device": srcDevice.ID,
			"group":  ug.Target,
		}).Warn("user sent an update for a group that the user is not a part of, ignoring")
		return
	}

	// If we already have this update, we just mark that this peer has it too and return
	var existingUG updateGroup
	err = b.database.Where("id = ?", ug.ID).First(&existingUG).Error
	if err == nil {
		b.markDeliveredTo(&existingUG, peer)
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
			"user":   srcDevice.UserID,
			"device": srcDevice.ID,
			"type":   ug.Type,
			"error":  err.Error(),
		}).Error("error applying update group")
		return
	}

	// Ack it
	//go b.broadcast(&ack{
	//	destination:   srcDevice.ID,
	//	GroupUpdates:  ug.ID.String(),
	//})

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

	//err = b.database.Create(&ug).Error // TODO: have to make sure the rules for applying it check out before saving

	// Apply the change, unless we have a more recent update of the same type, in which case just save it
	// (this actually only applies to certain types of updates, like name changes, user additions are always going to be respected)
	// but: what happens if two different people add the same person to the group?  display both, but ignore?  only display the first?
	// also: check permissions in here

	switch ug.Type {
	case UPDATE_GROUP_TYPE_CHANGE_NAME:
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
		err = b.database.Create(&ug).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error saving update group")
		}

		// Check to make sure there isn't a more recent name change we're already aware of
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
	case UPDATE_GROUP_TYPE_ADD_USER:
	case UPDATE_GROUP_TYPE_REMOVE_USER:
	case UPDATE_GROUP_TYPE_CHANGE_NOTIFICATION_SETTINGS:
	default:
		log.WithFields(log.Fields{
			"type": ug.Type,
		}).Warn("received update group with unknown type")
		return ERR_UPDATE_GROUP_WITH_UNKNOWN_TYPE
	}

	return nil
}

func (b *bounce) renameGroup(groupID uuid.UUID, newName string) error {
	return b.applyAndBroadcastUpdateGroup(updateGroup{
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Type:      UPDATE_GROUP_TYPE_CHANGE_NAME,
		Data:      []byte(newName),
	})
}

func (b *bounce) addUserToGroup(groupID, userID uuid.UUID) {
	log.WithFields(log.Fields{
		"group": groupID,
		"user":  userID,
	}).Info("UI wants to add user to group")
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
