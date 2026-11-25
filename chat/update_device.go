package chat

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

var updateDeviceMutex sync.Mutex

var updateDeviceTypeUpdateName = uint16(0)
var updateDeviceTypeRevoke = uint16(1)
var updateDeviceTypeSetPublicKey = uint16(2)

var errInvalidDeviceName = errors.New("invalid name")
var errInvalidDeviceRevokePayload = errors.New("invalid device revoke payload")
var errUnsupportedUpdateDeviceType = errors.New("unsupported update device type")
var errCannotRevokeAnotherUsersDevice = errors.New("cannot revoke device owned by another user")
var errDeviceAlreadyRevoked = errors.New("device has already been revoked")
var errCannotRevokeLastDevice = errors.New("cannot revoke last device")

type updateDevice struct {
	SignedFrame
	ID        ID `gorm:"primary_key;"`
	Nonce     nonce
	Target    ID
	Type      uint16
	Data      []byte
	Timestamp int64
	SavedAt   int64 `msgpack:"-"`
	Author    ID    `msgpack:"-"`
}

func (ud *updateDevice) getNonce() nonce {
	return ud.Nonce
}

func (ud *updateDevice) generateID() ID {
	return newContentHasher(typeUpdateDevice, ud.Nonce).
		id(ud.Target).
		int64(ud.Timestamp).
		uint16(ud.Type).
		bytes(ud.Data).
		generate()
}

func (ud *updateDevice) BeforeCreate(tx *gorm.DB) error {
	if !idMatchesContent(ud) {
		return errors.New("update device ID is not generated from its content")
	}
	ud.SavedAt = time.Now().Unix()

	return nil
}

func (ud *updateDevice) getID() ID {
	return ud.ID
}

func (ud *updateDevice) getScope(myID ID) int {
	if ud.Type == updateDeviceTypeRevoke {
		return scopeGlobal
	}

	return scopeSync
}

func (ud *updateDevice) getDestination(myID ID) ID {
	// Destination is not needed in sync or global scoped frames, and
	// a device ID is not a valid destination, however we use this to
	// track which devices need to be updated during a catch up.
	return ud.Target
}

func (ud *updateDevice) getType() uint16 {
	return typeUpdateDevice
}

func (ud *updateDevice) getPayload() []byte {
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

	return bytes
}

func (ud *updateDevice) getAuthor() ID {
	return ud.Author
}

func (ud *updateDevice) getTimestamp() int64 {
	return ud.Timestamp
}

func (ud *updateDevice) getSavedAt() int64 {
	return ud.SavedAt
}

func introducedDevicesToKeep(ud updateDevice) ([]ID, error) {
	devices := []ID{}
	if err := msgpack.Unmarshal(ud.Data, &devices); err != nil {
		return nil, err
	}
	if len(devices) > maximumDevicesPerUser {
		return nil, errInvalidDeviceRevokePayload
	}
	return devices, nil
}

func (ud *updateDevice) validPayload() error {
	switch ud.Type {
	case updateDeviceTypeUpdateName:
		if !validDeviceName(string(ud.Data)) {
			return errInvalidDeviceName
		}
	case updateDeviceTypeSetPublicKey:
		if _, err := ecdh.X25519().NewPublicKey(ud.Data); err != nil {
			return errInvalidKeyMaterial
		}
	case updateDeviceTypeRevoke:
		_, err := introducedDevicesToKeep(*ud)
		return err
	default:
		return errUnsupportedUpdateDeviceType
	}

	return nil
}

func (b *Bounce) handleUpdateDevice(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	updateDeviceMutex.Lock()
	defer updateDeviceMutex.Unlock()

	// Unpack the signed container
	sc, err := b.unpackSignedContainer(typeUpdateDevice, payload)
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

	// Make sure the ID is generated from the content
	if !idMatchesContent(&ud) {
		log.WithFields(log.Fields{
			"peer":       peer,
			"id":         ud.getID(),
			"content_id": ud.generateID(),
		}).Error("rejecting update device with an ID not generated from its content")
		return nil, false
	}

	// Look up the device
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

	// Make sure the user that signed this update owns the device before checking block status
	if !b.signedByUser(sc, d.UserID) {
		log.WithFields(log.Fields{
			"id":     ud.ID,
			"target": ud.Target,
			"signer": ud.Signer,
		}).Warn("ignoring update device not signed by user who owns device")
		return nil, false
	}

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

	// Validate the payload
	if err = ud.validPayload(); err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("ignoring update device with invalid payload")
		return nil, false
	}

	// Make sure the signing device was not revoked
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

	// Do not allow frames from revoked devices
	if signerDevice.RevokedAt != 0 {
		log.WithFields(log.Fields{
			"id":     ud.ID,
			"signer": ud.Signer,
		}).Warn("ignoring update device signed by revoked device")
		if peer != "" && peer != b.network.Address() {
			go b.sendAck(peer, typeUpdateDevice, ud.ID)
		}
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

func (b *Bounce) updateDeviceState(deviceID ID) {
	var d device
	err := b.database.Where("id = ?", deviceID).First(&d).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"device_id": deviceID,
				"error":     err.Error(),
			}).Error("device not found when updating device state")
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
			log.Info("this device has been revoked")
			err := b.database.Table("devices").Where("id = ?", deviceID).Updates(map[string]interface{}{"revoked_at": revokedAt}).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error":     err.Error(),
					"device_id": deviceID,
				}).Fatal("database error updating device revoked at")
			}
			go b.goOfflineAfterRevocation()
			go b.ui.DeviceRevoked(deviceID)
		} else {
			devicesToKeep, err := introducedDevicesToKeep(revokeFrame)
			if err != nil {
				log.WithFields(log.Fields{
					"error":     err.Error(),
					"device_id": deviceID,
				}).Error("error decoding devices to keep in device revocation")
				return
			}
			err = b.database.Table("devices").Where("id = ?", deviceID).Updates(map[string]interface{}{"revoked_at": revokedAt}).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error":     err.Error(),
					"device_id": deviceID,
				}).Fatal("database error updating device revoked at")
			}
			b.devicePool.revokedMutex.Lock()
			b.devicePool.revokedDevices[d.Address] = true
			b.devicePool.revokedMutex.Unlock()

			b.revokeUnauthorizedDeviceActions(d.Address, revokedAt, devicesToKeep)

			if d.UserID == b.currentUserID() {
				go b.ui.DeviceRevoked(deviceID)
			}

			if sendDirect {
				rd := b.getRemoteDevice(d.Address)
				rd.send(&revokeFrame)
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

func (b *Bounce) revokeUnauthorizedDeviceActions(address string, revokedAt int64, devicesToKeep []ID) {
	// Find any group creations signed by this device after it was revoked and delete those groups
	var unauthorizedGroupCreations []groupCreation
	err := b.database.Where("signer = ? AND timestamp > ?", address, revokedAt).Find(&unauthorizedGroupCreations).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up unauthorized group creations")
	}
	for _, gc := range unauthorizedGroupCreations {
		err = deleteEach[group](b.database, "id = ?", gc.ID)
		if err != nil {
			log.WithFields(log.Fields{
				"id":    gc.ID,
				"error": err.Error(),
			}).Fatal("error deleting unauthorized group")
		}
		go b.ui.GroupDeleted(GroupDeleted{
			Group: gc.ID,
			Actor: NilID,
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
		err := deleteEach[groupMessage](b.database, "id = ?", gm.ID)
		if err != nil {
			log.WithFields(log.Fields{
				"id":    gm.ID,
				"error": err.Error(),
			}).Fatal("error deleting unauthorized group message")
		}
		go b.ui.DeleteItem(gm.ID)
	}

	// Find any update groups signed by this device after it was revoked and delete them, then re-do group consensus
	var unauthorizedUpdateGroups []updateGroup
	err = b.database.Where("signer = ? AND timestamp > ?", address, revokedAt).Find(&unauthorizedUpdateGroups).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error looking up unauthorized update groups")
	}
	groupsToUpdate := map[ID]bool{}
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
	usersToUpdate := map[ID]bool{}
	for _, uu := range unauthorizedUpdateUsers {
		usersToUpdate[uu.Target] = true
		err = b.database.Delete(&uu).Error
		if err != nil {
			log.WithFields(log.Fields{
				"id":    uu.ID,
				"error": err.Error(),
			}).Fatal("error deleting unauthorized update user")
		}
		go b.ui.DeleteItem(uu.ID)
	}
	for target, _ := range usersToUpdate {
		b.updateUserState(target)
	}

	// Find devices this device introduced that the revoke did not retain, and recursively clear their actions as well
	var unauthorizedDevices []device
	query := b.database.
		Joins("JOIN introduction_signatures ON introduction_signatures.device_id = devices.id").
		Where("introduction_signatures.preexisting_device = ?", address)
	if len(devicesToKeep) > 0 {
		query = query.Where("devices.id NOT IN ?", devicesToKeep)
	}
	err = query.Find(&unauthorizedDevices).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up unauthorized devices")
	}
	for _, dev := range unauthorizedDevices {
		b.devicePool.revokedMutex.Lock()
		b.devicePool.revokedDevices[dev.Address] = true
		b.devicePool.revokedMutex.Unlock()
		b.getRemoteDevice(dev.Address).shutdown()

		err = b.database.Delete(&dev).Error
		if err != nil {
			log.WithFields(log.Fields{
				"id":    dev.ID,
				"error": err.Error(),
			}).Fatal("database error deleting unauthorized devices")
		}
		if dev.UserID == b.currentUserID() {
			go b.ui.DeviceRevoked(dev.ID)
		}

		b.revokeUnauthorizedDeviceActions(dev.Address, 0, nil)
	}
}

func (b *Bounce) RenameDevice(deviceID ID, name string) error {
	var esd encryptedSyncDevice
	err := b.database.First(&esd, "id = ?", deviceID).Error
	if err == nil {
		return b.applyAndBroadcastUpdateUser(updateUser{
			Nonce:     newNonce(),
			Target:    b.currentUserID(),
			Timestamp: time.Now().Unix(),
			Type:      updateUserTypeSetEncryptedDeviceName,
			Data:      []byte(esd.Address + ":" + name),
		})
	}

	ud := updateDevice{
		Nonce:     newNonce(),
		Target:    deviceID,
		Type:      updateDeviceTypeUpdateName,
		Data:      []byte(name),
		Timestamp: time.Now().Unix(),
		Author:    b.currentUserID(),
	}

	return b.applyAndBroadcastUpdateDevice(ud)
}

func (b *Bounce) goOfflineAfterRevocation() {
	b.networkIsOnline.Store(false)

	b.devicePool.deviceMutex.Lock()
	devices := make([]*remoteDevice, 0, len(b.devicePool.devices))
	for _, rd := range b.devicePool.devices {
		devices = append(devices, rd)
	}
	b.devicePool.devices = make(map[string]*remoteDevice)
	b.devicePool.deviceMutex.Unlock()

	for _, rd := range devices {
		rd.shutdown()
	}

	b.network.Shutdown()
}

func (b *Bounce) RevokeDevice(deviceID ID) error {
	var esd encryptedSyncDevice
	err := b.database.First(&esd, "id = ?", deviceID).Error
	if err == nil {
		return b.applyAndBroadcastUpdateUser(updateUser{
			Nonce:     newNonce(),
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

	unrevokedDevices := 0
	for _, d := range profile.Devices {
		if d.RevokedAt == 0 {
			unrevokedDevices += 1
		}
	}
	if unrevokedDevices <= 1 {
		return errCannotRevokeLastDevice
	}

	devicesToKeep := []ID{}
	for _, d := range profile.Devices {
		if d.Signature != nil && d.Signature.PreexistingDevice == dev.Address {
			devicesToKeep = append(devicesToKeep, d.ID)
		}
	}
	data, err := msgpack.Marshal(devicesToKeep)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling devices to keep when revoking a device")
	}

	ud := updateDevice{
		Nonce:     newNonce(),
		Target:    deviceID,
		Type:      updateDeviceTypeRevoke,
		Data:      data,
		Timestamp: time.Now().Unix(),
		Author:    b.currentUserID(),
	}

	err = b.applyAndBroadcastUpdateDevice(ud)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error applying update device that revokes a device")
		return err
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
		Nonce:     newNonce(),
		Target:    d.ID,
		Type:      updateDeviceTypeSetPublicKey,
		Data:      publicKeyBytes,
		Timestamp: time.Now().Unix(),
		Author:    b.currentUserID(),
	})
}

func (b *Bounce) applyAndBroadcastUpdateDevice(ud updateDevice) error {
	// Give the update its nonce and derive the ID from content
	if ud.Nonce == noNonce {
		ud.Nonce = newNonce()
	}
	ud.ID = ud.generateID()

	var err error
	ud.OriginalPayload, err = msgpack.Marshal(&ud)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling update device")
	}
	sc := b.createSignedContainer(typeUpdateDevice, ud.OriginalPayload)
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

// attachDeviceRevocations attaches the signed effective revoke frame for each revoked device in u.Devices so
// recipients learn which carried devices are already revoked before dialing them or relying on catch-up order.
func (b *Bounce) attachDeviceRevocations(u *user) {
	revokedAtByID := make(map[ID]int64)
	revokedIDs := make([]ID, 0)
	for _, dev := range u.Devices {
		if dev.RevokedAt != 0 {
			if _, seen := revokedAtByID[dev.ID]; !seen {
				revokedAtByID[dev.ID] = dev.RevokedAt
				revokedIDs = append(revokedIDs, dev.ID)
			}
		}
	}
	if len(revokedIDs) == 0 {
		u.DeviceRevocations = nil
		return
	}

	var revokes []updateDevice
	err := b.database.
		Where("target IN ? AND type = ?", revokedIDs, updateDeviceTypeRevoke).
		Order("timestamp asc, id asc").
		Find(&revokes).Error
	if err != nil {
		log.WithFields(log.Fields{
			"user_id": u.ID,
			"error":   err.Error(),
		}).Fatal("database error looking up device revocations to attach to user")
	}

	seenTarget := make(map[ID]bool, len(revokedIDs))
	attached := make([][]byte, 0, len(revokedIDs))
	for i := range revokes {
		ud := revokes[i]
		if seenTarget[ud.Target] || ud.Timestamp != revokedAtByID[ud.Target] {
			continue
		}
		seenTarget[ud.Target] = true
		attached = append(attached, ud.getPayload())
	}
	u.DeviceRevocations = attached
}

type carriedDeviceRevocation struct {
	timestamp int64
	id        ID
	payload   []byte
}

// applyCarriedDeviceRevocations verifies and applies signed device revocations bundled with a carried user record.
// Must not be called while holding b.consensusStore's lock, because updateDeviceState can reload group consensus.
func (b *Bounce) applyCarriedDeviceRevocations(peer string, u user) {
	if len(u.DeviceRevocations) == 0 {
		return
	}

	allowedTargets := make(map[ID]bool, len(u.Devices))
	for _, dev := range u.Devices {
		if dev.UserID == u.ID {
			allowedTargets[dev.ID] = true
		}
	}

	limit := len(u.DeviceRevocations)
	if limit > maximumDevicesPerUser {
		limit = maximumDevicesPerUser
	}

	seenID := make(map[ID]bool, limit)
	revocations := make([]carriedDeviceRevocation, 0, limit)
	for _, rawPayload := range u.DeviceRevocations[:limit] {
		sc, err := b.unpackSignedContainer(typeUpdateDevice, rawPayload)
		if err != nil {
			log.WithFields(log.Fields{
				"user_id": u.ID,
				"error":   err.Error(),
			}).Warn("ignoring carried device revocation with invalid signed container")
			continue
		}
		var ud updateDevice
		if err := msgpack.Unmarshal(sc.Payload, &ud); err != nil {
			log.WithFields(log.Fields{
				"user_id": u.ID,
				"error":   err.Error(),
			}).Warn("ignoring carried device revocation that failed to unmarshal")
			continue
		}
		if ud.Type != updateDeviceTypeRevoke || !allowedTargets[ud.Target] {
			log.WithFields(log.Fields{
				"user_id": u.ID,
				"target":  ud.Target,
				"type":    ud.Type,
			}).Warn("ignoring carried device update that is not a revocation for this user")
			continue
		}
		if seenID[ud.ID] {
			continue
		}
		seenID[ud.ID] = true
		revocations = append(revocations, carriedDeviceRevocation{
			timestamp: ud.Timestamp,
			id:        ud.ID,
			payload:   rawPayload,
		})
	}

	sort.Slice(revocations, func(i, j int) bool {
		if revocations[i].timestamp != revocations[j].timestamp {
			return revocations[i].timestamp < revocations[j].timestamp
		}
		return bytes.Compare(revocations[i].id[:], revocations[j].id[:]) < 0
	})

	fromPeer := peer != "" && peer != b.network.Address()
	for _, rev := range revocations {
		br, firstTimeSeeing := b.handleUpdateDevice(peer, rev.payload, false)
		if br == nil {
			continue
		}
		if fromPeer {
			go b.sendAck(peer, typeUpdateDevice, br.getID())
			b.markDeliveredTo(br, peer)
		}
		if firstTimeSeeing {
			b.broadcast(br)
		}
	}
}
