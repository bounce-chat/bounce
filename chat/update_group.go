package chat

import (
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
)

var updateGroupMutex sync.Mutex

const UPDATE_GROUP_TYPE_CHANGE_NAME = uint16(0)
const UPDATE_GROUP_TYPE_ADD_USER = uint16(1)
const UPDATE_GROUP_TYPE_REMOVE_USER = uint16(2)
const UPDATE_GROUP_TYPE_CHANGE_NOTIFICATION_SETTINGS = uint16(3)

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
	// this will require check if we have a more recent update of this type when applying (but we don't need to cache that anywhere)
	// should also have a string method on here for the frontend?  internationalization issues there though
	//   each type could have it's own structure exported to the frontend for this too
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
	var sc signedContainer
	err := msgpack.Unmarshal(payload, &sc)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling update group signed container")
		return
	}

	if !b.validSignedContainer(sc) {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("update group received with invalid signature, ignoring")
		return
	}

	var ug updateGroup
	err = msgpack.Unmarshal(sc.Payload, &ug)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error unmarshalling update group")
	}

	// Make sure the author is in the group and has the correct permissions
	// TODO

	// Make sure the peer that delivered this message is part of the group
	err = b.database.Table("group_users").
		Select("count(*) = 1").
		Where("user_id = ? AND group_id", srcDevice.UserID, ug.Target).
		Find(&exists).
		Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error checking if source device is in group while handling update group")
	}
	if !exists {
		log.WithFields(log.Fields{
			"user":   srcDevice.UserID,
			"device": srcDevice.ID,
			"group":  ug.Target,
		}).Warn("device sent an update for a group that the device's user is not a part of, ignoring")
		return
	}

	// If we already have this update, we just mark that this peer has it too and return

	// Otherwise, we save it and apply the changes
	//err = b.database.Create(&ug).Error

	// Apply the change, unless we have a more recent update of the same type, in which case just save it
	// (this actually only applies to certain types of updates, like name changes, user additions are always going to be respected)
	// but: what happens if two different people add the same person to the group?  display both, but ignore?  only display the first?
	switch ug.Type {
	case UPDATE_GROUP_TYPE_CHANGE_NAME:
	case UPDATE_GROUP_TYPE_ADD_USER:
	case UPDATE_GROUP_TYPE_REMOVE_USER:
	case UPDATE_GROUP_TYPE_CHANGE_NOTIFICATION_SETTINGS:
	default:
		log.WithFields(log.Fields{
			"type": ug.Type,
		}).Warn("received update group with unknown type")
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

func (b *bounce) renameGroup(groupID uuid.UUID, newName string) {
	// TODO: make sure the new name is valid (length, character set, etc)

	b.broadcastUpdateGroup(&updateGroup{
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

func (b *bounce) broadcastUpdateGroup(ug *updateGroup) {
	// Create the signed container for this update
	var err error
	ug.Payload, err = msgpack.Marshal(ug)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling group update")
	}
	sc := b.createSignedContainer(ug.Payload)
	ug.Signature = sc.Signature
	ug.Signer = sc.Signer

	// Apply this update locally using the network handler function
	signedUpdateBytes, err := msgpack.Marshal(sc)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling group update signed container")
	}
	b.handleUpdateGroup(b.network.Address(), signedUpdateBytes)

	// Broadcast
	//go b.broadcast(&update) // TODO: don't need to broadcast because the handler will, make sure this makes sense everywhere
}
