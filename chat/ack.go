package chat

import (
	"errors"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// An ack is a frame that contains any number of frame references.  Acks indicate that a peer has a received
// a frame, and are sent in response to most frames as well as to indicate that frames offered during a
// reference offer have already been delivered to a device.
type ack struct {
	References []frameReference
}

func (a *ack) getType() uint16 {
	return typeAck
}

func (a *ack) getPayload() []byte {
	bytes, err := msgpack.Marshal(a)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal ack")
	}
	return bytes
}

func (b *Bounce) handleAck(peer string, payload []byte, _ bool) (broadcastable, bool) {
	var a ack
	err := msgpack.Unmarshal(payload, &a)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling ack")
		return nil, false
	}

	for _, fr := range a.References {
		// Reference offer delivery records are stored in the reference database
		if fr.Type == typeReferenceOffer {
			err := b.referenceDatabase.Clauses(clause.OnConflict{DoNothing: true}).Create(&deliveryRecord{
				Destination: peer,
				FrameID:     fr.FrameID,
				FrameType:   typeReferenceOffer,
			}).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("error creating delivery record for reference offer")
			}
			continue
		}

		// Append recipient payloads just delete the append recipient intentions in the database
		if fr.Type == typeAppendRecipientPayloads {
			err = b.database.Where("id = ?", fr.FrameID).Delete(&appendRecipient{}).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error deleting append recipient")
			}
			continue
		}

		// All other delivery records are stored in the main database
		b.markFrameDelivered(fr.FrameID, fr.Type, peer)

		// Handle side effects for some types
		if !b.encrypted {
			b.handleAckSideEffects(fr, peer)
		}
	}

	return nil, false
}

func (b *Bounce) handleAckSideEffects(fr frameReference, peer string) {
	switch fr.Type {
	case typeDirectMessage:
		encryptedDeviceCacheMutex.Lock()
		userID, ok := encryptedDeviceCache[peer]
		encryptedDeviceCacheMutex.Unlock()
		if ok {
			b.ui.MessageDelivered(fr.FrameID, userID)
		} else {
			dev, ok := b.getDeviceFromAddress(peer)
			if ok {
				b.ui.MessageDelivered(fr.FrameID, dev.UserID)
			} else {
				log.WithFields(log.Fields{
					"peer": peer,
				}).Warn("direct message acked by unknown peer")
			}
		}
		// If we're waiting to check if this message becomes undeliverable, we can stop that now
		dmDeliveryNotificationMutex.Lock()
		notifier, ok := dmDeliveryNotifications[fr.FrameID]
		if ok {
			notifier <- true
		}
		dmDeliveryNotificationMutex.Unlock()
	case typeGroupMessage:
		encryptedDeviceCacheMutex.Lock()
		userID, ok := encryptedDeviceCache[peer]
		encryptedDeviceCacheMutex.Unlock()
		if ok {
			b.ui.MessageDelivered(fr.FrameID, userID)
		} else {
			dev, ok := b.getDeviceFromAddress(peer)
			if ok {
				b.ui.MessageDelivered(fr.FrameID, dev.UserID)
			} else {
				log.WithFields(log.Fields{
					"peer": peer,
				}).Warn("group message acked by unknown peer")
			}
		}

		// If we're waiting to check if this message becomes undeliverable, we can stop that now
		gmDeliveryNotificationMutex.Lock()
		notifier, ok := gmDeliveryNotifications[fr.FrameID]
		if ok {
			notifier <- true
		}
		gmDeliveryNotificationMutex.Unlock()
	case typeAddUser:
		go b.sendReferences(peer)
	case typeUpdateGroup:
		var ug updateGroup
		err := b.database.First(&ug, "id = ?", fr.FrameID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   fr.FrameID,
					"peer": peer,
				}).Warn("unknown update group acked")
				return
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error querying for update group")
			}
		}

		if ug.CustomScope != uuid.Nil {
			var cs customScope
			err = b.database.First(&cs, "id = ?", ug.CustomScope).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					log.WithFields(log.Fields{
						"id":   ug.ID,
						"peer": peer,
					}).Debug("update group missing custom scope")
					err = b.database.Delete(&ug).Error
					if err != nil {
						log.WithFields(log.Fields{
							"error": err.Error(),
						}).Fatal("database error deleting update group")
					}
					return
				} else {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Fatal("database error querying for custom scope")
				}
			}

			allDelivered := true
			for _, addr := range cs.addresses() {
				if b.devicePool.isRevoked(addr) {
					continue
				}
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

func (b *Bounce) sendAck(peer string, frameType uint16, frameID uuid.UUID) {
	b.sendDirect(peer, &ack{
		References: []frameReference{frameReference{Type: frameType, FrameID: frameID}},
	})
}
