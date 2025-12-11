package chat

import (
	"errors"
	"sync"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// An ack is a frame that contains any number of frame references.  Acks indicate that a peer has a received
// a frame, and are sent in response to most frames as well as to indicate that frames offered during a
// reference offer have already been delivered to a device.
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

func (b *Bounce) handleAck(peer string, payload []byte, _ bool) (broadcastable, bool) {
	var a ack
	err := msgpack.Unmarshal(payload, &a)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling ack")
		return nil, false
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
	b.handleAckConfirmations(peer, ackedIDs[typeConfirmation])
	b.handleAckUpdateUsers(peer, ackedIDs[typeUpdateUser])
	b.handleAckUpdateDevices(peer, ackedIDs[typeUpdateDevice])
	b.handleAckReadReceipts(peer, ackedIDs[typeReadReceipt])
	b.handleAckUpdateSettings(peer, ackedIDs[typeUpdateSettings])
	b.handleAckFiles(peer, ackedIDs[typeFile])
	b.handleAckChunkOffers(peer, ackedIDs[typeChunkOffer])

	return nil, false
}

func (b *Bounce) sendAck(peer string, frameType uint16, frameID uuid.UUID) {
	b.sendDirect(peer, &ack{
		References: []frameReference{frameReference{Type: frameType, FrameID: frameID}},
	})
}

func (b *Bounce) handleAckDirectMessages(peer string, ids []uuid.UUID) {
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

		dev, ok := b.getDeviceFromAddress(peer) // TODO: also let UI know about delivery to encrypted device?
		if ok {
			b.ui.MessageDelivered(dmID, dev.UserID)
		} else {
			log.WithFields(log.Fields{
				"peer": peer,
			}).Warn("direct message acked by unknown peer")
		}

		// If we're waiting to check if this message becomes undeliverable, we can stop that now
		dmDeliveryNotificationMutex.Lock()
		notifier, ok := dmDeliveryNotifications[dmID]
		if ok {
			notifier <- true
		}
		dmDeliveryNotificationMutex.Unlock()
	}
}

func (b *Bounce) handleAckGroupMessages(peer string, ids []uuid.UUID) {
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

		dev, ok := b.getDeviceFromAddress(peer) // TODO: also let UI know about delivery to encrypted device?
		if ok {
			b.ui.MessageDelivered(gmID, dev.UserID)
		} else {
			log.WithFields(log.Fields{
				"peer": peer,
			}).Warn("group message acked by unknown peer")
		}

		// If we're waiting to check if this message becomes undeliverable, we can stop that now
		gmDeliveryNotificationMutex.Lock()
		notifier, ok := gmDeliveryNotifications[gmID]
		if ok {
			notifier <- true
		}
		gmDeliveryNotificationMutex.Unlock()
	}
}

func (b *Bounce) handleAckReferenceOffers(peer string, ids []uuid.UUID) {
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

func (b *Bounce) handleAckUpdateDMs(peer string, ids []uuid.UUID) {
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

func (b *Bounce) handleAckDevices(peer string, ids []uuid.UUID) {
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

func (b *Bounce) handleAckAddUsers(peer string, ids []uuid.UUID) {
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

func (b *Bounce) handleAckGroupCreations(peer string, ids []uuid.UUID) {
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

func (b *Bounce) handleAckUpdateGroups(peer string, ids []uuid.UUID) {
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
						}).Debug("update group missing custom scope")
						err = b.database.Delete(&ug).Error
						if err != nil {
							log.WithFields(log.Fields{
								"error": err.Error(),
							}).Fatal("database error deleting update group")
						}
						continue
					} else {
						log.WithFields(log.Fields{
							"error": err.Error(),
						}).Fatal("database error querying for custom scope")
					}
				}

				allDelivered := true
				for _, addr := range cs.addresses() {
					if _, revoked := b.devicePool.revokedDevices[addr]; revoked {
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
}

func (b *Bounce) handleAckConfirmations(peer string, ids []uuid.UUID) {
	for _, confirmationID := range ids {
		var c confirmation
		err := b.database.First(&c, "id = ?", confirmationID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   confirmationID,
					"peer": peer,
				}).Warn("unknown confirmation acked")
				continue
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error querying for confirmation")
			}
		} else {
			b.markDeliveredTo(&c, peer)
		}
	}
}

func (b *Bounce) handleAckUpdateUsers(peer string, ids []uuid.UUID) {
	for _, updateUserID := range ids {
		var uu updateUser
		err := b.database.First(&uu, "id = ?", updateUserID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   updateUserID,
					"peer": peer,
				}).Warn("unknown update user acked")
				continue
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error querying for update user")
			}
		} else {
			b.markDeliveredTo(&uu, peer)
		}
	}
}

func (b *Bounce) handleAckUpdateDevices(peer string, ids []uuid.UUID) {
	for _, updateDeviceID := range ids {
		var ud updateDevice
		err := b.database.First(&ud, "id = ?", updateDeviceID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   updateDeviceID,
					"peer": peer,
				}).Warn("unknown update device acked")
				continue
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error querying for update device")
			}
		} else {
			b.markDeliveredTo(&ud, peer)
		}
	}
}

func (b *Bounce) handleAckReadReceipts(peer string, ids []uuid.UUID) {
	for _, readReceiptID := range ids {
		var rr readReceipt
		err := b.database.First(&rr, "id = ?", readReceiptID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   readReceiptID,
					"peer": peer,
				}).Warn("unknown read receipt acked")
				continue
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error querying for read receipt")
			}
		} else {
			b.markDeliveredTo(&rr, peer)
		}
	}
}

func (b *Bounce) handleAckUpdateSettings(peer string, ids []uuid.UUID) {
	for _, updateSettingsID := range ids {
		var us updateSettings
		err := b.database.First(&us, "id = ?", updateSettingsID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   updateSettingsID,
					"peer": peer,
				}).Warn("unknown update settings acked")
				continue
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error querying for update settings")
			}
		} else {
			b.markDeliveredTo(&us, peer)
		}
	}
}

func (b *Bounce) handleAckFiles(peer string, ids []uuid.UUID) {
	for _, fileID := range ids {
		var f file
		err := b.database.First(&f, "id = ?", fileID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   fileID,
					"peer": peer,
				}).Warn("unknown file acked")
				continue
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error querying for file")
			}
		} else {
			b.markDeliveredTo(&f, peer)
		}
	}
}

func (b *Bounce) handleAckChunkOffers(peer string, ids []uuid.UUID) {
	for _, chunkOfferID := range ids {
		var co chunkOffer
		err := b.database.First(&co, "id = ?", chunkOfferID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   chunkOfferID,
					"peer": peer,
				}).Warn("unknown chunk offer acked")
				continue
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error querying for chunk offer")
			}
		} else {
			b.markDeliveredTo(&co, peer)
		}
	}
}
