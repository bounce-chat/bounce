package chat

import (
	"errors"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type device struct {
	ID        uuid.UUID              `gorm:"type:uuid;primary_key;" json:"-"`
	Name      string                 `json:"-"`
	UserID    uuid.UUID              `json:"-"`
	Address   string                 `gorm:"uniqueIndex"`
	Signature *introductionSignature `json:",omitempty"`
}

func (device *device) BeforeCreate(tx *gorm.DB) error {
	device.ID = uuid.New()
	return nil
}

type introductionSignature struct {
	ID                           uuid.UUID `gorm:"type:uuid;primary_key;"`
	DeviceID                     uuid.UUID
	PreexistingDevice            string // TODO: should be a UUID?
	SignatureOfNewDevice         []byte
	SignatureOfPreexistingDevice []byte
}

func (introductionSignature *introductionSignature) BeforeCreate(tx *gorm.DB) error {
	introductionSignature.ID = uuid.New()
	return nil
}

func (b *bounce) getDeviceFromAddress(address string) (device, bool) {
	var dev device
	err := b.database.Where("address = ?", address).First(&dev).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return dev, false
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up device")
		}
	}
	return dev, true
}

func (b *bounce) isSyncDevice(address string) bool {
	dev, exists := b.getDeviceFromAddress(address)
	if !exists {
		return false
	}
	return dev.UserID == b.currentUserID()
}
