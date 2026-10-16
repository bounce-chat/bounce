package chat

import (
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

func (b *Bounce) markDeliveredTo(br broadcastable, destination string) {
	if br.getID() == uuid.Nil {
		log.Warn("tracking delivery of broadcastable with nil UUID")
	}

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

func (b *Bounce) isDeliveredTo(br broadcastable, destination string) bool {
	return b.frameIsDeliveredTo(br.getID(), br.getType(), destination)
}

func (b *Bounce) frameIsDeliveredTo(frameID uuid.UUID, frameType uint16, peer string) bool {
	var count int64
	err := b.database.Model(&deliveryRecord{}).Where("destination = ? AND frame_id = ? AND frame_type = ?", peer, frameID, frameType).Count(&count).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up delivery record")
	}
	return count > 0
}

func (b *Bounce) markFrameDelivered(id uuid.UUID, frameType uint16, destination string) {
	if id == uuid.Nil {
		log.Warn("tracking delivery of broadcastable with nil UUID")
	}

	err := b.database.Clauses(clause.OnConflict{DoNothing: true}).Create(&deliveryRecord{
		Destination: destination,
		FrameID:     id,
		FrameType:   frameType,
	}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error creating delivery record")
	}
}
