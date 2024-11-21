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
	ID              uuid.UUID
	Actor           uuid.UUID
	Destination     uuid.UUID `msgpack:"-"`
	Scope           int       `msgpack:"-"`
	Target          uuid.UUID
	TargetType      uint16
	Timestamp       int64
	Signer          string `msgpack:"-" gorm:"not null"`
	OriginalPayload []byte `msgpack:"-" gorm:"not null"`
	Signature       []byte `msgpack:"-" gorm:"not null"`
	payload         []byte
	payloadMutex    sync.Mutex
}

func (rr *readReceipt) BeforeCreate(tx *gorm.DB) error {
	if rr.ID == uuid.Nil {
		return errors.New("read receipt ID must be set before creation")
	}

	return nil
}

func (rr *readReceipt) AfterDelete(tx *gorm.DB) error {
	return tx.Where("frame_id = ? AND frame_type = ?", rr.ID, typeReadReceipt).Delete(&deliveryRecord{}).Error
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

func (b *bounce) handleReadReceipt(peer string, payload []byte, catchUp bool) broadcastable {
	readReceiptMutex.Lock()
	defer readReceiptMutex.Unlock()

	// Unpack the signed container
	sc, err := b.unpackSignedContainer(payload)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unpacking signed container for read receipt")
		return nil
	}
	var rr readReceipt
	err = msgpack.Unmarshal(sc.Payload, &rr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling read receipt")
		return nil
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
		return nil
	}

	// Check if it already exists in the database
	var existingRR readReceipt
	err = b.database.Where("id = ?", rr.ID).First(&existingRR).Error
	if err == nil {
		return &existingRR
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
		return nil
	}
	destination, author, scope, err := b.getReadReceiptDestinationAuthorAndScope(rr.Target, targetTypeString)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error parsing read receipt")
		return nil
	}
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
		b.markReadInDatabase(rr.Target, targetTypeString)
		b.userInterface.MessageRead(rr.Target)
	} else if author == b.currentUserID() {
		b.userInterface.ReceivedReadReceipt(ReadReceipt{
			ID:     rr.ID,
			Actor:  rr.Actor,
			Target: rr.Target,
		})
	}

	return &rr
}

func (b *bounce) getReadReceiptDestinationAuthorAndScope(id uuid.UUID, frameType string) (uuid.UUID, uuid.UUID, int, error) {
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
				log.WithFields(log.Fields{
					"id": id,
				}).Error("group message not found while marking as read")
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
				log.WithFields(log.Fields{
					"id": gm.Destination,
				}).Error("group not found while marking as read")
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
				}).Error("group message not found while marking as read")
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
			return userID, dm.Author, scopeUser, nil
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

func (b *bounce) sendReadReceipt(id uuid.UUID, frameType string) error {
	// Create read receipt
	targetTypeInt, ok := readReceiptTargetTypeInt[frameType]
	if !ok {
		return errUnknownReadReceiptTargetType
	}
	destination, _, scope, err := b.getReadReceiptDestinationAuthorAndScope(id, frameType)
	if err != nil {
		if errors.Is(err, errUnknownReadReceiptTargetType) {
			return nil
		}
		return err
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

func (b *bounce) markReadInDatabase(id uuid.UUID, frameType string) error {
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
	default:
		return errUnknownReadReceiptTargetType
	}

	err := b.database.Table(tableName).Where("id = ?", id).Updates(map[string]interface{}{"read": true}).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"id":         id,
				"frame_type": frameType,
			}).Error("item not found while marking as read")
			return err
		} else {
			log.WithFields(log.Fields{
				"error":      err.Error(),
				"id":         id,
				"frame_type": frameType,
			}).Fatal("database error marking item as read")
		}
	}

	// TODO: make read on any earlier frames of the same type in this thread?
	//       for each type of frame, if destination lines up and timestamp is before x, mark as read?

	return nil
}

func (b *bounce) markRead(id uuid.UUID, frameType string) {
	err := b.markReadInDatabase(id, frameType)
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
