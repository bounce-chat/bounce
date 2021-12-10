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
	Name        string                 `json:"-"`
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
	// Make sure this is a sync device
	srcDevice, exists := b.getDeviceFromAddress(peer)
	if !exists {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("ignoring a device sent from an unknown device")
		return
	}

	// Unmarshal the device
	var dev device
	err := msgpack.Unmarshal(payload, &dev)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling device")
		return
	}

	// TODO: make sure this user can inform us about this device
	// TODO: make sure this device group is valid (ORM hook?)

	// Save it
	dev.DeliveredTo = peer
	err = b.database.Create(&dev).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error saving new device")
	}

	// ACK it
	go b.broadcast(&ack{
		destination: srcDevice.ID,
		Devices:     dev.ID.String(),
	})

	// Broadcast to the rest of the peers
	go b.broadcast(&dev)
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
