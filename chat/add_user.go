package chat

import (
	"encoding/binary"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	log "github.com/sirupsen/logrus"
	"github.com/zeebo/blake3"
	"gorm.io/gorm"
)

const maximumAddUserRecordSize = 256 * 1024
const addUserSignatureDomain = "bounce-add-user-signature:"

var handleAddUsersMutex sync.Mutex

// addUserSignatureDigest binds both user records, the nonce, timestamp, and both signing device addresses under a
// domain tag so neither party can re-mint an addUser frame unilaterally.
func addUserSignatureDigest(n nonce, timestamp int64, offerUser, requesterUser []byte, offerDevice, requesterDevice string) []byte {
	hasher := blake3.New()
	hasher.Write([]byte(addUserSignatureDomain))
	hasher.Write(n[:])
	hasher.Write(binary.BigEndian.AppendUint64(nil, uint64(timestamp)))
	hasher.Write(binary.BigEndian.AppendUint32(nil, uint32(len(offerUser))))
	hasher.Write(offerUser)
	hasher.Write(binary.BigEndian.AppendUint32(nil, uint32(len(requesterUser))))
	hasher.Write(requesterUser)
	hasher.Write(binary.BigEndian.AppendUint32(nil, uint32(len(offerDevice))))
	hasher.Write([]byte(offerDevice))
	hasher.Write(binary.BigEndian.AppendUint32(nil, uint32(len(requesterDevice))))
	hasher.Write([]byte(requesterDevice))
	return hasher.Sum(nil)
}

// An addUser frame contains proof that two users became friends, where one of the users is us.  It is created as part
// of the final step in adding a user over the wire.
type addUser struct {
	ID                 ID `gorm:"primary_key;"`
	Nonce              nonce
	Xor                ID
	Timestamp          int64
	SavedAt            int64 `msgpack:"-"`
	OfferUser          []byte
	RequesterUser      []byte
	OfferDevice        string
	RequesterDevice    string
	OfferSignature     []byte
	RequesterSignature []byte
}

func (au *addUser) getNonce() nonce {
	return au.Nonce
}

// An add user's ID is generated from everything in it: the two user records, which are packed as the bytes they
// travel as, the two devices that made the introduction, and their signatures over the records
func (au *addUser) generateID() ID {
	return newContentHasher(typeAddUser, au.Nonce).
		id(au.Xor).
		int64(au.Timestamp).
		bytes(au.OfferUser).
		bytes(au.RequesterUser).
		bytes([]byte(au.OfferDevice)).
		bytes([]byte(au.RequesterDevice)).
		bytes(au.OfferSignature).
		bytes(au.RequesterSignature).
		generate()
}

func (au *addUser) BeforeCreate(tx *gorm.DB) error {
	if !idMatchesContent(au) {
		return errors.New("add user ID is not generated from its content")
	}
	au.SavedAt = time.Now().Unix()
	return nil
}

func (au *addUser) AfterDelete(tx *gorm.DB) error {
	if au.ID == NilID {
		return nil
	}
	return tx.Where("frame_id = ? AND frame_type = ?", au.ID, typeAddUser).Delete(&deliveryRecord{}).Error
}

func (au *addUser) getID() ID {
	return au.ID
}

func (au *addUser) getScope(_ ID) int {
	return scopeUser
}

func (au *addUser) getDestination(myID ID) ID {
	return xor(myID, au.Xor)
}

func (au *addUser) getType() uint16 {
	return typeAddUser
}

func (au *addUser) getPayload() []byte {
	bytes, err := msgpack.Marshal(au)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal add user")
	}

	return bytes
}

func (au *addUser) getAuthor() ID {
	// Since add users will never be broadcast globally, we don't
	// need to accurately report the author
	return NilID
}

func (au *addUser) getTimestamp() int64 {
	return au.Timestamp
}

func (au *addUser) getSavedAt() int64 {
	return au.SavedAt
}

func (b *Bounce) handleAddUser(peer string, payload []byte, _ bool) (broadcastable, bool) {
	if len(payload) > maximumAddUserFramePayload {
		log.WithFields(log.Fields{
			"peer": peer,
			"size": len(payload),
		}).Warn("ignoring add user larger than maximum allowed payload size")
		return nil, false
	}

	// An add user we built ourselves, on accepting a request, comes through here under our own address so that it is
	// saved and applied the same way; it has no peer to ack, record delivery to or offer references to
	fromPeer := peer != b.network.Address()
	handleAddUsersMutex.Lock()
	defer handleAddUsersMutex.Unlock()

	// Unmarshal the structure
	var au addUser
	err := msgpack.Unmarshal(payload, &au)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling add user")
		return nil, false
	}

	if len(au.OfferUser) > maximumAddUserRecordSize || len(au.RequesterUser) > maximumAddUserRecordSize {
		log.WithFields(log.Fields{
			"peer":                peer,
			"offer_user_size":     len(au.OfferUser),
			"requester_user_size": len(au.RequesterUser),
		}).Warn("rejecting add user with oversized user record")
		return nil, false
	}

	// Make sure the ID is generated from the content
	if !idMatchesContent(&au) {
		log.WithFields(log.Fields{
			"peer":       peer,
			"id":         au.getID(),
			"content_id": au.generateID(),
		}).Error("rejecting add user with an ID not generated from its content")
		return nil, false
	}

	// Ignore add users where any device involved is revoked
	if b.devicePool.isRevoked(peer) {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("ignoring add user from revoked peer")
		return nil, false
	}
	if b.devicePool.isRevoked(au.OfferDevice) {
		log.WithFields(log.Fields{
			"peer":         peer,
			"offer_device": au.OfferDevice,
		}).Warn("ignoring add user with revoked offer device")
		return nil, false
	}
	if b.devicePool.isRevoked(au.RequesterDevice) {
		log.WithFields(log.Fields{
			"peer":             peer,
			"requester_device": au.RequesterDevice,
		}).Warn("ignoring add user with revoked requester device")
		return nil, false
	}

	// If we already know about this add user, just ack it and mark as delivered
	var existingAU addUser
	err = b.database.Where("id = ?", au.ID).First(&existingAU).Error
	if err == nil {
		return &existingAU, false
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up add user")
	}

	// Ensure that both signatures cover the full mutual addUser digest
	signatureDigest := addUserSignatureDigest(au.Nonce, au.Timestamp, au.OfferUser, au.RequesterUser, au.OfferDevice, au.RequesterDevice)
	ok := b.network.VerifySignature(au.RequesterDevice, signatureDigest, au.RequesterSignature)
	if !ok {
		log.Warn("add user has invalid requester signature")
		return nil, false
	}
	ok = b.network.VerifySignature(au.OfferDevice, signatureDigest, au.OfferSignature)
	if !ok {
		log.Warn("add user has invalid offer signature")
		return nil, false
	}

	// Unmarshal the users inside the structure
	var offerUser user
	err = msgpack.Unmarshal(au.OfferUser, &offerUser)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling offer user inside of add user")
		return nil, false
	}
	var requesterUser user
	err = msgpack.Unmarshal(au.RequesterUser, &requesterUser)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling requester user inside of add user")
		return nil, false
	}

	// Make sure both users contain valid data
	for _, u := range []user{offerUser, requesterUser} {
		if err := validUserRecord(u); err != nil {
			log.WithFields(log.Fields{
				"peer":    peer,
				"user_id": u.ID,
				"error":   err.Error(),
			}).Warn("rejecting add user containing invalid user record")
			return nil, false
		}
	}

	// Make sure the xor matches the users
	if au.Xor != xor(offerUser.ID, requesterUser.ID) {
		log.WithFields(log.Fields{
			"xor":               au.Xor,
			"actual_xor":        xor(offerUser.ID, requesterUser.ID),
			"offer_user_id":     offerUser.ID,
			"requester_user_id": requesterUser.ID,
		}).Warn("rejecting add user with invalid xor")
		return nil, false
	}

	// Ensure that the devices that did the signing are part of the users in the structure
	offerDeviceFound := false
	for _, dev := range offerUser.Devices {
		if offerDeviceFound {
			continue
		}
		if dev.Address == au.OfferDevice {
			offerDeviceFound = true
		}
	}
	if !offerDeviceFound {
		log.Warn("add user offer signature not made by a device owned by the offer user")
		return nil, false
	}
	requesterDeviceFound := false
	for _, dev := range requesterUser.Devices {
		if requesterDeviceFound {
			continue
		}
		if dev.Address == au.RequesterDevice {
			requesterDeviceFound = true
		}
	}
	if !requesterDeviceFound {
		log.Warn("add user requester signature not made by a device owned by the requester user")
		return nil, false
	}

	// Figure out which user is not us
	var counterparty user
	var myUser user
	var counterpartySigningDeviceAddress string
	if offerUser.ID == b.currentUserID() && requesterUser.ID != b.currentUserID() {
		counterparty = requesterUser
		myUser = offerUser
		counterpartySigningDeviceAddress = au.RequesterDevice
	} else if requesterUser.ID == b.currentUserID() && offerUser.ID != b.currentUserID() {
		counterparty = offerUser
		myUser = requesterUser
		counterpartySigningDeviceAddress = au.OfferDevice
	} else {
		log.WithFields(log.Fields{
			"offer_user":     offerUser.ID,
			"requester_user": requesterUser.ID,
		}).Warn("add user does not contain us and someone else")
		return nil, false
	}

	// Make sure that this new user has a valid device group
	if !b.hasValidDeviceGroup(counterparty) {
		log.Warn("rejecting add user with invalid device group")
		return nil, false
	}
	for _, dev := range counterparty.Devices {
		if dev.UserID != counterparty.ID {
			log.WithFields(log.Fields{
				"device_id":       dev.ID,
				"counterparty_id": counterparty.ID,
				"device_user":     dev.UserID,
			}).Warn("rejecting add user with counterparty device that does not belong to counterparty")
			return nil, false
		}
	}

	// Make sure that our user has a valid device group
	if !b.hasValidDeviceGroup(myUser) {
		log.Warn("rejecting add user with invalid device group")
		return nil, false
	}
	for _, dev := range myUser.Devices {
		if dev.UserID != myUser.ID {
			log.WithFields(log.Fields{
				"device_id":   dev.ID,
				"profile_id":  myUser.ID,
				"device_user": dev.UserID,
			}).Warn("rejecting add user with sync device that does not belong to profile")
			return nil, false
		}
	}

	// Validate every sync device before checking peer authorization or persisting anything
	syncDevices := devices(myUser.Devices)
	sort.Sort(syncDevices)
	validDevices := []device{}
	for i := range syncDevices {
		dev := syncDevices[i]
		existingSyncDevice, exists := b.getDeviceFromAddress(dev.Address)
		if exists {
			if existingSyncDevice.UserID != myUser.ID {
				log.WithFields(log.Fields{
					"address":       dev.Address,
					"existing_user": existingSyncDevice.UserID,
					"claimed_user":  myUser.ID,
				}).Warn("rejecting add user claiming a device that belongs to another user as our sync device")
				return nil, false
			}
		} else {
			if b.isValidAddition(myUser, dev) {
				validDevices = append(validDevices, dev)
			} else {
				log.WithFields(log.Fields{
					"id":      dev.ID,
					"address": dev.Address,
				}).Warn("rejecting add user with new sync device that is not a valid addition to our device group")
				return nil, false
			}
		}
	}

	// Only our own non-revoked devices or a device belonging to the counterparty (known or listed in the
	// mutually signed record) may deliver this addUser.
	fromOwnSyncDevice := false
	if fromPeer {
		peerAllowed := false
		if peerDev, ok := b.getDeviceFromAddress(peer); ok {
			if peerDev.UserID == b.currentUserID() {
				peerAllowed = true
				fromOwnSyncDevice = true
			} else if peerDev.UserID == counterparty.ID {
				peerAllowed = true
			}
		}
		if !peerAllowed {
			for _, dev := range counterparty.Devices {
				if dev.Address == peer {
					peerAllowed = true
					break
				}
			}
		}
		if !peerAllowed {
			for _, dev := range myUser.Devices {
				if dev.Address == peer {
					peerAllowed = true
					fromOwnSyncDevice = true
					break
				}
			}
		}
		if !peerAllowed {
			log.WithFields(log.Fields{
				"peer":            peer,
				"counterparty_id": counterparty.ID,
			}).Warn("rejecting add user delivered by unauthorized peer")
			return nil, false
		}

		// Drop addUsers for blocked counterparties unless delivered by our own sync device (needed during
		// initial sync of a new device so the historical user row exists before the block updateDM applies).
		if !fromOwnSyncDevice && blockedUser(counterparty.ID) {
			log.WithFields(log.Fields{
				"id":              au.ID,
				"peer":            peer,
				"counterparty_id": counterparty.ID,
			}).Warn("ignoring add user for blocked counterparty")
			return nil, false
		}
	}

	// Make sure that none of the counterparty devices already belong to another user before writing anything
	for _, dev := range counterparty.Devices {
		// Addresses cannot collide
		if existingDevice, exists := b.getDeviceFromAddress(dev.Address); exists {
			if existingDevice.UserID != counterparty.ID {
				log.WithFields(log.Fields{
					"address":       dev.Address,
					"new_user":      counterparty.ID,
					"existing_user": existingDevice.UserID,
				}).Warn("rejecting add user with device address collision with separate existing user")
				return nil, false
			}
		}
		// Primary keys cannot collide
		var existingDevice device
		err = b.database.Where("id = ?", dev.ID).First(&existingDevice).Error
		if err == nil {
			if existingDevice.UserID != counterparty.ID {
				log.WithFields(log.Fields{
					"id":            dev.ID,
					"new_user":      counterparty.ID,
					"existing_user": existingDevice.UserID,
				}).Warn("rejecting add user with device ID collision with separate existing user")
				return nil, false
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up device")
		}
	}

	for _, dev := range validDevices {
		err = b.database.Create(&dev).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error saving sync device learned about via add user")
		}
	}
	b.applyCarriedDeviceRevocations(peer, myUser)
	for _, dev := range validDevices {
		var stored device
		err = b.database.Select("revoked_at").Where("id = ?", dev.ID).First(&stored).Error
		if err != nil || stored.RevokedAt != 0 {
			continue
		}
		b.ui.DeviceAdded(Device{
			ID:        dev.ID,
			Name:      dev.Name,
			Address:   dev.Address,
			CreatedAt: dev.Timestamp,
			Local:     false,
			Online:    b.getRemoteDevice(dev.Address).connectedSockets() > 0,
		})
	}

	// Check if we are already aware of this user
	userIsNew := true
	var existingCounterparty user
	err = b.database.Where("id = ?", counterparty.ID).First(&existingCounterparty).Error
	if err == nil {
		userIsNew = false
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up user")
	}

	// A new user's devices are saved only if we persist the user; a known user's valid devices are safe to keep
	if userIsNew {
		counterparty.Devices = b.admissibleCarriedDevices(counterparty.Devices)
	} else {
		counterparty.Devices = b.saveCarriedDevices(counterparty.Devices)
	}

	// A device rejected by those admission checks cannot sign for the counterparty
	counterpartySignerFound := false
	for _, dev := range counterparty.Devices {
		if dev.Address == counterpartySigningDeviceAddress {
			counterpartySignerFound = true
			break
		}
	}
	if !counterpartySignerFound {
		log.WithFields(log.Fields{
			"address": counterpartySigningDeviceAddress,
		}).Warn("rejecting add user whose counterparty signer is not an admitted device")
		return nil, false
	}

	// If we already have the counterparty and an add_users row for this pair, avoid storing duplicate rows.
	// Any new valid devices/revocations above have already been applied; ack this frame so the peer stops
	// re-offering it.
	if !userIsNew {
		var existingForPair addUser
		err = b.database.Select("id").Where("xor = ?", au.Xor).First(&existingForPair).Error
		if err == nil {
			b.applyCarriedDeviceRevocations(peer, counterparty)
			for _, dev := range counterparty.Devices {
				if fromPeer {
					go b.sendAck(peer, typeDevice, dev.ID)
					b.markDeliveredTo(&dev, peer)
				}
			}
			if fromPeer {
				go b.sendAck(peer, typeAddUser, au.ID)
			}
			return nil, false
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up existing add user for pair")
		}
	}

	// Save the addUser
	err = b.database.Create(&au).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving add user")
	}

	// Save the new user without letting GORM bypass the device admission checks
	if userIsNew {
		counterparty.OpenDM = true
		counterparty.IntroductionMethod = userIntroductionAddUser
		counterparty.IntroductionTime = time.Now().Unix()
		counterparty.Accepted = true
		counterparty.Blocked = blockedUser(counterparty.ID)
		err = b.database.Omit("Devices").Create(&counterparty).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error saving new user while handling add user")
			return nil, false
		}
		counterparty.Devices = b.saveCarriedDevices(counterparty.Devices)
		for _, imageID := range counterparty.images() {
			b.activateReferencedFile(imageID)
		}
	}
	b.applyCarriedDeviceRevocations(peer, counterparty)

	// Ack and delivery track only devices we accepted
	for _, dev := range counterparty.Devices {
		if fromPeer {
			go b.sendAck(peer, typeDevice, dev.ID)
			b.markDeliveredTo(&dev, peer)
		}
	}

	// Do a reference flow with this peer
	if fromPeer {
		go b.sendReferences(peer)
	}

	// Connect to this new user if needed
	go b.auditPeers()

	// Inform the UI that a new friend has been added
	if userIsNew {
		b.ui.UserAdded(counterparty.exportedUser())
		b.updateDMState(counterparty.ID)
	}

	return &au, true
}
