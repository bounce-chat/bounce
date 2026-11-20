package chat

import (
	"errors"

	"gorm.io/gorm"
)

var errDeviceIDIsNil = errors.New("device ID is nil")

type introductionSignature struct {
	ID                           ID `gorm:"primary_key;" msgpack:"-"`
	DeviceID                     ID `msgpack:"-"`
	PreexistingDevice            string
	SignatureOfNewDevice         []byte
	SignatureOfPreexistingDevice []byte
}

func (is *introductionSignature) BeforeCreate(tx *gorm.DB) error {
	is.ID = is.DeviceID
	if is.ID == NilID {
		return errDeviceIDIsNil
	}
	return nil
}
