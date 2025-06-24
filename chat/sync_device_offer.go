package chat

import (
	"crypto/rand"
	"fmt"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

//
// A sync device offer is the secret stored on our device that is conveyed to another device that wants to be a sync device for our user.
// The other device scans or otherwise gets a strng with our address and this secret, then initiates the flow with a sync device request.
// A new secret is generated every time the UI displays this string, and only one is stored in the database at a time.
//
type syncDeviceOffer struct {
	ID        uuid.UUID `gorm:"type:uuid;primary_key;"`
	Timestamp int64
	Secret    string
}

func (sdo *syncDeviceOffer) BeforeCreate(tx *gorm.DB) error {
	if sdo.ID != uuid.Nil {
		log.Fatal("sync device offer cannot have an ID assigned before creation")
	}
	sdo.ID = uuid.New()
	return nil
}

func (b *Bounce) GetNewSyncString() string {
	// Generate a secret
	secretBytes := make([]byte, 16)
	rand.Read(secretBytes)
	secret := fmt.Sprintf("%x", secretBytes)

	// Save the new offer
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

	// Delete all other past offers
	err = b.database.Clauses(clause.Returning{}).Where("id != ?", offer.ID).Delete(syncDeviceOffer{}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error deleting old sync device offers while creating new one")
	}

	return b.network.Address() + ":" + secret
}
