package chat

import (
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type introductionSignature struct {
	ID                           uuid.UUID `gorm:"type:uuid;primary_key;" json:"-"`
	DeviceID                     uuid.UUID
	PreexistingDevice            string // TODO: should be a UUID?  No, this is an address not a pk.  But should it be?
	SignatureOfNewDevice         []byte
	SignatureOfPreexistingDevice []byte
}

func (is *introductionSignature) BeforeCreate(tx *gorm.DB) error {
	if is.ID == uuid.Nil {
		is.ID = uuid.New()
	}
	return nil
}
