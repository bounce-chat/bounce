package chat

import (
	"errors"
	"io/ioutil"
	"os"
	"path/filepath"
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
var errCannotRevokeAnotherUsersDevice = errors.New("cannot revoke device owned by another user")
var errDeviceAlreadyRevoked = errors.New("device has already been revoked")
var errCannotRevokeLastDevice = errors.New("cannot revoke last device")

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
	// Destination is only required for user and group scopes, so we
	// do not need to provide one for this frame
	return uuid.Nil
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

	// Make sure the user that signed this update owns the device
	if !b.signedByUser(sc, d.UserID) {
		log.WithFields(log.Fields{
			"id":     ud.ID,
			"target": ud.Target,
			"signer": ud.Signer,
		}).Warn("ignoring update device not signed by user who owns device")
		return nil
	}

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

	b.updateDeviceState(ud.Target)

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
	err = b.database.Where("target = ?", deviceID).Order("timestamp asc").Find(&uds).Error
	if err != nil {
		log.WithFields(log.Fields{
			"device_id": deviceID,
			"error":     err.Error(),
		}).Fatal("database error looking up update devices")
	}

	var revokeFrame updateDevice
	sendDirect := false
	for _, ud := range uds {
		switch ud.Type {
		case updateDeviceTypeUpdateName:
			name = string(ud.Data)
		case updateDeviceTypeRevoke:
			if revokedAt == 0 {
				sendDirect = true
				revokeFrame = ud
				revokedAt = ud.Timestamp
			} else {
				if ud.Timestamp == 0 {
					// Once the revokedAt has been set to a non-zero value, it cannot be zero again
					continue
				} else if ud.Timestamp < revokedAt {
					sendDirect = true
					revokeFrame = ud
					revokedAt = ud.Timestamp
				}
			}
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
		if d.Address == b.network.Address() {
			// This device has been revoked, remove all files and close the app
			log.Info("this device has been revoked, cleaning up data and exiting")
			configDirectory := getConfigDirectory()
			dir, err := ioutil.ReadDir(configDirectory)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
					"path":  configDirectory,
				}).Error("error reading directory")
			}
			for _, d := range dir {
				err = os.RemoveAll(filepath.Join(configDirectory, d.Name()))
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
						"path":  filepath.Join(configDirectory, d.Name()),
					}).Error("error removing file")
				}
			}
			os.Exit(0)
		} else {
			err := b.database.Table("devices").Where("id = ?", deviceID).Updates(map[string]interface{}{"revoked_at": revokedAt}).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error":     err.Error(),
					"device_id": deviceID,
				}).Fatal("database error updating device revoked at")
			}
			b.devicePool.revokedDevices[d.Address] = true

			b.revokeUnauthorizedDeviceActions(d.Address, revokedAt)

			if d.UserID == b.currentUserID() {
				b.userInterface.DeviceRevoked(deviceID)
			}

			if sendDirect {
				rd := b.getRemoteDevice(d.Address)
				if rd.connectedSockets > 0 {
					rd.messages <- &revokeFrame
				}
			}
		}
	}
}

func (b *bounce) revokeUnauthorizedDeviceActions(address string, revokedAt int64) {
	// Find any group creations signed by this device after it was revoked and delete those groups
	var unauthorizedGroupCreations []groupCreation
	err := b.database.Where("signer = ? AND timestamp > ?", address, revokedAt).Find(&unauthorizedGroupCreations).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up unauthorized group creations")
	}
	for _, gc := range unauthorizedGroupCreations {
		err = b.database.Delete(&group{}, gc.ID).Error
		if err != nil {
			log.WithFields(log.Fields{
				"id":    gc.ID,
				"error": err.Error(),
			}).Fatal("error deleting unauthorized group")
		}
	}

	// Find any group messages signed by this device after it was revoked and delete those messages
	var unauthorizedGroupMessages []groupMessage
	err = b.database.Where("signer = ? AND written_at > ?", address, revokedAt).Find(&unauthorizedGroupMessages).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error looking up unauthorized group messages")
	}
	for _, gm := range unauthorizedGroupMessages {
		err := b.database.Where("id = ?", gm.ID).Delete(&groupMessage{}).Error
		if err != nil {
			log.WithFields(log.Fields{
				"id":    gm.ID,
				"error": err.Error(),
			}).Fatal("error deleting unauthorized group message")
		}
		b.userInterface.DeleteItem(gm.ID)
	}

	// Find any update groups signed by this device after it was revoked and delete them, then re-do group consensus
	var unauthorizedUpdateGroups []updateGroup
	err = b.database.Where("signer = ? AND timestamp > ?", address, revokedAt).Find(&unauthorizedUpdateGroups).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error looking up unauthorized update groups")
	}
	groupsToUpdate := map[uuid.UUID]bool{}
	for _, ug := range unauthorizedUpdateGroups {
		groupsToUpdate[ug.Target] = true
		err = b.database.Delete(&ug).Error
		if err != nil {
			log.WithFields(log.Fields{
				"id":    ug.ID,
				"error": err.Error(),
			}).Fatal("error deleting unauthorized update group")
		}
	}
	for target, _ := range groupsToUpdate {
		b.updateGroupConsensus(target)
	}

	// Find any update users signed by this device after it was revoked, then reset the user state
	var unauthorizedUpdateUsers []updateUser
	err = b.database.Where("signer = ? AND timestamp > ?", address, revokedAt).Find(&unauthorizedUpdateUsers).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error looking up unauthorized update users")
	}
	usersToUpdate := map[uuid.UUID]bool{}
	for _, uu := range unauthorizedUpdateUsers {
		usersToUpdate[uu.Target] = true
		err = b.database.Delete(&uu).Error
		if err != nil {
			log.WithFields(log.Fields{
				"id":    uu.ID,
				"error": err.Error(),
			}).Fatal("error deleting unauthorized update user")
		}
	}
	for target, _ := range usersToUpdate {
		b.updateUserState(target)
	}

	// Find any devices that were added by this device after it was revoked and delete them
	var unauthorizedDevices []device
	err = b.database.Preload("Devices.Signature").Where("devices.timestamp > ? AND introduction_signatures.preexisting_device = ?", revokedAt, address).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up unauthorized devices")
	}
	for _, dev := range unauthorizedDevices {
		err = b.database.Delete(&dev).Error
		if err != nil {
			log.WithFields(log.Fields{
				"id":    dev.ID,
				"error": err.Error(),
			}).Fatal("database error deleting unauthorized devices")
		}
	}
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
	var dev device
	err := b.database.First("id = ?", deviceID, &dev).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		} else {
			log.WithFields(log.Fields{
				"id":    deviceID,
				"error": err.Error(),
			}).Fatal("database error looking up device to revoke")
		}
	}

	if dev.UserID != b.currentUserID() {
		return errCannotRevokeAnotherUsersDevice
	}

	if dev.RevokedAt != 0 {
		return errDeviceAlreadyRevoked
	}

	profile, ok := b.currentUser()
	if !ok {
		log.Fatal("cannot revoke devices when no profile exists")
	}

	if len(profile.Devices) == 1 {
		return errCannotRevokeLastDevice
	}

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
