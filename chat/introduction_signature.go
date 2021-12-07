package chat

import (
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type introductionSignature struct {
	ID                           uuid.UUID `gorm:"type:uuid;primary_key;" json:"-"`
	DeviceID                     uuid.UUID
	PreexistingDevice            string // TODO: should be a UUID?  No, this is an address not a pk
	SignatureOfNewDevice         []byte
	SignatureOfPreexistingDevice []byte
}

func (introductionSignature *introductionSignature) BeforeCreate(tx *gorm.DB) error {
	introductionSignature.ID = uuid.New()
	return nil
}
