package chat

import (
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Basekick-Labs/msgpack/v6"
	log "github.com/sirupsen/logrus"
	"github.com/zeebo/blake3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var handleDevicesMutex sync.Mutex

// A device represents an instance of bounce
type device struct {
	ID             ID `gorm:"primary_key;"`
	Nonce          nonce
	Name           string `msgpack:"-"`
	UserID         ID
	Address        string `gorm:"uniqueIndex"`
	Network        string
	Timestamp      int64
	SavedAt        int64 `msgpack:"-"`
	LastSeen       int64 `msgpack:"-"`
	RevokedAt      int64
	ECDHPublicKey  []byte `msgpack:"-"`
	ECDHPrivateKey []byte `msgpack:"-"`
	Signature      *introductionSignature
	SelfSignature  []byte
}

func (d *device) getNonce() nonce {
	return d.Nonce
}

// A device's ID is generated from who it belongs to, its address, the network that address is on, when it was
// created, and the introduction that vouches for it.  Revocation is set by later updates and so it isn't included.
// The network is a separate hashed field, not a prefix on the address: the bare address stays what is signed and
// recovered back into the key, while the network term makes (address, network) jointly the device's identity.
// SelfSignature signs this ID, so the signature itself is not part of the hash.
func (d *device) generateID() ID {
	ch := newContentHasher(typeDevice, d.Nonce).
		id(d.UserID).
		bytes([]byte(d.Address)).
		bytes([]byte(d.Network)).
		int64(d.Timestamp)
	if d.Signature == nil {
		ch.uint16(0)
	} else {
		ch.uint16(1).
			bytes([]byte(d.Signature.PreexistingDevice)).
			bytes(d.Signature.SignatureOfNewDevice).
			bytes(d.Signature.SignatureOfPreexistingDevice)
	}
	return ch.generate()
}

const deviceSelfSignatureDomain = "bounce-device-self-signature:"

func deviceSelfSignatureHash(deviceID ID) []byte {
	hasher := blake3.New()
	hasher.Write([]byte(deviceSelfSignatureDomain))
	hasher.Write(deviceID[:])
	return hasher.Sum(nil)
}

func (b *Bounce) signDevice(d *device) {
	d.SelfSignature = b.network.Sign(deviceSelfSignatureHash(d.ID))
}

func (b *Bounce) hasValidSelfSignature(d device) bool {
	return b.network.VerifySignature(d.Address, deviceSelfSignatureHash(d.ID), d.SelfSignature)
}

func (d *device) BeforeCreate(tx *gorm.DB) error {
	if !idMatchesContent(d) {
		return errors.New("device ID is not generated from its content")
	}
	if len(d.SelfSignature) == 0 {
		return errors.New("device self-signature is missing")
	}
	if d.Signature != nil {
		d.Signature.ID = d.ID
		d.Signature.DeviceID = d.ID
	}
	d.RevokedAt = 0 // Revoked status can only be set by signed updates
	d.SavedAt = time.Now().Unix()
	return nil
}

// AfterCreate evicts this device's address from the encrypted device cache and from any user's list of encrypted
// devices.  A device row only exists once its self-signature and any required introduction have verified, while
// claiming an address as an encrypted device is a bare assertion by one user, so when the two disagree the device
// wins.  Without this a contact could claim another user's device address as their own encrypted device before we met that device, and
// every frame to it would be refused as unsendable to an encrypted device from then on.
func (d *device) AfterCreate(tx *gorm.DB) error {
	peerIdentitiesChanged()
	encryptedDeviceCacheMutex.Lock()
	claimedBy, squatted := encryptedDeviceCache[d.Address]
	if squatted {
		delete(encryptedDeviceCache, d.Address)
	}
	encryptedDeviceCacheMutex.Unlock()
	if squatted {
		log.WithFields(log.Fields{
			"address":    d.Address,
			"claimed_by": claimedBy,
			"device":     d.ID,
		}).Warn("evicting encrypted device claim for an address that is now a verified device")
	}

	// Remove the address from any user that lists it as an encrypted device, so it does not come back the next
	// time the cache is rebuilt from the database
	var claimants []user
	err := tx.Model(&user{}).Select("id", "encrypted_devices").Where("encrypted_devices LIKE ?", "%"+d.Address+"%").Find(&claimants).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error":   err.Error(),
			"address": d.Address,
		}).Error("error looking up users claiming a device address as an encrypted device")
		return nil
	}
	for _, claimant := range claimants {
		kept := []string{}
		for _, addr := range strings.Split(claimant.EncryptedDevices, ",") {
			if addr != "" && addr != d.Address {
				kept = append(kept, addr)
			}
		}
		err := tx.Table("users").Where("id = ?", claimant.ID).Update("encrypted_devices", strings.Join(kept, ",")).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":   err.Error(),
				"address": d.Address,
				"user":    claimant.ID,
			}).Error("error removing device address from a user's encrypted devices")
		}
	}

	return nil
}

func (d *device) AfterDelete(tx *gorm.DB) error {
	if d.ID == NilID {
		return nil
	}
	err := tx.Where("device_id = ?", d.ID).Delete(&introductionSignature{}).Error
	if err != nil {
		return err
	}
	return tx.Where("frame_id = ? AND frame_type = ?", d.ID, typeDevice).Delete(&deliveryRecord{}).Error
}

func (d *device) getID() ID {
	return d.ID
}

func (d *device) getScope(myID ID) int {
	return scopeGlobal
}

func (d *device) getDestination(myID ID) ID {
	return NilID
}

func (d *device) getType() uint16 {
	return typeDevice
}

func (d *device) getPayload() []byte {
	bytes, err := msgpack.Marshal(d)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal device")
	}

	return bytes
}

func (d *device) getAuthor() ID {
	return d.UserID
}

func (d *device) getTimestamp() int64 {
	return d.Timestamp
}

func (d *device) getSavedAt() int64 {
	return d.SavedAt
}

func (b *Bounce) deviceIntroductionAllowed(d device) bool {
	if d.Signature == nil {
		return true
	}

	var preexistingDevice device
	err := b.database.Select("id", "revoked_at").Where("address = ?", d.Signature.PreexistingDevice).First(&preexistingDevice).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"address": d.Signature.PreexistingDevice,
			}).Error("preexisting device not found for device")
			return false
		} else {
			log.WithFields(log.Fields{
				"address": d.Signature.PreexistingDevice,
				"error":   err.Error(),
			}).Fatal("database error looking up preexisting device")
		}
	}
	if preexistingDevice.RevokedAt != 0 {
		var revoke updateDevice
		err = b.database.Where("target = ? AND type = ? AND timestamp = ?", preexistingDevice.ID, updateDeviceTypeRevoke, preexistingDevice.RevokedAt).First(&revoke).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"address": d.Signature.PreexistingDevice,
				}).Error("revoke not found for revoked device")
				return false
			} else {
				log.WithFields(log.Fields{
					"address": d.Signature.PreexistingDevice,
					"error":   err.Error(),
				}).Fatal("database error looking up device revoke")
			}
		}
		devicesToKeep, err := introducedDevicesToKeep(revoke)
		if err != nil {
			log.WithFields(log.Fields{
				"signer": d.Signature.PreexistingDevice,
				"error":  err.Error(),
			}).Warn("ignoring device with invalid revoke keep list")
			return false
		}
		keep := false
		for _, deviceID := range devicesToKeep {
			if deviceID == d.ID {
				keep = true
				break
			}
		}
		if !keep {
			log.WithFields(log.Fields{
				"signer": d.Signature.PreexistingDevice,
			}).Warn("ignoring device introduced by revoked device")
			return false
		}
	}

	return true
}

// Select admissible carried devices. Children become eligible only after their parent has been admitted.
func (b *Bounce) admissibleCarriedDevices(carriedDevices []device) []device {
	admissibleDevices := []device{}
	pendingDevices := map[string]device{}
	for _, dev := range carriedDevices {
		if _, exists := b.getDeviceFromAddress(dev.Address); exists {
			admissibleDevices = append(admissibleDevices, dev)
		} else {
			pendingDevices[dev.Address] = dev
		}
	}
	children := map[string][]device{}
	devicesToCheck := []device{}
	for _, dev := range pendingDevices {
		if dev.Signature != nil {
			if _, parentPending := pendingDevices[dev.Signature.PreexistingDevice]; parentPending {
				children[dev.Signature.PreexistingDevice] = append(children[dev.Signature.PreexistingDevice], dev)
				continue
			}
		}
		devicesToCheck = append(devicesToCheck, dev)
	}
	newlyAdmitted := map[string]bool{}
	for len(devicesToCheck) > 0 {
		dev := devicesToCheck[0]
		devicesToCheck = devicesToCheck[1:]
		parentAdmitted := dev.Signature != nil && newlyAdmitted[dev.Signature.PreexistingDevice]
		if !parentAdmitted && !b.deviceIntroductionAllowed(dev) {
			continue
		}
		admissibleDevices = append(admissibleDevices, dev)
		newlyAdmitted[dev.Address] = true
		devicesToCheck = append(devicesToCheck, children[dev.Address]...)
	}
	return admissibleDevices
}

func (b *Bounce) saveCarriedDevices(carriedDevices []device) []device {
	admissibleDevices := b.admissibleCarriedDevices(carriedDevices)
	for _, dev := range admissibleDevices {
		if _, exists := b.getDeviceFromAddress(dev.Address); exists {
			continue
		}
		err := b.database.Clauses(clause.OnConflict{DoNothing: true}).Create(&dev).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error saving carried device")
		}
	}
	return admissibleDevices
}

func (b *Bounce) handleDevice(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	handleDevicesMutex.Lock()
	defer handleDevicesMutex.Unlock()

	// Unmarshal the device
	var d device
	err := msgpack.Unmarshal(payload, &d)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling device")
		return nil, false
	}

	// Make sure the ID is generated from the content
	if !idMatchesContent(&d) {
		log.WithFields(log.Fields{
			"peer":       peer,
			"id":         d.getID(),
			"content_id": d.generateID(),
		}).Error("rejecting device with an ID not generated from its content")
		return nil, false
	}

	// A device introduced by a revoked device is accepted only when the revoke retained it
	if !b.deviceIntroductionAllowed(d) {
		return nil, false
	}

	// Ignore anything from a blocked user
	if blockedUser(d.getAuthor()) {
		log.WithFields(log.Fields{
			"id":     d.ID,
			"author": d.getAuthor(),
		}).Warn("ignoring device from blocked user")

		if peerDev, ok := b.getDeviceFromAddress(peer); ok {
			if !blockedUser(peerDev.UserID) {
				go b.sendAck(peer, typeDevice, d.ID)
			}
		}
		return nil, false
	}

	// Check if this device already exists, acking but ignoring it if the address exists under another ID
	var existingDevice device
	err = b.database.Preload(clause.Associations).Where("address = ?", d.Address).First(&existingDevice).Error
	if err == nil {
		if existingDevice.ID == d.ID {
			return &existingDevice, false
		} else {
			go b.sendAck(peer, typeDevice, d.ID)
			return nil, false
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up device")
	}

	// Find the user this new device is for
	var targetUser user
	err = b.database.Preload("Devices.Signature").Preload(clause.Associations).First(&targetUser, "id = ?", d.UserID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"user":    d.UserID,
				"device":  d.ID,
				"address": d.Address,
				"peer":    peer,
			}).Warn("rejecting received device because we do not have the specified user")
			return nil, false
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up user while receiving new device")
		}
	}

	// Make sure this device is properly introduced by checking if adding the device to the user would result in an
	// invalid device group
	if !b.isValidAddition(targetUser, d) {
		log.WithFields(log.Fields{
			"user":    targetUser.ID,
			"device":  d.ID,
			"address": d.Address,
			"peer":    peer,
		}).Warn("rejecting received device because it would result in an invalid device group")
		return nil, false
	}

	// Save it
	err = b.database.Create(&d).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error saving new device")
		return nil, false
	}

	// Inform the UI if this is a new sync device
	if d.UserID == b.currentUserID() {
		rd := b.getRemoteDevice(d.Address)
		online := rd.connectedSockets() > 0
		var lastSeen int64
		if peer == d.Address {
			lastSeen = time.Now().Unix()
		}

		if pendingSyncDeviceOffers.isExpectedFrom(syncDeviceCompletionKey(b.network.Address(), d.Address)) {
			b.ui.NewSyncDeviceAdded()
		}
		b.ui.DeviceAdded(Device{
			ID:        d.ID,
			Name:      d.Name,
			Address:   d.Address,
			CreatedAt: d.Timestamp,
			LastSeen:  lastSeen,
			Local:     false,
			Online:    online,
		})
		b.updateUserOnlineStatus(d.Address)
		go b.sendReferences(d.Address)
	}

	return &d, true
}

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

func (b *Bounce) deviceWasRevokedAt(address string, ts int64) bool {
	dev, ok := b.getDeviceFromAddress(address)
	if !ok {
		return false
	}

	if dev.RevokedAt == 0 {
		return false
	}

	return dev.RevokedAt < ts
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
