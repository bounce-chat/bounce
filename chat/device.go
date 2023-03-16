package chat

import (
	"errors"
	"sync"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var handleDevicesMutex sync.Mutex

type device struct {
	ID           uuid.UUID `gorm:"type:uuid;primary_key;"`
	Name         string    `json:"-"` // TODO: exclude from non-sync devices
	UserID       uuid.UUID `json:"-"`
	Address      string    `gorm:"uniqueIndex"`
	Timestamp    int64
	Signature    *introductionSignature `json:",omitempty" gorm:"constraint:OnDelete:CASCADE;"`
	payload      []byte
	payloadMutex sync.Mutex
}

func (d *device) BeforeCreate(tx *gorm.DB) error {
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	return nil
}

func (d *device) AfterDelete(tx *gorm.DB) error {
	return tx.Where("frame_id = ? AND frame_type = ?", d.ID, typeDevice).Delete(&deliveryRecord{}).Error
}

func (d *device) getID() uuid.UUID {
	return d.ID
}

func (d *device) getScope(myID uuid.UUID) int {
	if d.UserID == myID {
		// Tell everyone about my devices
		return scopeGlobal
	}
	//return scopeOverlap // TODO
	return scopeSync
}

func (d *device) getDestination(myID uuid.UUID) uuid.UUID {
	return uuid.Nil
}

func (d *device) getType() uint16 {
	return typeDevice
}

func (d *device) getPayload() []byte {
	d.payloadMutex.Lock()
	defer d.payloadMutex.Unlock()

	if len(d.payload) == 0 {
		bytes, err := msgpack.Marshal(d)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal device")
		}
		d.payload = bytes
	}
	return d.payload
}

func (d *device) getTimestamp() int64 {
	return d.Timestamp
}

func (b *bounce) handleDevice(peer string, payload []byte) {
	handleDevicesMutex.Lock()
	defer handleDevicesMutex.Unlock()

	// Unmarshal the device
	var newDevice device
	err := msgpack.Unmarshal(payload, &newDevice)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling device")
		return
	}

	// If the device already exists, ack it and return
	if _, deviceExists := b.getDeviceFromAddress(newDevice.Address); deviceExists {
		b.sendDirectAck(peer, frameReference{FrameID: newDevice.ID, Type: typeDevice})
		b.markDeliveredTo(&newDevice, peer)
		return
	}

	// Find the user this new device is for
	var targetUser user
	err = b.database.Preload(clause.Associations).First(&targetUser, "id = ?", newDevice.UserID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"user":    newDevice.UserID,
				"device":  newDevice.ID,
				"address": newDevice.Address,
				"peer":    peer,
			}).Warn("rejecting received device because we do have the specified user")
			return
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up user while receiving new device")
		}
	}

	// Make sure this device is properly introduced by checking if adding the device to the user would result in an
	// invalid device group
	if !b.isValidAddition(targetUser, newDevice) {
		log.WithFields(log.Fields{
			"user":    targetUser.ID,
			"device":  newDevice.ID,
			"address": newDevice.Address,
			"peer":    peer,
		}).Warn("rejecting received device because it would result in an invalid device group")
		return
	}

	// Save it
	err = b.database.Create(&newDevice).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving new device")
	}

	// Save a delivery record for the peer that sent us this device
	b.markDeliveredTo(&newDevice, peer)

	// Broadcast to the rest of the peers
	go b.broadcast(&newDevice)

	// ACK it
	b.sendDirectAck(peer, frameReference{FrameID: newDevice.ID, Type: typeDevice})
}

func (b *bounce) getDeviceFromAddress(address string) (device, bool) {
	var dev device
	err := b.database.Preload(clause.Associations).Where("address = ?", address).First(&dev).Error
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

func (b *bounce) isSyncDevice(dev device) bool {
	return dev.UserID == b.currentUserID()
}
