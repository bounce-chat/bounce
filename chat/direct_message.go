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

var dmDeliveryNotificationMutex sync.Mutex
var dmDeliveryNotifications = map[uuid.UUID]chan bool{}

//
// A direct message is a chat message from one user to another
//
type directMessage struct {
	ID            uuid.UUID `gorm:"type:uuid;primary_key;"`
	SavedAt       int64     `msgpack:"-"`
	WrittenAt     int64
	DeleteAt      int64 `msgpack:"-"`
	Seen          bool  `msgpack:"-"`
	Undeliverable bool  `msgpack:"-"` // The message was never delivered to another device and is beyond when we give up including it in reference offers
	Author        uuid.UUID
	Xor           uuid.UUID // XOR of the two users in the DM
	Text          string
	payload       []byte
	payloadMutex  sync.Mutex
}

func (dm *directMessage) BeforeCreate(tx *gorm.DB) error {
	if dm.ID == uuid.Nil {
		return errors.New("direct message must have an ID assigned before creation")
	}
	dm.SavedAt = time.Now().Unix()
	return nil
}

func (dm *directMessage) AfterDelete(tx *gorm.DB) error {
	err := tx.Where("frame_id = ? AND frame_type = ?", dm.ID, typeDirectMessage).Delete(&deliveryRecord{}).Error
	if err != nil {
		return err
	}
	return tx.Where("target = ? AND target_type = ?", dm.ID, typeDirectMessage).Delete(&readReceipt{}).Error
}

func (dm *directMessage) getID() uuid.UUID {
	return dm.ID
}

func (dm *directMessage) getScope(_ uuid.UUID) int {
	if dm.Xor == uuid.Nil {
		// A DM to ourselves, only needs to be sent to sync devices
		return scopeSync
	}
	return scopeUser
}

func (dm *directMessage) getDestination(myID uuid.UUID) uuid.UUID {
	return xor(dm.Xor, myID)
}

func (dm *directMessage) getType() uint16 {
	return typeDirectMessage
}

func (dm *directMessage) getPayload() []byte {
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

func (dm *directMessage) getAuthor() uuid.UUID {
	return dm.Author
}

func (dm *directMessage) getTimestamp() int64 {
	return dm.WrittenAt
}

func (b *bounce) handleDirectMessage(peer string, payload []byte, catchUp bool) broadcastable {
	directMessageMutex.Lock()
	defer directMessageMutex.Unlock()

	// Unmarshal the payload
	var dm directMessage
	err := msgpack.Unmarshal(payload, &dm)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling direct message")
		return nil
	}

	// Look up the device that sent it
	srcDevice, exists := b.getDeviceFromAddress(peer)
	if !exists {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("ignoring a direct message sent from an unknown device")
		return nil
	}

	// Make sure that the peer we received this DM from makes sense, it must either be from a device belonging to the
	// other user or one of our devices
	if !b.dmOriginAcceptable(dm, srcDevice) {
		log.WithFields(log.Fields{
			"message_id":  dm.ID,
			"author":      dm.Author,
			"destination": dm.getDestination(b.currentUserID()),
			"peer":        peer,
		}).Warn("ignoring a direct message from an unacceptable peer")
		return nil
	}

	// If we have already seen this message, all we need to do is mark that this peer has the message as well and ack it
	var existingDM directMessage
	err = b.database.Where("id = ?", dm.ID).First(&existingDM).Error
	if err == nil {
		return &existingDM
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up direct message")
	}

	// If the message is older than the user's ClearBefore, don't process it
	if b.directMessageWrittenBeforeHistoryCleared(dm.getDestination(b.currentUserID()), dm.WrittenAt) {
		log.WithFields(log.Fields{
			"author":      dm.Author,
			"destination": dm.getDestination(b.currentUserID()),
			"written_at":  dm.WrittenAt,
		}).Debug("ignoring a direct message that was written before the history was cleared")
		return nil
	}

	// Save the new message
	err = b.database.Create(&dm).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving incoming direct message")
	}

	// Update the activity timestamp on the user model
	b.updateLastUserActivity(dm.getDestination(b.currentUserID()), dm.SavedAt)

	// Send the message to the user interface
	b.userInterface.DisplayDirectMessage(DirectMessage{
		ID:        dm.ID,
		Author:    dm.Author,
		Thread:    dm.getDestination(b.currentUserID()),
		WrittenAt: dm.WrittenAt,
		SavedAt:   dm.SavedAt,
		Text:      dm.Text,
	})

	// Make sure the user interface isn't still displaying that the user is typing
	b.clearUserTypingIndicator(dm.Author, dm.getDestination(b.currentUserID()))

	return &dm
}

func (b *bounce) dmOriginAcceptable(dm directMessage, dev device) bool {
	// If this is a message to ourselves, then the peer must be a device we own
	if dm.Xor == uuid.Nil {
		if dm.Author == b.currentUserID() {
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
				"author":      dm.Author,
				"destination": dm.getDestination(b.currentUserID()),
			}).Warn("received self direct message not intended for us")
			return false
		}
	} else {
		// Make sure that user actually exists
		var otherUser user
		err := b.database.Preload(clause.Associations).First(&otherUser, "id = ?", dm.getDestination(b.currentUserID())).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"user_id": dm.getDestination(b.currentUserID()),
				}).Error("user not found while validating direct message peer address")
				return false
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error looking up user")
			}
		}

		// If the message came from one of our devices it's allowed
		if b.isSyncDevice(dev) {
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
			"author":      dm.Author,
			"destination": dm.getDestination(b.currentUserID()),
			"peer":        dev.Address,
		}).Warn("received direct message from a device not in the allowed device set")
		return false
	}
}

func (b *bounce) sendDirectMessage(message DirectMessage) {
	if message.ID != uuid.Nil {
		log.Fatal("direct message ID cannot be set by the UI")
	}

	now := time.Now()
	deleteAt := int64(0)
	retentionSeconds := b.getGroupRetention(message.Thread)
	if retentionSeconds > 0 {
		deleteAt = now.Unix() + retentionSeconds
	}
	dm := &directMessage{
		ID:        uuid.New(),
		WrittenAt: now.Unix(),
		Seen:      true,
		Author:    b.currentUserID(),
		Xor:       xor(b.currentUserID(), message.Thread),
		DeleteAt:  deleteAt,
		Text:      message.Text,
	}
	message.ID = dm.ID
	message.Author = b.currentUserID()
	message.WrittenAt = now.Unix()

	err := b.database.Create(dm).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving direct message to the database")
	}
	b.updateLastUserActivity(message.Thread, dm.SavedAt)

	go b.checkIfDirectMessageUndeliverableAt(now.Add(undeliverableAfter).Unix(), dm.ID)

	b.userInterface.DisplayDirectMessage(DirectMessage{
		ID:            dm.ID,
		Author:        dm.Author,
		Thread:        dm.getDestination(b.currentUserID()),
		WrittenAt:     dm.WrittenAt,
		SavedAt:       dm.SavedAt,
		Text:          dm.Text,
		Expires:       dm.DeleteAt,
		Seen:          dm.Seen,
		Undeliverable: dm.Undeliverable,
	})

	b.broadcast(dm)
}

func (b *bounce) deleteDirectMessageAt(timestamp int64, id uuid.UUID) {
	// Sleep as long as needed
	duration := timestamp - time.Now().Unix()
	if duration > 0 {
		time.Sleep(time.Duration(duration) * time.Second)
	}

	// Delete from the database
	err := b.database.Where("id = ?", id).Delete(&directMessage{}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"message_id": id,
			"error":      err.Error(),
		}).Fatal("error deleting direct message that expired")
	}

	// Delete from the UI
	b.userInterface.DeleteItem(id)
}

func (b *bounce) checkIfDirectMessageUndeliverableAt(timestamp int64, id uuid.UUID) {
	// Create a receiver for delivery notifications
	delivered := make(chan bool)
	dmDeliveryNotificationMutex.Lock()
	dmDeliveryNotifications[id] = delivered
	dmDeliveryNotificationMutex.Unlock()

	// Create a timer that waits until the timestamp
	duration := time.Duration(timestamp-time.Now().Unix()) * time.Second
	sleeper := time.NewTimer(duration)
	defer sleeper.Stop()

	// If this message gets ack'd then just return here, otherwise wait until the timestamp
	select {
	case <-delivered:
		dmDeliveryNotificationMutex.Lock()
		delete(dmDeliveryNotifications, id)
		dmDeliveryNotificationMutex.Unlock()
		return
	case <-sleeper.C:
		dmDeliveryNotificationMutex.Lock()
		delete(dmDeliveryNotifications, id)
		dmDeliveryNotificationMutex.Unlock()
		break
	}

	// Check if the message still hasn't been delivered
	var count int64
	err := b.database.Model(&deliveryRecord{}).Where("frame_id = ? AND frame_type = ?", id, typeDirectMessage).Count(&count).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error counting delivery records for direct message")
	}

	// If it hasn't been delivered, mark it as undeliverable
	if count == 0 {
		// Find the DM
		var dm directMessage
		err = b.database.Where("id = ?", id).First(&dm).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"message_id": id,
				}).Warn("no direct message found when checking for delivery")
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("error looking up direct message")
			}
		}

		// This message is undeliverable, update the undeliverable field and inform the UI
		err = b.database.Model(&dm).Update("undeliverable", true).Error
		if err != nil {
			log.WithFields(log.Fields{
				"message_id": dm.ID,
				"error":      err.Error(),
			}).Fatal("error updating undeliverable field of undeliverable direct message")
		}
		b.userInterface.MarkMessageUndeliverable(dm.ID)
	}
}
