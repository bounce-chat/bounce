package chat

import (
	"time"

	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type deliveryRecord struct {
	ID          ID `gorm:"primary_key;"`
	CreatedAt   int64
	Destination string `gorm:"index;uniqueIndex:idx_destination_frame_id_frame_type"`
	FrameID     ID     `gorm:"index;uniqueIndex:idx_destination_frame_id_frame_type"`
	FrameType   uint16 `gorm:"index;uniqueIndex:idx_destination_frame_id_frame_type"`
}

func (dr *deliveryRecord) BeforeCreate(tx *gorm.DB) error {
	if dr.ID != NilID {
		log.Fatal("cannot create a delivery record with an ID already set")
	}
	dr.ID = NewID()
	dr.CreatedAt = time.Now().Unix()
	return nil
}

func (b *Bounce) markDeliveredTo(br broadcastable, destination string) {
	if br.getID() == NilID {
		log.Warn("tracking delivery of broadcastable with nil ID")
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

func (b *Bounce) frameIsDeliveredTo(frameID ID, frameType uint16, peer string) bool {
	var count int64
	err := b.database.Model(&deliveryRecord{}).Where("destination = ? AND frame_id = ? AND frame_type = ?", peer, frameID, frameType).Count(&count).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up delivery record")
	}
	return count > 0
}

func (b *Bounce) deliveredUsers(frameType uint16, frameIDs []ID) map[ID][]ID {
	query := b.database.Where("frame_type = ?", frameType)
	if frameIDs != nil {
		if len(frameIDs) == 0 {
			return map[ID][]ID{}
		}
		query = query.Where("frame_id IN (?)", frameIDs)
	}
	var records []deliveryRecord
	err := query.Find(&records).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error looking up delivery records")
	}

	owners := map[string]ID{}
	unknown := map[string]bool{}
	delivered := map[ID][]ID{}
	for _, dr := range records {
		owner, known := owners[dr.Destination]
		if !known {
			if unknown[dr.Destination] {
				continue
			}
			var ok bool
			owner, ok = b.peerOwner(dr.Destination)
			if !ok {
				unknown[dr.Destination] = true
				continue
			}
			owners[dr.Destination] = owner
		}
		delivered[dr.FrameID] = append(delivered[dr.FrameID], owner)
	}
	return delivered
}

func (b *Bounce) markFrameDelivered(id ID, frameType uint16, destination string) {
	if id == NilID {
		log.Warn("tracking delivery of broadcastable with nil ID")
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
