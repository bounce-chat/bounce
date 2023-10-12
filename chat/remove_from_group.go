package chat

import (
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var removeFromGroupMutex sync.Mutex

//
// A remove from group frame is a sync-scoped frame that we use to inform our other sync devices that we've been remove
// after we delete all information associated with a group that is stored locally
//
type removeFromGroup struct {
	ID              uuid.UUID `gorm:"type:uuid;primary_key;"`
	GroupID         uuid.UUID
	UserID          uuid.UUID
	ActorID         uuid.UUID
	Timestamp       int64
	CustomScope     uuid.UUID `msgpack:"-"`
	Signer          string    `msgpack:"-" gorm:"not null"`
	OriginalPayload []byte    `msgpack:"-" gorm:"not null"`
	Signature       []byte    `msgpack:"-" gorm:"not null"`
	payload         []byte
	payloadMutex    sync.Mutex
}

func (rfg *removeFromGroup) BeforeCreate(tx *gorm.DB) error {
	if rfg.ID == uuid.Nil {
		return errors.New("removed from group frames must have an ID assigned before creation")
	}
	return nil
}

func (rfg *removeFromGroup) AfterDelete(tx *gorm.DB) error {
	if rfg.CustomScope != uuid.Nil {
		err := tx.Where("id = ?", rfg.CustomScope).Delete(&customScope{}).Error
		if err != nil {
			return err
		}
	}

	return tx.Where("frame_id = ? AND frame_type = ?", rfg.ID, typeRemoveFromGroup).Delete(&deliveryRecord{}).Error
}

func (rfg *removeFromGroup) getID() uuid.UUID {
	return rfg.ID
}

func (rfg *removeFromGroup) getScope(myID uuid.UUID) int {
	if rfg.UserID == myID {
		return scopeCustom
	}
	return scopeGroup
}

func (rfg *removeFromGroup) getDestination(myID uuid.UUID) uuid.UUID {
	if rfg.UserID == myID {
		return rfg.CustomScope
	}
	return rfg.GroupID
}

func (rfg *removeFromGroup) getType() uint16 {
	return typeRemoveFromGroup
}

func (rfg *removeFromGroup) getPayload() []byte {
	rfg.payloadMutex.Lock()
	defer rfg.payloadMutex.Unlock()

	if len(rfg.payload) == 0 {
		bytes, err := msgpack.Marshal(signedContainer{
			Payload:   rfg.OriginalPayload,
			Signature: rfg.Signature,
			Signer:    rfg.Signer,
		})
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error marshalling remove from group's signed container")
		}
		rfg.payload = bytes
	}

	return rfg.payload
}

func (rfg *removeFromGroup) getAuthor() uuid.UUID {
	return rfg.ActorID
}

func (rfg *removeFromGroup) getTimestamp() int64 {
	return rfg.Timestamp
}

func (b *bounce) handleRemoveFromGroup(peer string, payload []byte) {
	removeFromGroupMutex.Lock()
	defer removeFromGroupMutex.Unlock()

	// Verify and unpack the signed container
	sc, err := b.unpackSignedContainer(payload)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unpacking signed container for remove from group")
		return
	}
	var rfg removeFromGroup
	err = msgpack.Unmarshal(sc.Payload, &rfg)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error unmarshalling remove from group")
	}
	rfg.OriginalPayload = sc.Payload
	rfg.Signature = sc.Signature
	rfg.Signer = sc.Signer

	// Make sure the device that signed this message belongs to the actor
	if !b.signedByUser(sc, rfg.ActorID) {
		log.WithFields(log.Fields{
			"group_id": rfg.GroupID,
			"user_id":  rfg.UserID,
			"actor_id": rfg.ActorID,
			"signer":   rfg.Signer,
		}).Warn("received remove from group not signed by the actor")
	}

	// ack and return if we've already seen it
	var existingRFG removeFromGroup
	err = b.database.Where("id = ?", rfg.ID).First(&existingRFG).Error
	if err == nil {
		b.markDeliveredTo(&existingRFG, peer)
		go b.sendAck(peer, typeRemoveFromGroup, rfg.ID)
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up remove from group")
	}

	// Ensure that the actor is in the group that is being affected
	if !b.userIsInGroup(rfg.ActorID, rfg.GroupID) {
		log.WithFields(log.Fields{
			"group_id": rfg.GroupID,
			"user_id":  rfg.UserID,
			"actor_id": rfg.ActorID,
		}).Warn("remove from group frame has actor that is not in group")
		return
	}

	// Ensure that the user to be removed is in the group
	if !b.userIsInGroup(rfg.UserID, rfg.GroupID) {
		log.WithFields(log.Fields{
			"group_id": rfg.GroupID,
			"user_id":  rfg.UserID,
			"actor_id": rfg.ActorID,
		}).Warn("remove from group frame attempts to remove user that is not in group")
		return
	}

	// If we're being removed, create a custom scope for the group that is going to be deleted
	if rfg.UserID == b.currentUserID() {
		cs, err := b.createCustomScopeFromGroup(rfg.GroupID)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error creating custom scope for group")
		}
		rfg.CustomScope = cs
	}

	// Apply it
	err = b.applyRemoveFromGroup(&rfg)
	if err != nil {
		log.WithFields(log.Fields{
			"errors": err.Error(),
		}).Error("error applying remove from group")
		return
	}

	// Save it
	err = b.database.Create(&rfg).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving remove from group")
	}

	// Mark as delivered to this peer
	b.markDeliveredTo(&rfg, peer)

	// Ack it
	go b.sendAck(peer, typeRemoveFromGroup, rfg.ID)

	// Broadcast it
	b.broadcast(&rfg)
}

func (b *bounce) removeUser(groupID, userID uuid.UUID) error {
	// Create a remove from group frame
	rfg := &removeFromGroup{
		ID:        uuid.New(),
		GroupID:   groupID,
		UserID:    userID,
		ActorID:   b.currentUserID(),
		Timestamp: time.Now().Unix(),
	}

	// Create an update group to inform the UI
	ug := updateGroup{
		ID:        rfg.ID,
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: rfg.Timestamp,
		Type:      updateGroupTypeRemoveUser,
		Data:      userID[:],
	}

	// If we're being removed from the group, custom scope these frames
	if userID == b.currentUserID() {
		cs, err := b.createCustomScopeFromGroup(groupID)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error creating custom scope for group")
		}
		rfg.CustomScope = cs
		ug.CustomScope = cs
	}

	// Sign the remove from group
	var err error
	rfg.OriginalPayload, err = msgpack.Marshal(rfg)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling remove from group")
	}
	sc := b.createSignedContainer(rfg.OriginalPayload)
	rfg.Signature = sc.Signature
	rfg.Signer = sc.Signer

	// Apply the changes
	err = b.applyRemoveFromGroup(rfg)
	if err != nil {
		return err
	}

	// Save to the database
	err = b.database.Create(rfg).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving remove from group")
	}

	// Broadcast
	go b.broadcast(rfg)
	err = b.applyAndBroadcastUpdateGroup(ug)
	if err != nil {
		return err
	}

	return nil
}

func (b *bounce) applyRemoveFromGroup(rfg *removeFromGroup) error {
	// Make sure the actor has permission to do this
	if rfg.UserID != b.currentUserID() {
		// Get the group
		var g group
		err := b.database.Where("id = ?", rfg.GroupID).First(&g).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"group_id": rfg.GroupID,
					"user_id":  rfg.UserID,
				}).Error("cannot remove user from unknown group")
				return err
			} else {
				log.WithFields(log.Fields{
					"group_id": rfg.GroupID,
					"error":    err.Error(),
				}).Fatal("database error looking up group")
			}
		}

		// Check the group permissions to make sure this user can remove other users
		if g.hasAdmins() && g.RestrictUserManagement && !b.isGroupAdmin(g.ID, rfg.ActorID) {
			log.WithFields(log.Fields{
				"user_id": rfg.UserID,
			}).Warn("user attempted to remove user from group without permission")
			return errNoPermissionToManageUsers
		}
	}

	// Look up the group
	var g group
	err := b.database.Where("id = ?", rfg.GroupID).First(&g).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"group_id": rfg.GroupID,
				"user_id":  rfg.UserID,
			}).Error("cannot remove user from unknown group")
			return err
		} else {
			log.WithFields(log.Fields{
				"group_id": rfg.GroupID,
				"error":    err.Error(),
			}).Fatal("database error looking up group")
		}
	}

	// If this frame removes us from a group, all we need to do is delete the group,
	// which will cascade cleaning everything else up
	if rfg.UserID == b.currentUserID() {
		b.userInterface.RemovedFromGroup(RemovedFromGroup{
			Group: g.ID,
			Actor: rfg.ActorID,
		})
		return b.database.Delete(&g).Error
	}

	// Remove this user from the group
	err = b.database.Exec("DELETE FROM group_users WHERE group_id = ? AND user_id = ?", rfg.GroupID, rfg.UserID).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error":    err.Error(),
			"group_id": rfg.GroupID,
			"user_id":  rfg.UserID,
		}).Fatal("database error removing user from group")
	}

	// Remove them from admin list if they are an admin
	if b.isGroupAdmin(rfg.GroupID, rfg.UserID) {
		b.removeGroupAdmin(rfg.GroupID, rfg.UserID)
	}

	// Get the user
	var u user
	err = b.database.Preload(clause.Associations).Where("id = ?", rfg.UserID).First(&u).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"group_id": rfg.GroupID,
				"user_id":  rfg.UserID,
			}).Error("user not found when attempting to remove user from group")
			return err
		} else {
			log.WithFields(log.Fields{
				"error":   err.Error(),
				"user_id": rfg.UserID,
			}).Fatal("database error looking up user")
		}
	}

	// Delete all delivery records for this user for items in this group
	for _, dev := range u.Devices {
		// Delete the delivery records for each group message
		gms := []groupMessage{}
		err = b.database.Where("destination = ?", rfg.GroupID).Find(&gms).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"group_id": rfg.GroupID,
			}).Fatal("database error looking up all group messages for a group")
		}
		for _, gm := range gms {
			err = b.database.Exec("DELETE FROM delivery_records WHERE destination = ? AND frame_type = ? AND frame_id = ?", dev.Address, typeGroupMessage, gm.ID).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error":      err.Error(),
					"group_id":   rfg.GroupID,
					"message_id": gm.ID,
					"device":     dev.Address,
				}).Fatal("database error deleting group message delivery record for user being removed")
			}
		}

		// Delete the delivery records for each update group
		ugs := []updateGroup{}
		err = b.database.Where("target = ?", rfg.GroupID).Find(&ugs).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"group_id": rfg.GroupID,
			}).Fatal("database error looking up all updates for a group")
		}
		for _, ugToDelete := range ugs {
			err = b.database.Exec("DELETE FROM delivery_records WHERE destination = ? AND frame_type = ? AND frame_id = ?", dev.Address, typeUpdateGroup, ugToDelete.ID).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error":           err.Error(),
					"group_id":        rfg.GroupID,
					"update_group_id": ugToDelete.ID,
					"device":          dev.Address,
				}).Fatal("database error deleting update group delivery record for user being removed")
			}
		}

		// Delete the delivery records for the original group creation
		err = b.database.Exec("DELETE FROM delivery_records WHERE destination = ? AND frame_type = ? AND frame_id = ?", dev.Address, typeGroupCreation, rfg.GroupID).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"group_id": rfg.GroupID,
				"device":   dev.Address,
			}).Fatal("database error deleting group creation delivery record for user being removed")
		}
	}

	return nil
}
