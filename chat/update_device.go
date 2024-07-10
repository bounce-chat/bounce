package chat

import (
	"errors"
	"sync"
	"time"

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
	Author          uuid.UUID `msgpack:"-"`
	Signer          string    `msgpack:"-" gorm:"not null"`
	OriginalPayload []byte    `msgpack:"-" gorm:"not null"`
	Signature       []byte    `msgpack:"-" gorm:"not null"`
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
	return ud.Target // TODO
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
	return ud.Author
}

func (ud *updateDevice) getTimestamp() int64 {
	return ud.Timestamp
}

func (ud *updateDevice) validPayload() error {
	switch ud.Type {
	case updateDeviceTypeUpdateName:
		if !validDeviceName(string(ud.Data)) {
			return errInvalidDeviceName
		}
	}

	return nil
}

func (b *bounce) handleUpdateDevice(peer string, payload []byte, catchUp bool) broadcastable {
	updateDeviceMutex.Lock()
	defer updateDeviceMutex.Unlock()

	// Unpack the signed container
	sc, err := b.unpackSignedContainer(payload)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unpacking signed container for update device")
		return nil
	}
	var ud updateDevice
	err = msgpack.Unmarshal(sc.Payload, &ud)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling update device")
		return nil
	}
	ud.OriginalPayload = sc.Payload
	ud.Signature = sc.Signature
	ud.Signer = sc.Signer

	if err = ud.validPayload(); err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("ignoring update device with invalid payload")
		return nil
	}

	var d device
	err = b.database.Where("id = ?", ud.Target).First(&d).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"id":        ud.ID,
				"device_id": ud.Target,
				"error":     err.Error(),
			}).Error("target device not found in update device")
			return nil
		} else {
			log.WithFields(log.Fields{
				"device_id": ud.Target,
				"error":     err.Error(),
			}).Fatal("database error looking up device")
		}
	}
	ud.Author = d.UserID

	// If we already have this update, we just mark that this peer has it too and return
	var existingUD updateDevice
	err = b.database.Where("id = ?", ud.ID).First(&existingUD).Error
	if err == nil {
		return &existingUD
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up update device")
	}

	// Save this update, apply the change to the database, and inform the UI
	err = b.database.Create(&ud).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving update device")
	}

	// If we're not in a catchup, set the state now
	//if !catchUp {
	b.updateDeviceState(ud.Target)
	//}

	return &ud
}

func (b *bounce) updateDeviceState(deviceID uuid.UUID) {
	var d device
	err := b.database.Where("id = ?", deviceID).First(&d).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"device_id": deviceID,
				"error":     err.Error(),
			}).Error("device not found when updating user state")
			return
		} else {
			log.WithFields(log.Fields{
				"device_id": deviceID,
				"error":     err.Error(),
			}).Fatal("database error looking up device")
		}
	}
	name := d.Name
	revokedAt := d.RevokedAt

	var uds []updateDevice
	err = b.database.Where("target = ?", deviceID).Find(&uds).Error
	if err != nil {
		log.WithFields(log.Fields{
			"device_id": deviceID,
			"error":     err.Error(),
		}).Fatal("database error looking up update devices")
	}

	for _, ud := range uds {
		switch ud.Type {
		case updateDeviceTypeUpdateName:
			name = string(ud.Data)
		case updateDeviceTypeRevoke:
			revokedAt = ud.Timestamp
		}
	}

	if d.Name != name {
		err := b.database.Table("devices").Where("id = ?", deviceID).Updates(map[string]interface{}{"name": name}).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":     err.Error(),
				"device_id": deviceID,
			}).Fatal("database error updating device name")
		}

		b.userInterface.DeviceRenamed(deviceID, name)
	}

	if revokedAt != 0 && (d.RevokedAt == 0 || revokedAt < d.RevokedAt) {
		err := b.database.Table("devices").Where("id = ?", deviceID).Updates(map[string]interface{}{"revoked_at": revokedAt}).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":     err.Error(),
				"device_id": deviceID,
			}).Fatal("database error updating device revoked at")
		}

		b.revokeUnauthorizedDeviceActions(d.Address, revokedAt)

		b.userInterface.DeviceRevoked(deviceID)
	}
}

func (b *bounce) revokeUnauthorizedDeviceActions(address string, revokedAt int64) {
	// TODO: find any group creations, group messages, update users, or update groups, that came from this device after the revoked at timestamp, and reverse them
}

func (b *bounce) renameDevice(deviceID uuid.UUID, name string) error {
	ud := updateDevice{
		ID:        uuid.New(),
		Target:    deviceID,
		Type:      updateDeviceTypeUpdateName,
		Data:      []byte(name),
		Timestamp: time.Now().Unix(),
		Author:    b.currentUserID(),
	}

	return b.applyAndBroadcastUpdateDevice(ud)
}

func (b *bounce) revokeDevice(deviceID uuid.UUID) error {
	// TODO: make sure not already revoked

	ud := updateDevice{
		ID:        uuid.New(),
		Target:    deviceID,
		Type:      updateDeviceTypeRevoke,
		Timestamp: time.Now().Unix(),
		Author:    b.currentUserID(),
	}

	return b.applyAndBroadcastUpdateDevice(ud)
}

func (b *bounce) applyAndBroadcastUpdateDevice(ud updateDevice) error {
	var err error
	ud.OriginalPayload, err = msgpack.Marshal(&ud)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling update device")
	}
	sc := b.createSignedContainer(ud.OriginalPayload)
	ud.Signature = sc.Signature
	ud.Signer = sc.Signer

	if err = ud.validPayload(); err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("refusing to apply update device with invalid payload")
		return err
	}

	err = b.database.Create(&ud).Error
	if err != nil {
		log.WithFields(log.Fields{
			"id":    ud.ID,
			"error": err.Error(),
		}).Error("error saving update device")
		return err
	}

	b.updateDeviceState(ud.Target)

	b.broadcast(&ud)

	return nil
}
