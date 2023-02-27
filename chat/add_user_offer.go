package chat

import (
	"crypto/rand"
	"fmt"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type addUserOffer struct {
	ID        uuid.UUID `gorm:"type:uuid;primary_key;"`
	Timestamp int64
	Secret    string
}

func (auo *addUserOffer) BeforeCreate(tx *gorm.DB) error {
	if auo.ID != uuid.Nil {
		log.Fatal("add user offer cannot have an ID assigned before creation")
	}
	auo.ID = uuid.New()
	return nil
}

func (b *bounce) getNewAddUserString() string {
	secretBytes := make([]byte, 16)
	rand.Read(secretBytes)
	secret := fmt.Sprintf("%x", secretBytes)

	offer := addUserOffer{
		Timestamp: time.Now().Unix(),
		Secret:    secret,
	}

	err := b.database.Create(&offer).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error creating add user offer")
	}

	err = b.database.Where("id != ?", offer.ID).Delete(addUserOffer{}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error deleting old add user offers while creating new one")
	}

	return b.network.Address() + ":" + secret
}
