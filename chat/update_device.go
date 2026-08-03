package chat

import (
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var updateDeviceMutex sync.Mutex

var updateDeviceTypeUpdateName = uint16(0)
var updateDeviceTypeRevoke = uint16(1)
var updateDeviceTypeSetPublicKey = uint16(2)

var errInvalidDeviceName = errors.New("invalid name")
var errUnsupportedUpdateDeviceType = errors.New("unsupported update device type")
var errCannotRevokeAnotherUsersDevice = errors.New("cannot revoke device owned by another user")
var errDeviceAlreadyRevoked = errors.New("device has already been revoked")
var errCannotRevokeLastDevice = errors.New("cannot revoke last device")

type updateDevice struct {
	SignedFrame
	cachedEncoding
	ID        uuid.UUID `gorm:"type:uuid;primary_key;"`
	Target    uuid.UUID
	Type      uint16
	Data      []byte
	Timestamp int64
	SavedAt   int64     `msgpack:"-"`
	Author    uuid.UUID `msgpack:"-"`
}

func (ud *updateDevice) BeforeCreate(tx *gorm.DB) error {
	if ud.ID == uuid.Nil {
		return errors.New("update device ID must be set before creation")
	}
	ud.SavedAt = time.Now().Unix()

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
	// Destination is not needed in sync or global scoped frames, and
	// a device ID is not a valid destination, however we use this to
	// track which devices need to be updated during a catch up.
	return ud.Target
}

func (ud *updateDevice) getType() uint16 {
	return typeUpdateDevice
}

func (ud *updateDevice) getPayload() []byte {
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

func (ud *updateDevice) getSavedAt() int64 {
	return ud.SavedAt
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

func (b *Bounce) handleUpdateDevice(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	updateDeviceMutex.Lock()
	defer updateDeviceMutex.Unlock()

	// Unpack the signed container
	sc, err := b.unpackSignedContainer(payload)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unpacking signed container for update device")
		return nil, false
	}
	var ud updateDevice
	err = msgpack.Unmarshal(sc.Payload, &ud)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling update device")
		return nil, false
	}
	ud.OriginalPayload = sc.Payload
	ud.Signature = sc.Signature
	ud.Signer = sc.Signer

	// Ignore anything from a blocked user
	if blockedUser(ud.getAuthor()) {
		log.WithFields(log.Fields{
			"id":     ud.ID,
			"author": ud.getAuthor(),
		}).Warn("ignoring update device from blocked user")

		if peerDev, ok := b.getDeviceFromAddress(peer); ok {
			if !blockedUser(peerDev.UserID) {
				go b.sendAck(peer, typeUpdateDevice, ud.ID)
			}
		}
		return nil, false
	}

	if err = ud.validPayload(); err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("ignoring update device with invalid payload")
		return nil, false
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
			return nil, false
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
		return nil, false
	}

	// Make sure the signing device was not revoked before creating this
	var signerDevice device
	err = b.database.Select("revoked_at").Where("address = ?", ud.Signer).First(&signerDevice).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"address": ud.Signer,
			}).Error("signer device not found for update device")
			return nil, false
		} else {
			log.WithFields(log.Fields{
				"address": ud.Signer,
				"error":   err.Error(),
			}).Fatal("database error looking up signing device")
		}
	}
	if signerDevice.RevokedAt != 0 && signerDevice.RevokedAt < ud.Timestamp {
		log.WithFields(log.Fields{
			"id":     ud.ID,
			"signer": ud.Signer,
		}).Warn("ignoring update device signed by revoked device")
		go b.sendAck(peer, typeUpdateDevice, ud.ID)
		return nil, false
	}

	// If we already have this update, we just mark that this peer has it too and return
	var existingUD updateDevice
	err = b.database.Where("id = ?", ud.ID).First(&existingUD).Error
	if err == nil {
		return &existingUD, false
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
	if !catchUp {
		b.updateDeviceState(ud.Target)
	}

	return &ud, true
}

func (b *Bounce) updateDeviceState(deviceID uuid.UUID) {
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
	publicKey := []byte{}

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
		if b.deviceWasRevokedAt(ud.Signer, ud.Timestamp) {
			continue
		}

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
		case updateDeviceTypeSetPublicKey:
			publicKey = ud.Data
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

		go b.ui.DeviceRenamed(deviceID, name)
	}

	if revokedAt != 0 && (d.RevokedAt == 0 || revokedAt < d.RevokedAt) {
		if d.Address == b.network.Address() {
			// This device has been revoked, remove all files and close the app
			log.Info("this device has been revoked")
			err := b.database.Table("devices").Where("id = ?", deviceID).Updates(map[string]interface{}{"revoked_at": revokedAt}).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error":     err.Error(),
					"device_id": deviceID,
				}).Fatal("database error updating device revoked at")
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
			b.devicePool.revokedMutex.Lock()
			b.devicePool.revokedDevices[d.Address] = true
			b.devicePool.revokedMutex.Unlock()

			b.revokeUnauthorizedDeviceActions(d.Address, revokedAt)

			if d.UserID == b.currentUserID() {
				go b.ui.DeviceRevoked(deviceID)
			}

			if sendDirect {
				rd := b.getRemoteDevice(d.Address)
				if rd.connectedSockets() > 0 {
					rd.messages <- &revokeFrame
				}
			}
		}
	}

	if len(publicKey) > 0 && d.Address != b.network.Address() {
		err := b.database.Table("devices").Where("id = ?", deviceID).Updates(map[string]interface{}{"ecdh_public_key": publicKey}).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":     err.Error(),
				"device_id": deviceID,
			}).Fatal("database error updating device public key")
		}
	}
}

func (b *Bounce) revokeUnauthorizedDeviceActions(address string, revokedAt int64) {
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
		b.ui.GroupDeleted(GroupDeleted{
			Group: gc.ID,
			Actor: uuid.Nil,
		})
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
		err := b.database.Clauses(clause.Returning{}).Where("id = ?", gm.ID).Delete(&groupMessage{}).Error
		if err != nil {
			log.WithFields(log.Fields{
				"id":    gm.ID,
				"error": err.Error(),
			}).Fatal("error deleting unauthorized group message")
		}
		b.ui.DeleteItem(gm.ID)
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
	}
	for target, _ := range groupsToUpdate {
		b.reloadGroupConsensusSince(target, revokedAt)
		b.writeGroupConsensus(target)
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
		b.ui.DeleteItem(uu.ID)
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

func (b *Bounce) RenameDevice(deviceID uuid.UUID, name string) error {
	var esd encryptedSyncDevice
	err := b.database.First(&esd, "id = ?", deviceID).Error
	if err == nil {
		return b.applyAndBroadcastUpdateUser(updateUser{
			ID:        uuid.New(),
			Target:    b.currentUserID(),
			Timestamp: time.Now().Unix(),
			Type:      updateUserTypeSetEncryptedDeviceName,
			Data:      []byte(esd.Address + ":" + name),
		})
	}

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

func (b *Bounce) RevokeDevice(deviceID uuid.UUID) error {
	var esd encryptedSyncDevice
	err := b.database.First(&esd, "id = ?", deviceID).Error
	if err == nil {
		return b.applyAndBroadcastUpdateUser(updateUser{
			ID:        uuid.New(),
			Target:    b.currentUserID(),
			Timestamp: time.Now().Unix(),
			Type:      updateUserTypeRemoveEncryptedDevice,
			Data:      []byte(esd.Address),
		})
	}

	var dev device
	err = b.database.First(&dev, "id = ?", deviceID).Error
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

	err = b.applyAndBroadcastUpdateDevice(ud)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error applying update device that revokes a device")
	}

	return b.rollKeys()
}

func (b *Bounce) createAndShareDeviceECDHKey() {
	d, ok := b.getDeviceFromAddress(b.network.Address())
	if !ok {
		log.Error("cannot set device ECDH key before device exists")
		return
	}

	curve := ecdh.X25519()
	privateKey, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error generating x25519 private key")
	}
	publicKey := privateKey.PublicKey()

	privateKeyBytes := privateKey.Bytes()
	publicKeyBytes := publicKey.Bytes()

	err = b.database.Table("devices").Where("address = ?", b.network.Address()).Updates(map[string]interface{}{"ecdh_public_key": publicKeyBytes, "ecdh_private_key": privateKeyBytes}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error setting device ECDH key")
	}

	b.applyAndBroadcastUpdateDevice(updateDevice{
		ID:        uuid.New(),
		Target:    d.ID,
		Type:      updateDeviceTypeSetPublicKey,
		Data:      publicKeyBytes,
		Timestamp: time.Now().Unix(),
		Author:    b.currentUserID(),
	})
}

func (b *Bounce) applyAndBroadcastUpdateDevice(ud updateDevice) error {
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
