package chat

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"github.com/zeebo/blake3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var handleAddUsersMutex sync.Mutex

// An addUser frame contains proof that two users became friends, where one of the users is us.  It is created as part
// of the final step in adding a user over the wire.
type addUser struct {
	cachedEncoding
	ID                 uuid.UUID `gorm:"type:uuid;primary_key;"`
	Xor                uuid.UUID
	Timestamp          int64
	OfferUser          []byte
	RequesterUser      []byte
	OfferDevice        string
	RequesterDevice    string
	OfferSignature     []byte
	RequesterSignature []byte
}

func (au *addUser) BeforeCreate(tx *gorm.DB) error {
	if au.ID == uuid.Nil {
		log.Fatal("add user must have an ID assigned before save")
	}
	return nil
}

func (au *addUser) AfterDelete(tx *gorm.DB) error {
	if au.ID == uuid.Nil {
		return nil
	}
	return tx.Clauses(clause.Returning{}).Where("frame_id = ? AND frame_type = ?", au.ID, typeAddUser).Delete(&deliveryRecord{}).Error
}

func (au *addUser) getID() uuid.UUID {
	return au.ID
}

func (au *addUser) getScope(_ uuid.UUID) int {
	return scopeUser
}

func (au *addUser) getDestination(myID uuid.UUID) uuid.UUID {
	return xor(myID, au.Xor)
}

func (au *addUser) getType() uint16 {
	return typeAddUser
}

func (au *addUser) getPayload() []byte {
	au.payloadMutex.Lock()
	defer au.payloadMutex.Unlock()

	if len(au.payload) == 0 {
		bytes, err := msgpack.Marshal(au)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal add user")
		}
		au.payload = bytes
	}
	return au.payload
}

func (au *addUser) getAuthor() uuid.UUID {
	// Since add users will never be broadcast globally, we don't
	// need to accurately report the author
	return uuid.Nil
}

func (au *addUser) getTimestamp() int64 {
	return au.Timestamp
}

func (b *Bounce) handleAddUser(peer string, payload []byte, _ bool) (broadcastable, bool) {
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

	// Ensure that all the signatures are correct
	offerUserHash := blake3.Sum256(au.OfferUser)
	ok := b.network.VerifySignature(au.RequesterDevice, offerUserHash[:], au.RequesterSignature)
	if !ok {
		log.Warn("add user has invalid requester signature")
		return nil, false

	}
	requesterUserHash := blake3.Sum256(au.RequesterUser)
	ok = b.network.VerifySignature(au.OfferDevice, requesterUserHash[:], au.OfferSignature)
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
	if offerUser.ID == b.currentUserID() && requesterUser.ID != b.currentUserID() {
		counterparty = requesterUser
		myUser = offerUser
	} else if requesterUser.ID == b.currentUserID() && offerUser.ID != b.currentUserID() {
		counterparty = offerUser
		myUser = requesterUser
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

	// Learn about any of our sync devices that we're not already aware of
	syncDevices := devices(myUser.Devices)
	sort.Sort(syncDevices)
	for _, dev := range syncDevices {
		_, exists := b.getDeviceFromAddress(dev.Address)
		if !exists {
			if b.isValidAddition(myUser, dev) {
				err = b.database.Create(&dev).Error
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Fatal("error saving sync device learned about via add user")
				}
			} else {
				log.WithFields(log.Fields{
					"id":      dev.ID,
					"address": dev.Address,
				}).Warn("rejecting add user with new sync device that is not a valid addition to our device group")
			}
		}
	}

	// Make sure the device that is sending us this makes sense
	peerDevice, exists := b.getDeviceFromAddress(peer)
	if exists && !(b.isSyncDevice(peerDevice) || peerDevice.UserID == counterparty.ID) {
		log.WithFields(log.Fields{
			"peer":            peer,
			"counterparty_id": counterparty.ID,
		}).Warn("add user came from unexpected device, ignoring")
		return nil, false
	}

	// Make sure that none of the devices don't already belong to another user
	for _, dev := range counterparty.Devices {
		// Addresses cannot collide
		if existingDevice, exists := b.getDeviceFromAddress(dev.Address); exists {
			if existingDevice.UserID != counterparty.ID {
				log.WithFields(log.Fields{
					"address":        dev.Address,
					"new_user":       counterparty.ID,
					"exisiting_user": existingDevice.UserID,
				}).Warn("rejecting add user with device address collision with separate existing user")
				return nil, false
			}
		}
		// Primary keys cannot collise
		var existingDevice device
		err = b.database.Where("id = ?", dev.ID).First(&existingDevice).Error
		if err == nil {
			if existingDevice.UserID != counterparty.ID {
				log.WithFields(log.Fields{
					"id":             dev.ID,
					"new_user":       counterparty.ID,
					"exisiting_user": existingDevice.UserID,
				}).Warn("rejecting add user with device ID collision with separate existing user")
				return nil, false
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up device")
		}
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

	// Save the addUser
	err = b.database.Create(&au).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving add user")
	}

	// Save the new user
	if userIsNew {
		// Save this new user and all of their devices
		counterparty.OpenDM = true
		counterparty.IntroductionMethod = userIntroductionAddUser
		counterparty.IntroductionTime = time.Now().Unix()
		kek, err := b.generateKEK(counterparty.PublicECDHKey)
		if err != nil {
			log.WithFields(log.Fields{
				"user_id": counterparty.ID,
				"error":   err.Error(),
			}).Error("refusing to save user in add user, error generating key encryption key")
			return nil, false
		}
		counterparty.KeyEncryptionKey = kek
		err = b.database.Create(&counterparty).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error saving new user while handing add user")
			return nil, false
		}

		// Ack and delivery track these devices
		for _, dev := range counterparty.Devices {
			go b.sendAck(peer, typeDevice, dev.ID)
			b.markDeliveredTo(&dev, peer)
		}
	} else {
		// We already know about this user, just save any devices we might not be aware of
		for _, dev := range counterparty.Devices {
			err = b.database.Clauses(clause.OnConflict{DoNothing: true}).Create(&dev).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error saving new device belonging to existing user while handling add user")
			}

			// Ack and delivery track these devices as well
			go b.sendAck(peer, typeDevice, dev.ID)
			b.markDeliveredTo(&dev, peer)
		}
	}

	// Do a reference flow with this peer
	go b.sendReferences(peer)

	// Connect to this new user if needed
	go b.auditPeers()

	// Inform the UI that a new friend has been added
	if userIsNew {
		b.ui.UserAdded(User{
			ID:               counterparty.ID,
			Name:             counterparty.Name,
			Images:           counterparty.images(),
			IntroductionTime: counterparty.IntroductionTime,
		})
		b.updateDMState(counterparty.ID)
	}

	return &au, true
}
