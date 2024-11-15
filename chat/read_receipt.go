package chat

import (
	"errors"
	"sync"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type readReceipt struct {
	ID              uuid.UUID
	Actor           uuid.UUID
	Scope           int // TODO: re-create on device?
	TargetID        uuid.UUID
	TargetType      uint16
	Timestamp       int64
	Signer          string `msgpack:"-" gorm:"not null"`
	OriginalPayload []byte `msgpack:"-" gorm:"not null"`
	Signature       []byte `msgpack:"-" gorm:"not null"`
	payload         []byte
	payloadMutex    sync.Mutex
}

func (b *bounce) sendReadReceipt(id uuid.UUID, frameType string) {
	switch frameType {
	case TypeGroupMessage:
		// Find the group this message is in
		//var gm groupMessage
		//err := b.database.Select("destination").First(&gm, "id = ?", id).Error
		//if err != nil {
		//	if errors.Is(err, gorm.ErrRecordNotFound) {
		//		log.WithFields(log.Fields{
		//			"id": id,
		//		}).Error("group message not found while marking as read")
		//		return
		//	} else {
		//		log.WithFields(log.Fields{
		//			"error": err.Error(),
		//			"id":    id,
		//		}).Fatal("database error looking up group message")
		//	}
		//}
	case TypeDirectMessage:
	case TypeUpdateGroup:
		//readReceipt{
		//	ID:         uuid.New(),
		//	Actor:      b.currentUserID(),
		//	Scope:      scopeSync,
		//	TargetID:   id,
		//	TargetType: typeUpdateGroup,
		//	Timestamp:  time.Now().Unix(),
		//}
	case TypeUpdateDM:
	}

}

func (b *bounce) handleReadReceipt(peer string, data []byte, catchUp bool) {
	// TODO: mark it as read if the Actor is us, update the chat bubble if it's someone else
}

func (b *bounce) markRead(id uuid.UUID, frameType string) {
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
		log.WithFields(log.Fields{
			"id":         id,
			"frame_type": frameType,
		}).Error("unsupported frame type for marking as read")
		return
	}

	err := b.database.Table(tableName).Where("id = ?", id).Updates(map[string]interface{}{"read": true}).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"id":         id,
				"frame_type": frameType,
			}).Error("item not found while marking as read")
			return
		} else {
			log.WithFields(log.Fields{
				"error":      err.Error(),
				"id":         id,
				"frame_type": frameType,
			}).Fatal("database error marking item as read")
		}
	}

	b.sendReadReceipt(id, frameType)

	// TODO: make read on any earlier frames of the same type?
	//       for each type of frame, if destination lines up timestamp is before x,
	//       mark as read?
}
