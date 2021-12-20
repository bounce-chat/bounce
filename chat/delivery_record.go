package chat

import (
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type deliveryRecord struct {
	ID          uuid.UUID `gorm:"type:uuid;primary_key;"`
	Destination string    `gorm:"uniqueIndex:idx_destination_frame_id_frame_type"`
	FrameID     uuid.UUID `gorm:"uniqueIndex:idx_destination_frame_id_frame_type"`
	FrameType   string    `gorm:"uniqueIndex:idx_destination_frame_id_frame_type"`
}

func (dr *deliveryRecord) BeforeCreate(tx *gorm.DB) error {
	if dr.ID != uuid.Nil {
		log.Fatal("cannot create a delivery record with an ID already set")
	}
	dr.ID = uuid.New()
	return nil
}

/*
func (b *bounce) markDeliveredTo(br broadcastable, destination string) {
	err := b.database.Clauses(clause.OnConflict{DoNothing: true}).Create(&deliveryRecord{
		Destination: destination,
		FrameID:     br.getID(),
		FrameType:   br.getTableName(),
	}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error creating delivery record")
		// TODO: if the parent doesn't exist, this should fail with a warning
	}
}

func (b *bounce) isDeliveredTo(br broadcastable, destination string) bool {
	var dr deliveryRecord
	err := b.database.Where("destination = ? AND frame_id = ? AND frame_type = ?", destination, br.getID, br.getTableName).First(&dr).Error
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
*/
