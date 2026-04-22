package chat

import (
	"errors"
	"sync"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var readReceiptTargetTypeString = map[uint16]string{
	typeGroupMessage:  TypeGroupMessage,
	typeDirectMessage: TypeDirectMessage,
	typeUpdateGroup:   TypeUpdateGroup,
	typeUpdateDM:      TypeUpdateDM,
	typeGroupCreation: TypeGroupCreation,
	typeUpdateUser:    TypeUpdateUser,
}

var readReceiptTargetTypeInt = map[string]uint16{
	TypeGroupMessage:  typeGroupMessage,
	TypeDirectMessage: typeDirectMessage,
	TypeUpdateGroup:   typeUpdateGroup,
	TypeUpdateDM:      typeUpdateDM,
	TypeGroupCreation: typeGroupCreation,
	TypeUpdateUser:    typeUpdateUser,
}

var errUnknownReadReceiptTargetType = errors.New("unknown target type for read receipt")

var readReceiptMutex sync.Mutex

type readReceipt struct {
	SignedFrame
	cachedEncoding
	ID          uuid.UUID `gorm:"type:uuid;primary_key;"`
	Actor       uuid.UUID
	Destination uuid.UUID `msgpack:"-"`
	Scope       int       `msgpack:"-"`
	Target      uuid.UUID
	TargetType  uint16
	Timestamp   int64
}

func (rr *readReceipt) BeforeCreate(tx *gorm.DB) error {
	if rr.ID == uuid.Nil {
		return errors.New("read receipt ID must be set before creation")
	}

	return nil
}

func (rr *readReceipt) AfterDelete(tx *gorm.DB) error {
	if rr.ID == uuid.Nil {
		return nil
	}
	return tx.Clauses(clause.Returning{}).Where("frame_id = ? AND frame_type = ?", rr.ID, typeReadReceipt).Delete(&deliveryRecord{}).Error
}

func (rr *readReceipt) getID() uuid.UUID {
	return rr.ID
}

func (rr *readReceipt) getScope(myID uuid.UUID) int {
	return rr.Scope
}

func (rr *readReceipt) getDestination(myID uuid.UUID) uuid.UUID {
	return rr.Destination
}

func (rr *readReceipt) getType() uint16 {
	return typeReadReceipt
}

func (rr *readReceipt) getPayload() []byte {
	rr.payloadMutex.Lock()
	defer rr.payloadMutex.Unlock()

	if len(rr.payload) == 0 {
		bytes, err := msgpack.Marshal(signedContainer{
			Payload:   rr.OriginalPayload,
			Signature: rr.Signature,
			Signer:    rr.Signer,
		})
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error marshalling read receipt's signed container")
		}
		rr.payload = bytes
	}
	return rr.payload
}

func (rr *readReceipt) getAuthor() uuid.UUID {
	return rr.Actor
}

func (rr *readReceipt) getTimestamp() int64 {
	return rr.Timestamp
}

func (b *Bounce) handleReadReceipt(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	readReceiptMutex.Lock()
	defer readReceiptMutex.Unlock()

	// Unpack the signed container
	sc, err := b.unpackSignedContainer(payload)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unpacking signed container for read receipt")
		return nil, false
	}
	var rr readReceipt
	err = msgpack.Unmarshal(sc.Payload, &rr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling read receipt")
		return nil, false
	}
	rr.OriginalPayload = sc.Payload
	rr.Signature = sc.Signature
	rr.Signer = sc.Signer

	// Make sure that the user that created this signed container is the actor
	if !b.signedByUser(sc, rr.Actor) {
		log.WithFields(log.Fields{
			"peer":           peer,
			"actor":          rr.Actor,
			"signing_device": sc.Signer,
		}).Warn("ignoring read receipt that was not signed by the supposed actor")
		return nil, false
	}

	// Ignore anything from a blocked user
	if blockedUser(rr.getAuthor()) {
		log.WithFields(log.Fields{
			"id":     rr.ID,
			"author": rr.getAuthor(),
		}).Warn("ignoring read receipt from blocked user")

		if peerDev, ok := b.getDeviceFromAddress(peer); ok {
			if !blockedUser(peerDev.UserID) {
				go b.sendAck(peer, typeReadReceipt, rr.ID)
			}
		}
		return nil, false
	}

	// Check if it already exists in the database
	var existingRR readReceipt
	err = b.database.Where("id = ?", rr.ID).First(&existingRR).Error
	if err == nil {
		return &existingRR, false
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up read receipt")
	}

	// Assign the scope based on if read receipts are enabled
	targetTypeString, ok := readReceiptTargetTypeString[rr.TargetType]
	if !ok {
		log.WithFields(log.Fields{
			"error": errUnknownReadReceiptTargetType.Error(),
		}).Error("error parsing read receipt")
		return nil, false
	}
	destination, author, scope, err := b.getReadReceiptDestinationAuthorAndScope(rr.Target, targetTypeString)
	if err == nil {
		rr.Destination = destination
		rr.Scope = scope

		// Save to database
		err = b.database.Create(&rr).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error saving read receipt")
		}

		// Update the database and UI
		if rr.Actor == b.currentUserID() {
			b.markSeenInDatabase(rr.Target, targetTypeString)
			if !catchUp {
				b.ui.MessageSeen(rr.Target)
			}
		} else if author == b.currentUserID() {
			if rr.Scope != scopeSync && !catchUp {
				b.ui.ReceivedReadReceipt(ReadReceipt{
					ID:     rr.ID,
					Actor:  rr.Actor,
					Target: rr.Target,
				})
			}
		}

		return &rr, true
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		// A read receipt may have arrived before the message, just save it and ack but don't broadcast,
		// additional details will be saved on this read receipt when the message arrives
		err = b.database.Create(&rr).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error saving read receipt")
		}
		go b.sendAck(peer, typeReadReceipt, rr.ID)

		return nil, false
	} else {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error parsing read receipt")
		return nil, false
	}
}

func (b *Bounce) processEarlyReadReceipts(messageID uuid.UUID, messageType uint16, notify bool) (bool, []ReadReceipt) {
	// Find any read receipts for this message that came early, add missing data, and send to the UI
	var rrs []readReceipt
	err := b.database.Where("target = ? AND target_type = ?", messageID, messageType).Find(&rrs).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error looking up read receipts")
	}

	rrsToNotify := []ReadReceipt{}
	seen := false
	for _, rr := range rrs {
		targetTypeString, ok := readReceiptTargetTypeString[rr.TargetType]
		if !ok {
			log.WithFields(log.Fields{
				"error": errUnknownReadReceiptTargetType.Error(),
			}).Error("error parsing read receipt that arrived before message")

			continue
		}
		destination, author, scope, err := b.getReadReceiptDestinationAuthorAndScope(rr.Target, targetTypeString)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
				"id":    rr.ID,
			}).Error("error getting read receipt data while processing read receipt that arrived before message")

			continue
		}
		rr.Destination = destination
		rr.Scope = scope
		err = b.database.Table("read_receipts").Where("id = ?", rr.ID).Updates(map[string]interface{}{
			"destination": destination,
			"scope":       scope,
		}).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"error": err.Error(),
					"id":    rr.ID,
				}).Error("read receipt not found for update")

				continue
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
					"id":    rr.ID,
				}).Fatal("database error updating read receipt")
			}
		}

		if rr.Actor == b.currentUserID() {
			b.markSeenInDatabase(rr.Target, targetTypeString)
			seen = true
			if notify {
				b.ui.MessageSeen(rr.Target)
			}
		} else if author == b.currentUserID() {
			if rr.Scope != scopeSync {
				exportedRR := ReadReceipt{
					ID:     rr.ID,
					Actor:  rr.Actor,
					Target: rr.Target,
				}
				rrsToNotify = append(rrsToNotify, exportedRR)
				if notify {
					b.ui.ReceivedReadReceipt(exportedRR)
				}
			}
		}

		b.broadcast(&rr)
	}

	return seen, rrsToNotify
}

func (b *Bounce) getReadReceiptDestinationAuthorAndScope(id uuid.UUID, frameType string) (uuid.UUID, uuid.UUID, int, error) {
	var defaultSendReadReceipts bool
	err := b.database.Model(&profileSettings{}).Select("default_send_read_receipts").Where("user_id = ?", b.currentUserID()).First(&defaultSendReadReceipts).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error selecting profile user default send read receipts")
	}

	switch frameType {
	case TypeGroupMessage:
		// Find the group message
		var gm groupMessage
		err := b.database.Select("destination", "author").First(&gm, "id = ?", id).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return uuid.Nil, uuid.Nil, scopeSync, err
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
					"id":    id,
				}).Fatal("database error looking up group message")
			}
		}

		// Find the group
		var g group
		err = b.database.Select("read_receipts_overridden", "read_receipts_enabled").First(&g, "id = ?", gm.Destination).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return uuid.Nil, uuid.Nil, scopeSync, err
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
					"id":    gm.Destination,
				}).Fatal("database error looking up group")
			}
		}

		if (g.ReadReceiptsOverridden && g.ReadReceiptsEnabled) || (!g.ReadReceiptsOverridden && defaultSendReadReceipts) {
			return gm.Destination, gm.Author, scopeGroup, nil
		} else {
			return gm.Destination, gm.Author, scopeSync, nil
		}
	case TypeDirectMessage:
		// Find the direct message
		var dm directMessage
		err := b.database.Select("xor", "author").First(&dm, "id = ?", id).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id": id,
				}).Debug("direct message not found while marking as read")
				return uuid.Nil, uuid.Nil, scopeSync, err
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
					"id":    id,
				}).Fatal("database error looking up group message")
			}
		}

		// Find the user
		userID := xor(dm.Xor, b.currentUserID())
		var u user
		err = b.database.Select("read_receipts_overridden", "read_receipts_enabled").First(&u, "id = ?", userID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id": userID,
				}).Error("user not found while marking as read")
				return uuid.Nil, uuid.Nil, scopeSync, err
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
					"id":    userID,
				}).Fatal("database error looking up user")
			}
		}

		if (u.ReadReceiptsOverridden && u.ReadReceiptsEnabled) || (!u.ReadReceiptsOverridden && defaultSendReadReceipts) {
			if userID == b.currentUserID() {
				return userID, dm.Author, scopeSync, nil
			} else {
				return userID, dm.Author, scopeUser, nil
			}
		} else {
			return userID, dm.Author, scopeSync, nil
		}
	case TypeUpdateGroup:
		// Currently not sending these, but could send and display in the future
	case TypeUpdateDM:
		// Currently not sending these, but could send and display in the future
	}

	return uuid.Nil, uuid.Nil, scopeSync, errUnknownReadReceiptTargetType
}

func (b *Bounce) sendReadReceipt(id uuid.UUID, frameType string) error {
	// Create read receipt
	targetTypeInt, ok := readReceiptTargetTypeInt[frameType]
	if !ok {
		return errUnknownReadReceiptTargetType
	}
	destination, author, scope, err := b.getReadReceiptDestinationAuthorAndScope(id, frameType)
	if err != nil {
		if errors.Is(err, errUnknownReadReceiptTargetType) {
			return nil
		}
		return err
	}
	if author == b.currentUserID() {
		return nil
	}

	rr := readReceipt{
		ID:          uuid.New(),
		Actor:       b.currentUserID(),
		Destination: destination,
		Scope:       scope,
		Target:      id,
		TargetType:  targetTypeInt,
		Timestamp:   time.Now().Unix(),
	}

	// Sign it
	rr.OriginalPayload, err = msgpack.Marshal(rr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling read reeceipt")
	}
	sc := b.createSignedContainer(rr.OriginalPayload)
	rr.Signature = sc.Signature
	rr.Signer = sc.Signer

	// Save to database
	err = b.database.Create(&rr).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving read receipt")
	}

	// Broadcast
	go b.broadcast(&rr)

	return nil
}

func (b *Bounce) markSeenInDatabase(id uuid.UUID, frameType string) error {
	tableName := ""
	switch frameType {
	case TypeGroupMessage:
		tableName = "group_messages"
	case TypeDirectMessage:
		tableName = "direct_messages"
	case TypeUpdateGroup:
		tableName = "update_groups"
	case TypeUpdateDM:
		tableName = "update_dms"
	case TypeUpdateUser:
		tableName = "update_users"
	case TypeGroupCreation:
		// Seen tracking is not supported for group creations
		return nil
	default:
		return errUnknownReadReceiptTargetType
	}

	err := b.database.Table(tableName).Where("id = ?", id).Updates(map[string]interface{}{"seen": true}).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"id":         id,
				"frame_type": frameType,
			}).Error("item not found while marking as seen")
			return err
		} else {
			log.WithFields(log.Fields{
				"error":      err.Error(),
				"id":         id,
				"frame_type": frameType,
			}).Fatal("database error marking item as seen")
		}
	}

	return nil
}

func (b *Bounce) MarkAsRead(id uuid.UUID, frameType string) {
	err := b.markSeenInDatabase(id, frameType)
	if err != nil {
		log.WithFields(log.Fields{
			"id":         id,
			"frame_type": frameType,
			"error":      err.Error(),
		}).Error("error marking frame as read in database")
		return
	}

	err = b.sendReadReceipt(id, frameType)
	if err != nil {
		log.WithFields(log.Fields{
			"id":         id,
			"frame_type": frameType,
			"error":      err.Error(),
		}).Error("error sending read receipt for frame")
	}
}

func (b *Bounce) MarkAllGroupMessagesAsRead(groupID uuid.UUID) {
	var gms []groupMessage
	err := b.database.Select("id").Where("destination = ? AND seen = ?", groupID, false).Find(&gms).Error
	if err != nil {
		log.WithFields(log.Fields{
			"group_id": groupID,
			"error":    err.Error(),
		}).Fatal("database error selecting unseen group messages")
	}
	if len(gms) == 0 {
		return
	}

	err = b.database.Table("group_messages").Where("destination = ? AND seen = ?", groupID, false).Updates(map[string]interface{}{"seen": true}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"group_id": groupID,
			"error":    err.Error(),
		}).Fatal("database error marking all group messages as seen")
	}

	go func() {
		for _, gm := range gms {
			err = b.sendReadReceipt(gm.ID, TypeGroupMessage)
			if err != nil {
				log.WithFields(log.Fields{
					"id":         gm.ID,
					"frame_type": TypeGroupMessage,
					"error":      err.Error(),
				}).Error("error sending read receipt")
			}
		}
	}()
}

func (b *Bounce) MarkAllDirectMessagesAsRead(userID uuid.UUID) {
	var dms []directMessage
	err := b.database.Select("id").Where("xor = ? AND seen = ?", xor(userID, b.currentUserID()), false).Find(&dms).Error
	if err != nil {
		log.WithFields(log.Fields{
			"user_id": userID,
			"error":   err.Error(),
		}).Fatal("database error selecting unseen direct messages")
	}
	if len(dms) == 0 {
		return
	}

	err = b.database.Table("direct_messages").Where("xor = ? AND seen = ?", xor(userID, b.currentUserID()), false).Updates(map[string]interface{}{"seen": true}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"user_id": userID,
			"error":   err.Error(),
		}).Fatal("database error marking all direct messages as seen")
	}

	go func() {
		for _, dm := range dms {
			err = b.sendReadReceipt(dm.ID, TypeDirectMessage)
			if err != nil {
				log.WithFields(log.Fields{
					"id":         dm.ID,
					"frame_type": TypeDirectMessage,
					"error":      err.Error(),
				}).Error("error sending read receipt")
			}
		}
	}()
}
