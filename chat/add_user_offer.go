package chat

import (
	"crypto/rand"
	"fmt"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// An add user offer is the secret stored on our device that is conveyed to another device that wants to be our friend.
// The other device scans or otherwise gets a strng with our address and this secret, then initiates thr flow with an
// add user request.  A new secret is generated every time the UI displays this string, and only one is stored in the
// database at a time.
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

func (b *Bounce) GetNewAddUserString() string {
	// Generate a secret
	secretBytes := make([]byte, 16)
	rand.Read(secretBytes)
	secret := fmt.Sprintf("%x", secretBytes)

	// Save the new offer
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

	// Delete all other past offers
	err = b.database.Where("id != ?", offer.ID).Delete(addUserOffer{}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error deleting old add user offers while creating new one")
	}

	return b.network.Address() + ":" + secret
}
