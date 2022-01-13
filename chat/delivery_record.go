package chat

import (
	"errors"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type deliveryRecord struct {
	ID          uuid.UUID `gorm:"type:uuid;primary_key;"`
	CreatedAt   int64
	Destination string    `gorm:"index;uniqueIndex:idx_destination_frame_id_frame_type"`
	FrameID     uuid.UUID `gorm:"index;uniqueIndex:idx_destination_frame_id_frame_type"`
	FrameType   uint16    `gorm:"index;uniqueIndex:idx_destination_frame_id_frame_type"`
}

func (dr *deliveryRecord) BeforeCreate(tx *gorm.DB) error {
	if dr.ID != uuid.Nil {
		log.Fatal("cannot create a delivery record with an ID already set")
	}
	dr.ID = uuid.New()
	dr.CreatedAt = time.Now().Unix()
	return nil
}

func (b *bounce) markDeliveredTo(br broadcastable, destination string) {
	if br.getID() == uuid.Nil {
		log.Warn("tracking delivery of broadcastable with nil UUID")
	} // TODO: just to catch bugs, maybe not needed

	err := b.database.Clauses(clause.OnConflict{DoNothing: true}).Create(&deliveryRecord{
		Destination: destination,
		FrameID:     br.getID(),
		FrameType:   br.getType(),
	}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error creating delivery record")
	}
}

func (b *bounce) isDeliveredTo(br broadcastable, destination string) bool {
	var dr deliveryRecord
	err := b.database.Where("destination = ? AND frame_id = ? AND frame_type = ?", destination, br.getID(), br.getType()).First(&dr).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error looking up delivery record")
		}
	}
	return true
}
