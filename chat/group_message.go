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

//
// A group message is sent from a member of a group to a group
//
type GroupMessage struct {
	ID               uuid.UUID `gorm:"type:uuid;primary_key;"`
	SavedAt          int64     `msgpack:"-"`
	WrittenAt        int64
	RetentionSeconds int64 // Number of seconds to retain this message, captures the retention setting from the author's perspective at the time the message was written
	DeleteAt         int64 `msgpack:"-"` // Absolute time at which the messages expires.  Time it was first acked/received + RetentionSeconds
	Read             bool  `msgpack:"-"`
	Undeliverable    bool  `msgpack:"-"` // The message was never delivered to another device and is beyond when we give up including it in reference offers
	Source           uuid.UUID
	Destination      uuid.UUID
	Text             string
	Signer           string `msgpack:"-" gorm:"not null"`
	OriginalPayload  []byte `msgpack:"-" gorm:"not null"`
	Signature        []byte `msgpack:"-" gorm:"not null"`
	payload          []byte
	payloadMutex     sync.Mutex
}

func (gm *GroupMessage) BeforeCreate(tx *gorm.DB) error {
	if gm.ID == uuid.Nil {
		return errors.New("group message must have an ID assigned before creation")
	}
	gm.SavedAt = time.Now().Unix()
	return nil
}

func (gm *GroupMessage) AfterDelete(tx *gorm.DB) error {
	return tx.Where("frame_id = ? AND frame_type = ?", gm.ID, typeGroupMessage).Delete(&deliveryRecord{}).Error
}

func (gm *GroupMessage) getID() uuid.UUID {
	return gm.ID
}

func (gm *GroupMessage) getScope(_ uuid.UUID) int {
	return scopeGroup
}

func (gm *GroupMessage) getDestination(_ uuid.UUID) uuid.UUID {
	return gm.Destination
}

func (gm *GroupMessage) getType() uint16 {
	return typeGroupMessage
}

func (gm *GroupMessage) getPayload() []byte {
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

func (gm *GroupMessage) getTimestamp() int64 {
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
	var gm GroupMessage
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
	var existingGM GroupMessage
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
			"user":       gm.Source,
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
	if !b.userIsInGroup(gm.Source, gm.Destination) {
		log.WithFields(log.Fields{
			"user":  gm.Source,
			"group": gm.Destination,
		}).Warn("user sent message to a group they are not in, ignoring")
		return
	}

	// Make sure the device that signed this message belongs to the author
	if !b.signedByUser(sc, gm.Source) {
		log.WithFields(log.Fields{
			"id":     gm.ID,
			"group":  gm.Destination,
			"signer": sc.Signer,
			"author": gm.Source,
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

	// Mark that the peer that sent this message has it
	b.markDeliveredTo(&gm, peer)

	// Inform the UI about the new message
	b.userInterface.ReceivedGroupMessage(gm)

	// Make sure the user interface isn't still displaying that the user is typing
	b.clearUserTypingIndicator(gm.Source, gm.Destination)

	// Save the new group message
	err = b.database.Create(&gm).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving group message")
	}

	// Ack it
	go b.sendAck(peer, typeGroupMessage, gm.ID)

	// Broadcast it
	go b.broadcast(&gm)
}

func (b *bounce) sendGroupMessage(gm *GroupMessage) uuid.UUID {
	gm.ID = uuid.New()
	gm.WrittenAt = time.Now().Unix()
	gm.Read = true
	gm.Source = b.currentUserID()
	gm.RetentionSeconds = b.getGroupRetention(gm.Destination)

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

	go b.broadcast(gm)

	return gm.ID
}
