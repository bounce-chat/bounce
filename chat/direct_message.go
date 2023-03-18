package chat

import (
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var directMessageMutex sync.Mutex

type DirectMessage struct {
	ID               uuid.UUID `gorm:"type:uuid;primary_key;"`
	SavedAt          int64     `msgpack:"-"`
	WrittenAt        int64
	RetentionSeconds int64 // Number of seconds to retain this message, captures the retention setting from the author's perspective at the time the message was written
	DeleteAt         int64 `msgpack:"-"` // Absolute time at which the messages expires.  Time it was first acked/received + RetentionSeconds
	Read             bool  `msgpack:"-"`
	Undeliverable    bool  `msgpack:"-"` // The message was never delivered to another device and is beyond when we give up including it in reference offers
	Source           uuid.UUID
	Destination      uuid.UUID
	Text             string // TODO: other things that can be in a message, like a reference to an image, audio, video, or file attachment
	payload          []byte
	payloadMutex     sync.Mutex
}

func (dm *DirectMessage) BeforeCreate(tx *gorm.DB) error {
	if dm.ID == uuid.Nil {
		log.Fatal("direct message must have an ID assigned before save")
	}
	dm.SavedAt = time.Now().Unix()
	return nil
}

func (dm *DirectMessage) AfterDelete(tx *gorm.DB) error {
	return tx.Where("frame_id = ? AND frame_type = ?", dm.ID, typeDirectMessage).Delete(&deliveryRecord{}).Error
}

func (dm *DirectMessage) getID() uuid.UUID {
	return dm.ID
}

func (dm *DirectMessage) getScope(_ uuid.UUID) int {
	if dm.Source == dm.Destination {
		// A DM to ourselves, only needs to be sent to sync devices
		return scopeSync
	}
	return scopeUser
}

func (dm *DirectMessage) getDestination(myID uuid.UUID) uuid.UUID {
	if dm.Source == dm.Destination {
		// A DM to ourselves, only needs to be sent to sync devices,
		// which doesn't invovle needing to know a destinatinon to
		// determine scope
		return uuid.Nil
	}

	otherParty := dm.Source // TODO: just triple xor?
	if dm.Source == myID {
		otherParty = dm.Destination
	}
	return otherParty
}

func (dm *DirectMessage) getType() uint16 {
	return typeDirectMessage
}

func (dm *DirectMessage) getPayload() []byte {
	dm.payloadMutex.Lock()
	defer dm.payloadMutex.Unlock()

	if len(dm.payload) == 0 {
		bytes, err := msgpack.Marshal(dm)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal direct message")
		}
		dm.payload = bytes
	}
	return dm.payload
}

func (dm *DirectMessage) getTimestamp() int64 {
	return dm.SavedAt
}

//
// UI Handlers
//

func (b *bounce) sendDirectMessage(message *DirectMessage) uuid.UUID {
	message.ID = uuid.New()
	message.WrittenAt = time.Now().Unix()
	message.Read = true
	message.Source = b.currentUserID()
	message.RetentionSeconds = b.getDMRetention(message.Destination)

	err := b.database.Create(message).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving direct message to the database")
	}

	b.broadcast(message)

	return message.ID
}

//
// Network Handlers
//

func (b *bounce) handleDirectMessage(peer string, payload []byte) {
	directMessageMutex.Lock()
	defer directMessageMutex.Unlock()

	// Unmarshal the payload
	var dm DirectMessage
	err := msgpack.Unmarshal(payload, &dm)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling direct message")
		return
	}

	// Look up the device that sent it
	srcDevice, exists := b.getDeviceFromAddress(peer)
	if !exists {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("ignoring a direct message sent from an unknown device")
		return
	}

	// Make sure that the peer we received this DM from makes sense, it must either be from a device belonging to the
	// other user or one of our devices
	if !b.dmOriginAcceptable(dm, srcDevice) {
		log.WithFields(log.Fields{
			"message_id":  dm.ID,
			"source":      dm.Source,
			"destination": dm.Destination,
			"peer":        peer,
		}).Warn("ignoring a direct message from an unacceptable peer")
		return
	}

	// If we have already seen this message, all we need to do is mark that this peer has the message as well.  If not, we save the message
	// in the database.  This step is synchroniszed with a mutex lock since the same message can come in concurrently during gossip.
	var existingDM DirectMessage
	err = b.database.Where("id = ?", dm.ID).First(&existingDM).Error
	if err == nil {
		b.markDeliveredTo(&existingDM, peer)
		// TODO: forgotten ack?
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up direct message")
	}

	// Capture the current message retention setting for this user and store it on the DM
	id := dm.Destination
	if id == b.currentUserID() {
		id = dm.Source
	}
	if dm.RetentionSeconds != 0 {
		dm.DeleteAt = time.Now().Unix() + dm.RetentionSeconds
	}

	// Save the new message
	err = b.database.Create(&dm).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving incoming direct message")
	}
	// Save a delivery report for the peer that send this message
	b.markDeliveredTo(&dm, peer)

	// Send the message to the user interface
	b.userInterface.ReceivedDirectMessage(dm)

	// Make sure the user interface isn't still displaying that the user is typing
	b.clearUserTypingIndicator(dm.Source, dm.getDestination(b.currentUserID()))

	// Send an ack to the sender that we got it
	go b.sendAck(peer, typeDirectMessage, dm.ID)

	// Gossip the message to any online devices that should have it
	go b.broadcast(&dm) // TODO: only send references if the devices in the pool are less that max connections per pool?
}

func (b *bounce) dmOriginAcceptable(dm DirectMessage, dev device) bool {
	// If this is a message to ourselves, then the peer must be a device we own
	if dm.Source == dm.Destination {
		if dm.Source == b.currentUserID() {
			if dev.UserID == b.currentUserID() {
				return true
			} else {
				log.WithFields(log.Fields{
					"peer": dev.Address,
				}).Warn("got self direct message from a device that is not a sync device")
				return false
			}
		} else {
			// This is a message from a user to themselves, but that user isn't us
			log.WithFields(log.Fields{
				"peer":        dev.Address,
				"source":      dm.Source,
				"destination": dm.Destination,
			}).Warn("received self direct message not intended for us")
			return false
		}
	} else {
		// Make sure that at least one of the user IDs is us
		if !(dm.Source == b.currentUserID() || dm.Destination == b.currentUserID()) {
			log.WithFields(log.Fields{
				"peer":        dev.Address,
				"source":      dm.Source,
				"destination": dm.Destination,
			}).Warn("received direct message not intended for us")
			return false
		}

		// Figure out which user ID is not us
		otherParty := dm.Source // TODO: just do: source ^ destination ^ currentUserID?
		if dm.Source == b.currentUserID() {
			otherParty = dm.Destination
		}

		// Make sure that user actually exists
		var otherUser user
		err := b.database.Preload(clause.Associations).First(&otherUser, "id = ?", otherParty).Error // TODO: cache?  otherwise each DM is another database read
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"user_id": otherParty,
				}).Error("user not found while validating direct message peer address")
				return false
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error looking up user")
			}
		}

		// Reguardless of who the other party is, if the message came from one of our devices it's allowed
		currentUser, ok := b.currentUser()
		if !ok {
			// This doesn't really make sense, but if we're getting DMs that are so far valid
			// while also not having a profile, we shouldn't allow this
			log.Error("could not find current user while attempting to validate direct message peer")
			return false
		}
		isFromSyncDevice := false
		for _, syncDevice := range currentUser.Devices {
			if syncDevice.Address == dev.Address {
				isFromSyncDevice = true
			}
		}
		if isFromSyncDevice {
			return true
		}

		// If the message didn't come from one of our devices, it must come from one of theirs
		for _, userDevice := range otherUser.Devices {
			if userDevice.Address == dev.Address {
				// Early return as soon as we discover the peer's device with the address
				// that sent this message
				return true
			}
		}

		// The device that sent this otherwise valid DM was not owned by the indicated counterparty
		log.WithFields(log.Fields{
			"message_id":  dm.ID,
			"source":      dm.Source,
			"destination": dm.Destination,
			"peer":        dev.Address,
		}).Warn("received direct message from a device not in the allowed device set")
		return false
	}
}
