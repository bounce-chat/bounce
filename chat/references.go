package chat

import (
	"strings"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
)

//
// References area a struct that contain a list of UUIDs for every type of message that Bounce stores
// in the database and needs to confirm delivery of to another device.  This structure is used for
// three types of broadcastable frames: reference offers, reference requests, and acks.
//
type references struct {
	_msgpack       struct{}  `msgpack:",omitempty"`
	ID             uuid.UUID `gorm:"type:uuid;primary_key;" json:"-"` // TODO: why did I omit json?
	For            string    `msgpack:"-"`
	DirectMessages string    // Comma-separated list of DM UUIDs
	CatchUps       string    // for ack-ing catch ups to ensure delivery.  TODO: maybe not what we stick with.
	destination    uuid.UUID
	payload        []byte
}

type catchUp struct {
	_msgpack       struct{} `msgpack:",omitempty"`
	ID             uuid.UUID
	DirectMessages [][]byte
	destination    uuid.UUID
	payload        []byte
}

func (cu *catchUp) getScope() int {
	return DEVICE_SCOPE
}

func (cu *catchUp) getDestination() uuid.UUID {
	return cu.destination

}

func (cu *catchUp) getType() uint16 {
	return TYPE_CATCH_UP
}

func (cu *catchUp) getPayload() []byte {
	if len(cu.payload) == 0 {
		bytes, err := msgpack.Marshal(cu)
		if err != nil {
			// TODO: how to handle?
		}
		cu.payload = bytes
	}
	return cu.payload
}

func (cu *catchUp) isAlreadyDeliveredTo(address string) bool {
	return false
}

func (cu *catchUp) hasContent() bool {
	if len(cu.DirectMessages) > 0 {
		return true
	}
	return false
}

//
// A reference offer is a message sent to a newly connected device that provides the UUIDs of any messages that device
// should have, but that we didn't deliver to it.
//
type referenceOffer references

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

	var dev device
	res := bounce.database.Model(&device{}).Where("address = ?", address).First(&dev)
	if res.Error != nil {
		log.WithFields(log.Fields{
			"error": res.Error.Error(),
		}).Error("error loading device for incoming connection") // TODO: make sure we don't error with every unknown device
		return offer
	}
	if res.RowsAffected == 0 {
		// Not a device we know about in the database, no references to offer
		return offer
	}
	offer.destination = dev.ID

	// After a week, we give up trying to deliver messages to a device we haven't seen
	aWeekAgo := time.Now().Add(-7 * 24 * time.Hour).Unix() // TODO: also do a sane LIMIT?

	// All DMs we have sent to or received from this user in the past week
	var dms []DirectMessage
	err := bounce.database.
		Where(
			"created_at >= ? AND (destination = ? OR source = ?) AND delivered_to NOT LIKE ?",
			aWeekAgo,
			dev.UserID,
			dev.UserID,
			"%"+address+"%",
		).Find(&dms).Error
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

func (ro *referenceOffer) hasContent() bool {
	if len(ro.DirectMessages) > 0 {
		return true
	}
	// TODO: check for any future referenced content
	return false
}

func (ro *referenceOffer) getScope() int {
	return DEVICE_SCOPE
}

func (ro *referenceOffer) getDestination() uuid.UUID {
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

//
//
//
type referenceRequest references

func (rr *referenceRequest) getScope() int {
	return DEVICE_SCOPE
}

func (rr *referenceRequest) getDestination() uuid.UUID {
	return rr.destination
}

func (rr *referenceRequest) getType() uint16 {
	return TYPE_REFERENCE_REQUEST
}

func (rr *referenceRequest) getPayload() []byte {
	if len(rr.payload) == 0 {
		bytes, err := msgpack.Marshal(rr)
		if err != nil {
			// TODO: how to handle?
		}
		rr.payload = bytes
	}
	return rr.payload
}

func (rr *referenceRequest) isAlreadyDeliveredTo(address string) bool {
	return false
}

type ack references

func (a *ack) getScope() int {
	return DEVICE_SCOPE
}

func (a *ack) getDestination() uuid.UUID {
	return a.destination
}

func (a *ack) getType() uint16 {
	return TYPE_ACK
}

func (a *ack) getPayload() []byte {
	if len(a.payload) == 0 {
		bytes, err := msgpack.Marshal(a)
		if err != nil {
			// TODO: how to handle?
		}
		a.payload = bytes
	}
	return a.payload
}

func (a *ack) isAlreadyDeliveredTo(address string) bool {
	return false
}
