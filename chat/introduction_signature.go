package chat

import (
	"gorm.io/gorm"
)

type introductionSignature struct {
	ID                           ID `gorm:"primary_key;"`
	DeviceID                     ID
	PreexistingDevice            string
	SignatureOfNewDevice         []byte
	SignatureOfPreexistingDevice []byte
}

func (is *introductionSignature) BeforeCreate(tx *gorm.DB) error {
	if is.ID == NilID {
		is.ID = NewID()
	}
	return nil
}
