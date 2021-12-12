package chat

import (
	"errors"
	"strings"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type device struct {
	ID          uuid.UUID              `gorm:"type:uuid;primary_key;"`
	Name        string                 `json:"-"` // TODO: exclude from tell non-sync devices
	UserID      uuid.UUID              `json:"-"`
	Address     string                 `gorm:"uniqueIndex"`
	Signature   *introductionSignature `json:",omitempty"`
	DeliveredTo string                 `json:"-" msgpack:"-"`
	payload     []byte
}

func (d *device) BeforeCreate(tx *gorm.DB) error {
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	return nil
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
	if len(d.payload) == 0 {
		bytes, err := msgpack.Marshal(d)
		if err != nil {
			// TODO: how to handle?
		}
		d.payload = bytes
	}
	return d.payload
}

func (d *device) isAlreadyDeliveredTo(address string) bool {
	// TODO: reload from the database?
	recipients := strings.Split(d.DeliveredTo, ",")
	for _, recipient := range recipients {
		if address == recipient {
			return true
		}
	}
	return false
}

func (b *bounce) markDeviceDeliveredTo(d *device, address string) {
	if !d.isAlreadyDeliveredTo(address) {
		currentDeliveredTo := []string{}
		if len(d.DeliveredTo) != 0 {
			currentDeliveredTo = strings.Split(d.DeliveredTo, ",")
		}
		updatedDeliveredTo := strings.Join(append(currentDeliveredTo, address), ",")
		err := b.database.Model(d).Update("delivered_to", updatedDeliveredTo).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":   err.Error(),
				"message": d.ID,
			}).Fatal("error updating device delivery status")
		}
	}
}

func (b *bounce) handleDevice(peer string, payload []byte) {
	// Unmarshal the device
	var newDevice device
	err := msgpack.Unmarshal(payload, &newDevice)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling device")
		return
	}

	// Find the device that sent this new device.  If we don't have it saved, this is a device introducing itself
	srcDevice, peerExists := b.getDeviceFromAddress(peer)
	if !peerExists {
		if newDevice.Address != peer {
			log.WithFields(log.Fields{
				"peer": peer,
			}).Warn("an unknown device can only send itself, ignoring received device")
			return
		}
	}

	// If the device already exists, ack it and return
	if _, deviceExists := b.getDeviceFromAddress(newDevice.Address); deviceExists {
		b.broadcast(&ack{
			destination: srcDevice.ID,
			Devices:     newDevice.ID.String(),
		})
		return
	}

	// Find the user this new device is for
	var targetUser user
	err = b.database.Find(&targetUser, "id = ?", newDevice.UserID).Error
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
	newDevice.DeliveredTo = peer
	err = b.database.Create(&newDevice).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving new device")
	}
	// Broadcast to the rest of the peers
	go b.broadcast(&newDevice)

	// If we didn't know about the peer that sent us this device, then the new device we saved
	// is the peer
	if !peerExists {
		srcDevice = newDevice
	}

	// ACK it
	go b.broadcast(&ack{
		destination: srcDevice.ID,
		Devices:     newDevice.ID.String(),
	})
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

func (b *bounce) isSyncDevice(address string) bool {
	dev, exists := b.getDeviceFromAddress(address)
	if !exists {
		return false
	}
	return dev.UserID == b.currentUserID()
}
