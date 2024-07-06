package chat

import (
	"errors"
	"sync"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
)

var updateDeviceMutex sync.Mutex

var updateDeviceTypeUpdateName = uint16(0)
var updateDeviceTypeRevoke = uint16(1)

var errInvalidDeviceName = errors.New("invalid name")
var errUnsupportedUpdateDeviceType = errors.New("unsupported update device type")

type updateDevice struct {
	ID              uuid.UUID `gorm:"type:uuid;primary_key;"`
	Target          uuid.UUID
	Type            uint16
	Data            []byte
	Timestamp       int64
	Signer          string `msgpack:"-" gorm:"not null"`
	OriginalPayload []byte `msgpack:"-" gorm:"not null"`
	Signature       []byte `msgpack:"-" gorm:"not null"`
	payload         []byte
	payloadMutex    sync.Mutex
}

func (ud *updateDevice) BeforeCreate(tx *gorm.DB) error {
	if ud.ID == uuid.Nil {
		return errors.New("update device ID must be set before creation")
	}

	return nil
}

func (ud *updateDevice) getID() uuid.UUID {
	return ud.ID
}

func (ud *updateDevice) getScope(myID uuid.UUID) int {
	if ud.Type == updateDeviceTypeRevoke {
		return scopeGlobal
	}

	return scopeSync
}

func (ud *updateDevice) getDestination(myID uuid.UUID) uuid.UUID {
	return ud.Target
}

func (ud *updateDevice) getType() uint16 {
	return typeUpdateDevice
}

func (ud *updateDevice) getPayload() []byte {
	ud.payloadMutex.Lock()
	defer ud.payloadMutex.Unlock()

	if len(ud.payload) == 0 {
		bytes, err := msgpack.Marshal(signedContainer{
			Payload:   ud.OriginalPayload,
			Signature: ud.Signature,
			Signer:    ud.Signer,
		})
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error marshalling update device's signed container")
		}
		ud.payload = bytes
	}
	return ud.payload
}

func (ud *updateDevice) getAuthor() uuid.UUID {
	return uuid.Nil // TODO
}

func (ud *updateDevice) getTimestamp() int64 {
	return ud.Timestamp
}

//func (uu *updateUser) validPayload() error {
//	switch uu.Type {
//	case updateUserTypeUpdateName:
//		if !validUserName(string(uu.Data)) {
//			return errInvalidUserName
//		}
//	}
//
//	return nil
//}

func (b *bounce) handleUpdateDevice(peer string, payload []byte, catchUp bool) broadcastable {
	return nil
}

func (b *bounce) RenameDevice(deviceID uuid.UUID, name string) error {
	return nil
}

func (b *bounce) RevokeDevice(deviceID uuid.UUID) error {
	return nil
}
