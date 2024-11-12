package chat

import (
	"errors"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

func (b *bounce) markRead(id uuid.UUID, frameType string) {
	log.WithFields(log.Fields{"id": id, "type": frameType}).Warn("read message")

	switch frameType {
	case TypeGroupMessage:
		// Find the group this message is in
		var gm groupMessage
		err := b.database.Select("destination").First(&gm, "id = ?", id).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id": id,
				}).Error("group message not found while marking as read")
				return
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
					"id":    id,
				}).Fatal("database error looking up group message")
			}
		}

		// Mark this message as read
		err = b.database.Table("group_messages").Where("id = ?", id).Updates(map[string]interface{}{"read": true}).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id": id,
				}).Error("group message not found while marking as read")
				return
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
					"id":    id,
				}).Fatal("database error marking group message as read")
			}
		}

		// TODO: call into the UI to update the thread item / chat bubble?

		// TODO: look up the group, check if we're doing read receipts on it, broadcast if so
	case TypeDirectMessage:
		err := b.database.Table("direct_messages").Where("id = ?", id).Updates(map[string]interface{}{"read": true}).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id": id,
				}).Error("direct message not found while marking as read")
				return
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
					"id":    id,
				}).Fatal("database error marking direct message as read")
			}
		}

		// TODO: send read receipt if enabled
	case TypeUpdateGroup:
		// Mark this update group as read
		err := b.database.Table("update_groups").Where("id = ?", id).Updates(map[string]interface{}{"read": true}).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id": id,
				}).Error("update group not found while marking as read")
				return
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
					"id":    id,
				}).Fatal("database error marking update group as read")
			}
		}
	case TypeUpdateDM:
		err := b.database.Table("update_dms").Where("id = ?", id).Updates(map[string]interface{}{"read": true}).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id": id,
				}).Error("update dm not found while marking as read")
				return
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
					"id":    id,
				}).Fatal("database error marking update dm as read")
			}
		}
		//case TypeUpdateUser:
	}

	// TODO: mark read on the frame
	// TODO: make read on any earlier frames of the same type
	// TODO: send read receipts if enabled

	// TODO: tell sync devices that a message was read
}
