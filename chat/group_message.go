package chat

import (
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
)

var groupMessageMutex sync.Mutex

var gmDeliveryNotificationMutex sync.Mutex
var gmDeliveryNotifications = map[uuid.UUID]chan bool{}

//
// A group message is sent from a member of a group to a group
//
type groupMessage struct {
	ID               uuid.UUID `gorm:"type:uuid;primary_key;"`
	SavedAt          int64     `msgpack:"-"`
	WrittenAt        int64
	RetentionSeconds int64 // Number of seconds to retain this message, captures the retention setting from the author's perspective at the time the message was written
	DeleteAt         int64 `msgpack:"-"` // Absolute time at which the messages expires.  Time it was first acked/received + RetentionSeconds
	Read             bool  `msgpack:"-"`
	Undeliverable    bool  `msgpack:"-"` // The message was never delivered to another device and is beyond when we give up including it in reference offers
	Author           uuid.UUID
	Destination      uuid.UUID
	Text             string
	Signer           string `msgpack:"-" gorm:"not null"`
	OriginalPayload  []byte `msgpack:"-" gorm:"not null"`
	Signature        []byte `msgpack:"-" gorm:"not null"`
	payload          []byte
	payloadMutex     sync.Mutex
}

func (gm *groupMessage) BeforeCreate(tx *gorm.DB) error {
	if gm.ID == uuid.Nil {
		return errors.New("group message must have an ID assigned before creation")
	}
	gm.SavedAt = time.Now().Unix()
	return nil
}

func (gm *groupMessage) AfterDelete(tx *gorm.DB) error {
	return tx.Where("frame_id = ? AND frame_type = ?", gm.ID, typeGroupMessage).Delete(&deliveryRecord{}).Error
}

func (gm *groupMessage) getID() uuid.UUID {
	return gm.ID
}

func (gm *groupMessage) getScope(_ uuid.UUID) int {
	return scopeGroup
}

func (gm *groupMessage) getDestination(_ uuid.UUID) uuid.UUID {
	return gm.Destination
}

func (gm *groupMessage) getType() uint16 {
	return typeGroupMessage
}

func (gm *groupMessage) getPayload() []byte {
	gm.payloadMutex.Lock()
	defer gm.payloadMutex.Unlock()

	if len(gm.payload) == 0 {
		bytes, err := msgpack.Marshal(signedContainer{
			Payload:   gm.OriginalPayload,
			Signature: gm.Signature,
			Signer:    gm.Signer,
		})
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error marshalling group message's signed container")
		}
		gm.payload = bytes
	}

	return gm.payload
}

func (gm *groupMessage) getAuthor() uuid.UUID {
	return gm.Author
}

func (gm *groupMessage) getTimestamp() int64 {
	return gm.WrittenAt
}

func (b *bounce) handleGroupMessage(peer string, payload []byte) {
	groupMessageMutex.Lock()
	defer groupMessageMutex.Unlock()

	// Look up the device that sent it
	srcDevice, exists := b.getDeviceFromAddress(peer)
	if !exists {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("ignoring a group message sent from an unknown device")
		return
	}

	// Verify and unpack the signed container
	sc, err := b.unpackSignedContainer(payload)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unpacking signed container for group message")
		return
	}
	var gm groupMessage
	err = msgpack.Unmarshal(sc.Payload, &gm)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error unmarshalling group message")
	}
	gm.OriginalPayload = sc.Payload
	gm.Signature = sc.Signature
	gm.Signer = sc.Signer

	// If we have already seen this message, all we need to do is mark that this peer has the message and ack it
	var existingGM groupMessage
	err = b.database.Where("id = ?", gm.ID).First(&existingGM).Error
	if err == nil {
		b.markDeliveredTo(&existingGM, peer)
		go b.sendAck(peer, typeGroupMessage, gm.ID)
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up group message")
	}

	// If the message is older than the group's ClearBefore, don't process it
	if b.groupMessageWrittenBeforeHistoryCleared(gm.Destination, gm.WrittenAt) {
		log.WithFields(log.Fields{
			"user":       gm.Author,
			"group":      gm.Destination,
			"written_at": gm.WrittenAt,
		}).Debug("ignoring a group message that was written before the history was cleared")
		return
	}

	// Capture the current message retention setting for this group and store it on the message
	if gm.RetentionSeconds != 0 {
		gm.DeleteAt = time.Now().Unix() + gm.RetentionSeconds
	}

	// Make sure the author is in the group
	if !b.userIsInGroup(gm.Author, gm.Destination) {
		log.WithFields(log.Fields{
			"user":  gm.Author,
			"group": gm.Destination,
		}).Warn("user sent message to a group they are not in, ignoring")
		return
	}

	// Make sure the device that signed this message belongs to the author
	if !b.signedByUser(sc, gm.Author) {
		log.WithFields(log.Fields{
			"id":     gm.ID,
			"group":  gm.Destination,
			"signer": sc.Signer,
			"author": gm.Author,
		}).Warn("received group message signed by a different user than the author, ignoring")
		return
	}

	// Make sure the peer that delivered this message is part of the group
	if !b.userIsInGroup(srcDevice.UserID, gm.Destination) {
		log.WithFields(log.Fields{
			"user":   srcDevice.UserID,
			"device": srcDevice.ID,
			"group":  gm.Destination,
		}).Warn("device sent a message for a group that the device's user is not a part of, ignoring")
		return
	}

	// Make sure the user has permission to post
	var g group
	err = b.database.Select("admins", "restrict_posting").Where("id = ?", gm.Destination).Find(&g).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"group_id": gm.Destination,
			}).Error("group not found when checking posting permission")
			return
		} else {
			log.WithFields(log.Fields{
				"group_id": gm.Destination,
			}).Fatal("database error looking up group posting permission")
		}
	}
	if g.hasAdmins() && g.RestrictPosting && !b.isGroupAdmin(gm.Destination, gm.Author) {
		log.WithFields(log.Fields{
			"user_id": gm.Author,
		}).Warn("user attempted to post in a group without permission")
		return
	}

	// Mark that the peer that sent this message has it
	b.markDeliveredTo(&gm, peer)

	// Make sure the user interface isn't still displaying that the user is typing
	b.clearUserTypingIndicator(gm.Author, gm.Destination)

	// Save the new group message
	err = b.database.Create(&gm).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving group message")
	}

	// Inform the UI about the new message
	b.userInterface.DisplayGroupMessage(GroupMessage{
		ID:        gm.ID,
		Author:    gm.Author,
		Thread:    gm.Destination,
		WrittenAt: gm.WrittenAt,
		SavedAt:   gm.SavedAt,
		Text:      gm.Text,
	})

	// Update the activity timestamp on the group model
	b.updateLastGroupActivity(gm.Destination, gm.SavedAt)

	// Ack it
	go b.sendAck(peer, typeGroupMessage, gm.ID)

	// Broadcast it
	b.broadcast(&gm)
}

func (b *bounce) sendGroupMessage(message GroupMessage) {
	if message.ID != uuid.Nil {
		log.Fatal("group message ID cannot be set by the UI")
	}

	now := time.Now()
	gm := &groupMessage{
		ID:               uuid.New(),
		WrittenAt:        now.Unix(),
		Read:             true,
		Author:           b.currentUserID(),
		Destination:      message.Thread,
		RetentionSeconds: b.getGroupRetention(message.Thread),
		Text:             message.Text,
	}

	var err error
	gm.OriginalPayload, err = msgpack.Marshal(gm)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling group message")
	}
	sc := b.createSignedContainer(gm.OriginalPayload)
	gm.Signature = sc.Signature
	gm.Signer = sc.Signer

	err = b.database.Create(gm).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving group message")
	}
	b.updateLastGroupActivity(gm.Destination, gm.SavedAt)

	go b.checkIfGroupMessageUndeliverableAt(now.Add(undeliverableAfter).Unix(), gm.ID)

	b.userInterface.DisplayGroupMessage(GroupMessage{
		ID:            gm.ID,
		Author:        gm.Author,
		Thread:        gm.getDestination(b.currentUserID()),
		WrittenAt:     gm.WrittenAt,
		SavedAt:       gm.SavedAt,
		Text:          gm.Text,
		Expires:       gm.DeleteAt,
		Read:          true, // TODO
		Undeliverable: gm.Undeliverable,
	})

	b.broadcast(gm)
}

func (b *bounce) deleteGroupMessageAt(timestamp int64, id uuid.UUID) {
	// Sleep as long as needed
	duration := timestamp - time.Now().Unix()
	if duration > 0 {
		time.Sleep(time.Duration(duration) * time.Second)
	}

	// Delete from the database
	err := b.database.Where("id = ?", id).Delete(&groupMessage{}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"message_id": id,
			"error":      err.Error(),
		}).Fatal("error deleting group message that expired")
	}

	// Delete from the UI
	b.userInterface.DeleteItem(id)
}

func (b *bounce) checkIfGroupMessageUndeliverableAt(timestamp int64, id uuid.UUID) {
	// Create a receiver for delivery notifications
	delivered := make(chan bool)
	gmDeliveryNotificationMutex.Lock()
	gmDeliveryNotifications[id] = delivered
	gmDeliveryNotificationMutex.Unlock()

	// Create a timer that waits until the timestamp
	duration := time.Duration(timestamp-time.Now().Unix()) * time.Second
	sleeper := time.NewTimer(duration)
	defer sleeper.Stop()

	// If this message gets ack'd then just return here, otherwise wait until the timestamp
	select {
	case <-delivered:
		gmDeliveryNotificationMutex.Lock()
		delete(gmDeliveryNotifications, id)
		gmDeliveryNotificationMutex.Unlock()
		return
	case <-sleeper.C:
		gmDeliveryNotificationMutex.Lock()
		delete(gmDeliveryNotifications, id)
		gmDeliveryNotificationMutex.Unlock()
		break
	}

	// Check if the message still hasn't been delivered
	var count int64
	err := b.database.Model(&deliveryRecord{}).Where("frame_id = ? AND frame_type = ?", id, typeGroupMessage).Count(&count).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error counting delivery records for group message")
	}

	// If it hasn't been delivered, mark it as undeliverable and schedule deletion if there's a retention setting
	if count == 0 {
		// Find the DM
		var gm groupMessage
		err = b.database.Where("id = ?", id).First(&gm).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"message_id": id,
				}).Warn("no group message found when checking for delivery")
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("error looking up group message")
			}
		}

		// This message is undeliverable, update the undeliverable field and inform the UI
		err = b.database.Model(&gm).Update("undeliverable", true).Error
		if err != nil {
			log.WithFields(log.Fields{
				"message_id": gm.ID,
				"error":      err.Error(),
			}).Fatal("error updating undeliverable field of undeliverable group message")
		}
		b.userInterface.MarkMessageUndeliverable(gm.ID)

		// Check if this message also has a retention setting
		if gm.RetentionSeconds > 0 {
			if gm.RetentionSeconds > int64(undeliverableAfter.Seconds()) {
				// This message has a retention setting that is longer than the delivery window
				deleteAt := time.Now().Unix() + gm.RetentionSeconds - int64(undeliverableAfter.Seconds())
				err = b.database.Model(&gm).Update("delete_at", deleteAt).Error
				if err != nil {
					log.WithFields(log.Fields{
						"message_id": gm.ID,
						"error":      err.Error(),
					}).Fatal("error updating delete_at of undeliverable group message with retention")
				}
				go b.deleteGroupMessageAt(deleteAt, gm.ID)
				b.userInterface.UpdateMessageDeletionTime(gm.ID, deleteAt)
			} else {
				// This message has a retention setting that is shorter than the delivery window, we can delete it now
				err = b.database.Where("id = ?", gm.ID).Delete(&groupMessage{}).Error
				if err != nil {
					log.WithFields(log.Fields{
						"message_id": gm.ID,
						"error":      err.Error(),
					}).Fatal("error deleting undeliverable group message with retention")
				}
				b.userInterface.DeleteItem(gm.ID)
			}
		}
	}
}
