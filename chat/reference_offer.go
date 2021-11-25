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

//
// A reference offer is a message sent to a newly connected device that provides the UUIDs of any messages that device
// should have, but that we didn't deliver to it.
//
type referenceOffer struct {
	_msgpack       struct{} `msgpack:",omitempty"`
	CreatedAt      int64
	ID             uuid.UUID `gorm:"type:uuid;primary_key;"`
	For            string    `msgpack:"-"`
	DirectMessages string    // Comma-separated list of DM UUIDs
	destination    uuid.UUID
	payload        []byte
}

func (referenceOffer *referenceOffer) BeforeCreate(tx *gorm.DB) error {
	referenceOffer.CreatedAt = time.Now().Unix()
	return nil
}

func (ro *referenceOffer) getScope() int {
	return DEVICE_SCOPE
}

func (ro *referenceOffer) getDestination() uuid.UUID {
	// TODO: derive from ro.For?
	return ro.destination
}

func (ro *referenceOffer) getType() uint16 {
	return TYPE_REFERENCE_OFFER
}

func (ro *referenceOffer) getPayload() []byte {
	if len(ro.payload) == 0 {
		bytes, err := msgpack.Marshal(ro)
		if err != nil {
			// TODO: how to handle?
		}
		ro.payload = bytes
	}
	return ro.payload
}

func (ro *referenceOffer) isAlreadyDeliveredTo(address string) bool {
	return false
}

func (ro *referenceOffer) shouldDial() bool {
	if len(ro.DirectMessages) > 0 {
		return true
	}
	// TODO: changes to my device group, clearing thread histories, etc
	return false
}

func (ro *referenceOffer) hasContent() bool {
	if len(ro.DirectMessages) > 0 {
		return true
	}
	// TODO: check for any future referenced content
	return false
}

func (bounce *Bounce) sendReferences(peerAddress string) {
	references := bounce.getReferenceOfferFor(peerAddress)
	if references.hasContent() {
		err := bounce.database.Create(references).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error saving referenceOffer")
		} else {
			go bounce.broadcastReferenceOffer(references)
		}
	}
}

func (bounce *Bounce) getReferenceOfferFor(address string) *referenceOffer {
	offer := &referenceOffer{
		ID:  uuid.New(),
		For: address,
	}

	dev, ok := bounce.getDeviceFromAddress(address)
	if !ok {
		return offer
	}
	offer.destination = dev.ID

	// All DMs we have sent to or received from this user in the past week
	var dms []DirectMessage
	err := bounce.database.
		Order("saved_at asc").
		Where(
			"written_at >= ? AND (destination = ? OR source = ?) AND delivered_to NOT LIKE ?",
			time.Now().Add(-undeliverableAfter).Unix(),
			dev.UserID,
			dev.UserID,
			"%"+address+"%",
		).Find(&dms).Error // TODO: also add a sane limit?
	if err != nil {
		log.WithFields(log.Fields{
			"peer":  address,
			"error": err.Error(),
		}).Error("error loading DMs for reference offer")
		return offer
	}

	// Collect IDs from the DMs
	dmsToOffer := []string{}
	for _, dm := range dms {
		dmsToOffer = append(dmsToOffer, dm.ID.String())
	}
	offer.DirectMessages = strings.Join(dmsToOffer, ",")

	return offer
}

func (bounce *Bounce) broadcastReferenceOffer(ro *referenceOffer) {
	giveUpTime := time.Now().Add(5 * time.Minute)
	for {
		bounce.broadcast(ro)
		time.Sleep(15 * time.Second) // TODO: derive from message size?
		bounce.devicePool.receivedAcksMutex.Lock()
		_, ok := bounce.devicePool.receivedAcks[ro.ID.String()]
		bounce.devicePool.receivedAcksMutex.Unlock()
		if ok {
			// we got the request, our offer was delivered
			bounce.devicePool.receivedAcksMutex.Lock()
			delete(bounce.devicePool.receivedAcks, ro.ID.String())
			bounce.devicePool.receivedAcksMutex.Unlock()
			return
		}
		if time.Now().After(giveUpTime) {
			log.WithFields(log.Fields{
				"id":          ro.ID,
				"destination": ro.For,
			}).Warn("gave up attempting to deliver reference offer")
			err := bounce.database.Delete(ro).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("error deleting reference offer after gave up on broadcasting")
			}
			return
		}
	}
}

func (bounce *Bounce) handleReferenceOffer(peer string, payload []byte) {
	var ro referenceOffer
	err := msgpack.Unmarshal(payload, &ro)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling reference offer")
		return
	}

	srcDevice, exists := bounce.getDeviceFromAddress(peer)
	if !exists {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Info("error loading device for reference offer handling")
		return
	}

	response := &referenceRequest{
		ID:          ro.ID,
		destination: srcDevice.ID,
	}

	requestedDMs := []string{}
	if len(ro.DirectMessages) > 0 {
		for _, dmIDString := range strings.Split(ro.DirectMessages, ",") {
			dmID, err := uuid.Parse(dmIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":  err.Error(),
					"string": dmIDString,
				}).Error("invalid UUID in reference offer")
				continue
			}

			var count int64
			err = bounce.database.Model(&DirectMessage{}).Where("id = ?", dmID).Count(&count).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error getting message from database")
				continue
			}
			if count == 0 {
				// TODO: want to make sure this can't be used to probe a devices for messages that it might already have from another chat,
				// to test group membership if a chat message from another group is known
				requestedDMs = append(requestedDMs, dmID.String())
			} else {
				// We already have this message, but if we didn't already know they had it, we update our records
				var dm DirectMessage
				err = bounce.database.First(&dm, dmID).Error
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error looking up known DM in reference offer")
					continue
				}
				if !dm.isAlreadyDeliveredTo(peer) {
					bounce.markDeliveredTo(&dm, peer)
				}
			}
		}
	}

	for i, dm := range requestedDMs {
		if i != 0 {
			response.DirectMessages = response.DirectMessages + ","
		}
		response.DirectMessages = response.DirectMessages + dm
	}

	bounce.broadcast(response)
}
