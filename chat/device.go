package chat

import (
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var handleDevicesMutex sync.Mutex

//
// A device represents an instance of bounce
//
type device struct {
	ID           uuid.UUID `gorm:"type:uuid;primary_key;"`
	Name         string    `json:"-" msgpack:"-"`
	UserID       uuid.UUID `json:"-"`
	Address      string    `gorm:"uniqueIndex"`
	Timestamp    int64
	LastSeen     int64 `json:"-" msgpack:"-"`
	RevokedAt    int64
	Signature    *introductionSignature `json:",omitempty" gorm:"constraint:OnDelete:CASCADE;"`
	payload      []byte
	payloadMutex sync.Mutex
}

func (d *device) BeforeCreate(tx *gorm.DB) error {
	if d.ID == uuid.Nil {
		return errors.New("device must have ID set before creation")
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
	return scopeGlobal
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

func (d *device) getAuthor() uuid.UUID {
	return d.UserID
}

func (d *device) getTimestamp() int64 {
	return d.Timestamp
}

func (b *Bounce) handleDevice(peer string, payload []byte, catchUp bool) broadcastable {
	handleDevicesMutex.Lock()
	defer handleDevicesMutex.Unlock()

	// Unmarshal the device
	var newDevice device
	err := msgpack.Unmarshal(payload, &newDevice)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling device")
		return nil
	}

	// If the device already exists, track delivery, ack it, and return
	var existingDevice device
	err = b.database.Preload(clause.Associations).Where("address = ?", newDevice.Address).First(&existingDevice).Error
	if err == nil {
		return &existingDevice
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up device")
	}

	// Find the user this new device is for
	var targetUser user
	err = b.database.Preload("Devices.Signature").Preload(clause.Associations).First(&targetUser, "id = ?", newDevice.UserID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"user":    newDevice.UserID,
				"device":  newDevice.ID,
				"address": newDevice.Address,
				"peer":    peer,
			}).Warn("rejecting received device because we do not have the specified user")
			return nil
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
		return nil
	}

	// Save it
	err = b.database.Create(&newDevice).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving new device")
	}

	// Inform the UI if this is a new sync device
	if newDevice.UserID == b.currentUserID() {
		rd := b.getRemoteDevice(newDevice.Address)
		online := rd.connectedSockets.Load() > 0
		var lastSeen int64
		if peer == newDevice.Address {
			lastSeen = time.Now().Unix()
		}

		b.ui.DeviceAdded(Device{
			ID:        newDevice.ID,
			Name:      newDevice.Name,
			Address:   newDevice.Address,
			CreatedAt: newDevice.Timestamp,
			LastSeen:  lastSeen,
			Local:     false,
			Online:    online,
		})
	}

	return &newDevice
}

//
// Helper functions for looking up devices in other parts of the codebase
//

func (b *Bounce) getDeviceFromAddress(address string) (device, bool) {
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

func (b *Bounce) isSyncDevice(dev device) bool {
	return dev.UserID == b.currentUserID()
}

func validDeviceName(name string) bool {
	if name != strings.TrimSpace(name) {
		return false
	}

	if strings.Contains(name, "\n") {
		return false
	}

	return utf8.ValidString(name) && utf8.RuneCountInString(name) <= MaximumNameLength
}

//
// When testing if a set of devices are all valid additions to a device group, we need to test adding them
// in the order they were created, so we implement the sort interface for a slice of devices.
//

type devices []device

func (ds devices) Len() int {
	return len(ds)
}
func (ds devices) Swap(i, j int) {
	ds[i], ds[j] = ds[j], ds[i]
}
func (ds devices) Less(i, j int) bool {
	return ds[i].getTimestamp() < ds[j].getTimestamp()
}
