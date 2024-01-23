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

//
// An ack is a frame that contains any number of frame references.  Acks indicate that a peer has a received
// a frame, and are sent in response to most frames as well as to indicate that frames offered during a
// reference offer have already been delivered to a device.
//
type ack struct {
	References   []frameReference
	payload      []byte
	payloadMutex sync.Mutex
}

func (a *ack) getType() uint16 {
	return typeAck
}

func (a *ack) getPayload() []byte {
	a.payloadMutex.Lock()
	defer a.payloadMutex.Unlock()

	if len(a.payload) == 0 {
		bytes, err := msgpack.Marshal(a)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal ack")
		}
		a.payload = bytes
	}
	return a.payload
}

func (b *bounce) handleAck(peer string, payload []byte) {
	var a ack
	err := msgpack.Unmarshal(payload, &a)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling ack")
		return
	}

	ackedIDs := referencedIDs(a.References)

	b.handleAckDirectMessages(peer, ackedIDs[typeDirectMessage])
	b.handleAckGroupMessages(peer, ackedIDs[typeGroupMessage])
	b.handleAckReferenceOffers(peer, ackedIDs[typeReferenceOffer])
	b.handleAckUpdateDMs(peer, ackedIDs[typeUpdateDM])
	b.handleAckDevices(peer, ackedIDs[typeDevice])
	b.handleAckAddUsers(peer, ackedIDs[typeAddUser])
	b.handleAckGroupCreations(peer, ackedIDs[typeGroupCreation])
	b.handleAckUpdateGroups(peer, ackedIDs[typeUpdateGroup])
}

func (b *bounce) sendAck(peer string, frameType uint16, frameID uuid.UUID) {
	b.sendDirect(peer, &ack{
		References: []frameReference{frameReference{Type: frameType, FrameID: frameID}},
	})
}

func (b *bounce) handleAckDirectMessages(peer string, ids []uuid.UUID) {
	for _, dmID := range ids {
		var dm directMessage
		err := b.database.First(&dm, "id = ?", dmID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   dmID,
					"peer": peer,
				}).Error("ack of unknown direct message from peer")
				continue
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error querying for direct message")
			}
		}
		b.markDeliveredTo(&dm, peer)

		// If we're waiting to check if this message becomes undeliverable, we can stop that now
		dmDeliveryNotificationMutex.Lock()
		notifier, ok := dmDeliveryNotifications[dmID]
		if ok {
			notifier <- true
		}
		dmDeliveryNotificationMutex.Unlock()

		// Now that we know the message has been delivered somewhere, if the message expires we start the clock on retention
		// by setting the absolute time the message should be delete at as now + the retention time
		if dm.RetentionSeconds != 0 && dm.DeleteAt == 0 {
			deleteAt := time.Now().Unix() + dm.RetentionSeconds
			err := b.database.Model(&dm).Update("delete_at", deleteAt).Error
			if err != nil {
				log.WithFields(log.Fields{
					"message_id": dm.ID,
					"error":      err.Error(),
				}).Fatal("error updating delete_at of acked direct message")
			}
			b.userInterface.UpdateMessageDeletionTime(dm.ID, deleteAt)
			go b.deleteDirectMessageAt(deleteAt, dm.ID)
		}
	}
}

func (b *bounce) handleAckGroupMessages(peer string, ids []uuid.UUID) {
	for _, gmID := range ids {
		var gm groupMessage
		err := b.database.First(&gm, "id = ?", gmID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   gmID,
					"peer": peer,
				}).Error("ack of unknown group message from peer")
				continue
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error querying for group message")
			}
		}
		b.markDeliveredTo(&gm, peer)

		// If we're waiting to check if this message becomes undeliverable, we can stop that now
		gmDeliveryNotificationMutex.Lock()
		notifier, ok := gmDeliveryNotifications[gmID]
		if ok {
			notifier <- true
		}
		gmDeliveryNotificationMutex.Unlock()

		// Now that we know the message has been delivered, if the message expires we start the clock on retention
		// by setting the absolute time the message should be delete at as now + the retention time
		if gm.RetentionSeconds != 0 && gm.DeleteAt == 0 {
			deleteAt := time.Now().Unix() + gm.RetentionSeconds
			err := b.database.Model(&gm).Update("delete_at", deleteAt).Error
			if err != nil {
				log.WithFields(log.Fields{
					"message_id": gm.ID,
					"error":      err.Error(),
				}).Fatal("error updating delete_at of acked group message")
			}
			b.userInterface.UpdateMessageDeletionTime(gm.ID, deleteAt)
			go b.deleteGroupMessageAt(deleteAt, gm.ID)
		}
	}
}

func (b *bounce) handleAckReferenceOffers(peer string, ids []uuid.UUID) {
	for _, roID := range ids {
		// Reference offers are not stored in the database, so there's nothing to look up.  We create a delivery record inside the
		// reference database manually to track delivery
		err := b.referenceDatabase.Clauses(clause.OnConflict{DoNothing: true}).Create(&deliveryRecord{
			Destination: peer,
			FrameID:     roID,
			FrameType:   typeReferenceOffer,
		}).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error creating delivery record for reference offer")
		}
	}
}

func (b *bounce) handleAckUpdateDMs(peer string, ids []uuid.UUID) {
	for _, udID := range ids {
		var ud updateDM
		err := b.database.First(&ud, "id = ?", udID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   udID,
					"peer": peer,
				}).Warn("unknown update DM acked")
				continue
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error querying for update DM")
			}
		} else {
			b.markDeliveredTo(&ud, peer)
		}
	}
}

func (b *bounce) handleAckDevices(peer string, ids []uuid.UUID) {
	for _, deviceID := range ids {
		var dev device
		err := b.database.Preload(clause.Associations).First(&dev, "id = ?", deviceID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   deviceID,
					"peer": peer,
				}).Warn("unknown device acked")
				continue
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error querying for device")
			}
		} else {
			b.markDeliveredTo(&dev, peer)
		}
	}
}

func (b *bounce) handleAckAddUsers(peer string, ids []uuid.UUID) {
	for _, addUserID := range ids {
		var au addUser
		err := b.database.First(&au, "id = ?", addUserID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   addUserID,
					"peer": peer,
				}).Warn("unknown add user acked")
				continue
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error querying for add user")
			}
		} else {
			b.markDeliveredTo(&au, peer)
		}
	}

	// We might have just learned about who this peer belongs to, check if we need to offer references
	if len(ids) > 0 {
		go b.sendReferences(peer)
	}
}

func (b *bounce) handleAckGroupCreations(peer string, ids []uuid.UUID) {
	for _, groupCreationID := range ids {
		var gc groupCreation
		err := b.database.First(&gc, "id = ?", groupCreationID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   groupCreationID,
					"peer": peer,
				}).Warn("unknown group creation acked")
				continue
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error querying for group creation")
			}
		} else {
			b.markDeliveredTo(&gc, peer)
		}
	}
}

func (b *bounce) handleAckUpdateGroups(peer string, ids []uuid.UUID) {
	for _, updateGroupID := range ids {
		var ug updateGroup
		err := b.database.First(&ug, "id = ?", updateGroupID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   updateGroupID,
					"peer": peer,
				}).Warn("unknown update group acked")
				continue
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error querying for update group")
			}
		} else {
			b.markDeliveredTo(&ug, peer)

			// If this updateGroup is custom scoped and we've delivered it to all recipients we can delete it
			if ug.CustomScope != uuid.Nil {
				var cs customScope
				err = b.database.First(&cs, "id = ?", ug.CustomScope).Error
				if err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						log.WithFields(log.Fields{
							"id":   ug.ID,
							"peer": peer,
						}).Warn("update group missing custom scope")
						continue
					} else {
						log.WithFields(log.Fields{
							"error": err.Error(),
						}).Fatal("database error querying for custom scope")
					}
				}

				allDelivered := true
				for _, addr := range cs.addresses() {
					if !b.isDeliveredTo(&ug, addr) {
						allDelivered = false
					}
				}

				if allDelivered {
					err = b.database.Delete(&ug).Error
					if err != nil {
						log.WithFields(log.Fields{
							"error": err.Error(),
						}).Fatal("database error deleting update group")
					}
				}

			}
		}
	}
}
