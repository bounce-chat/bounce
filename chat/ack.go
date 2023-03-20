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

// DirectMessages are comma separated for consistency with reference offers, which must do this
// since SQLite doesn't support slices
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

	b.handleAckReferenceOffers(peer, ackedIDs[typeCatchUp])
	b.handleAckDirectMessages(peer, ackedIDs[typeDirectMessage])
	b.handleAckGroupMessages(peer, ackedIDs[typeGroupMessage])
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
		var dm DirectMessage
		err := b.database.First(&dm, "id = ?", dmID).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
				"peer":  peer,
			}).Error("ack of unknown DM from peer")
			// TODO: could be abuse attempted to waste time hitting the database, perhaps should bail / reset connection
			continue
		}
		// TODO: confirm the device should be able to see this DM?
		b.markDeliveredTo(&dm, peer)

		// Now that we know the message has been delivered, if the message expires we start the clock on retention
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
		}
	}
}

func (b *bounce) handleAckGroupMessages(peer string, ids []uuid.UUID) {
	for _, gmID := range ids {
		var gm GroupMessage
		err := b.database.First(&gm, "id = ?", gmID).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
				"peer":  peer,
			}).Error("ack of unknown GM from peer")
			// TODO: could be abuse attempted to waste time hitting the database, perhaps should bail / reset connection
			continue
		}
		// TODO: confirm the device should be able to see this DM
		b.markDeliveredTo(&gm, peer)

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
		}
	}
}

func (b *bounce) handleAckReferenceOffers(peer string, ids []uuid.UUID) {
	for _, roID := range ids {
		// Mark this catch up as delivered so we can stop broadcasting it
		err := b.database.Clauses(clause.OnConflict{DoNothing: true}).Create(&deliveryRecord{
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
				}).Warn("unknown update DM settings acked")
			} else {
				log.WithFields(log.Fields{
					"id":    udID,
					"error": err.Error(),
				}).Fatal("database error querying for update DM settings")
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
			} else {
				log.WithFields(log.Fields{
					"id":    deviceID,
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
			} else {
				log.WithFields(log.Fields{
					"id":    addUserID,
					"error": err.Error(),
				}).Fatal("database error querying for add user")
			}
		} else {
			b.markDeliveredTo(&au, peer)
		}
	}

	// TODO: do a reference flow since we might have just added them?
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
			} else {
				log.WithFields(log.Fields{
					"id":    groupCreationID,
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
			} else {
				log.WithFields(log.Fields{
					"id":    updateGroupID,
					"error": err.Error(),
				}).Fatal("database error querying for update group")
			}
		} else {
			b.markDeliveredTo(&ug, peer)
		}
	}
}
