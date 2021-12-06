package chat

import (
	"crypto/rand"
	"fmt"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type syncDeviceOffer struct {
	ID        uuid.UUID `gorm:"type:uuid;primary_key;"`
	Timestamp int64
	Secret    string
}

func (sdo *syncDeviceOffer) BeforeCreate(tx *gorm.DB) error {
	sdo.ID = uuid.New()
	return nil
}

func (b *bounce) getNewSyncString() string {
	secretBytes := make([]byte, 16)
	rand.Read(secretBytes)
	secret := fmt.Sprintf("%x", secretBytes)

	offer := syncDeviceOffer{
		Timestamp: time.Now().Unix(),
		Secret:    secret,
	}

	err := b.database.Create(&offer).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error creating sync device offer")
	}

	err = b.database.Where("id != ?", offer.ID).Delete(syncDeviceOffer{}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error deleting old sync device offers while creating new one")
	}

	return b.network.Address() + ":" + secret
}
