package chat

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
)

type DirectMessage message

func (directMessage *DirectMessage) BeforeCreate(tx *gorm.DB) error {
	directMessage.ReceivedAt = time.Now().Unix()
	return nil
}

func (dm *DirectMessage) getScope() int {
	return USER_SCOPE
}

func (dm *DirectMessage) getDestination() uuid.UUID {
	return dm.Destination
}

func (dm *DirectMessage) getType() uint16 {
	return TYPE_DIRECT_MESSAGE
}

func (dm *DirectMessage) getPayload() []byte {
	if len(dm.payload) == 0 {
		bytes, err := msgpack.Marshal(dm)
		if err != nil {
			// TODO: how to handle?
		}
		dm.payload = bytes
	}
	return dm.payload
}

func (dm *DirectMessage) isAlreadyDeliveredTo(address string) bool {
	// TODO: reload from the database?
	recipients := strings.Split(dm.DeliveredTo, ",")
	for _, recipient := range recipients {
		if address == recipient {
			return true
		}
	}
	return false
}

func (bounce *Bounce) markDeliveredTo(dm *DirectMessage, address string) {
	if !dm.isAlreadyDeliveredTo(address) {
		if len(dm.DeliveredTo) != 0 {
			dm.DeliveredTo = dm.DeliveredTo + ","
		}
		dm.DeliveredTo = dm.DeliveredTo + address

		err := bounce.database.Save(dm).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":   err.Error(),
				"message": dm.ID,
			}).Error("error updating direct message delivery status")
		}
	}
}

func (bounce *Bounce) handleDirectMessage(peer string, payload []byte) {
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
	srcDevice, exists := bounce.getDeviceFromAddress(peer)
	if !exists {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("an unknown device sent a direct message, ignoring")
		return
	}

	// TODO: Ensure that this device is a sync device, or that it is either the source or destination of the message while the other side is us

	// If we have already seen this message, all we need to do is mark that this peer has the message as well.  If not, we save the message
	// in the database.  This step is synchroniszed with a mutex lock since the same message can come in concurrently during gossip.
	bounce.dmExistenceCheck.Lock()
	var existingDM DirectMessage
	err = bounce.database.Where("id = ?", dm.ID).First(&existingDM).Error
	if err == nil {
		bounce.markDeliveredTo(&existingDM, peer)
		bounce.dmExistenceCheck.Unlock()
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error lookinf up direct message")
	}

	// Capture the current message retention setting for this user and store it on the DM
	id := dm.Destination
	if id == bounce.currentUserID() {
		id = dm.Source
	}
	dm.RetentionSeconds = bounce.getUserDMRetention(id)

	// Save the new message
	dm.DeliveredTo = peer
	err = bounce.database.Create(&dm).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error saving incoming direct message")
	}
	bounce.dmExistenceCheck.Unlock()

	// Send the message to the user interface
	bounce.userInterface.ReceivedDirectMessage(dm)

	// Send an ack to the sender that we got it
	go bounce.broadcast(&ack{
		destination:    srcDevice.ID,
		DirectMessages: dm.ID.String(),
	})
	// gossip it as needed, or references to it (if it's a small group and we're pretty sure that this peer is connected to everyone else, decide that here or automatically in the broadcast function?)
}
