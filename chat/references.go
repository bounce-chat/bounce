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
	ID             uuid.UUID `gorm:"type:uuid;primary_key;" json:"-"`
	For            string    `msgpack:"-"`
	DirectMessages string    // Comma-separated list of DM UUIDs
	destination    uuid.UUID
	payload        []byte
}

//
// A reference offer is a message sent to a newly connected device that provides the UUIDs of any messages that device
// should have, but that we didn't deliver to it.
//
type referenceOffer references

func (bounce *Bounce) getReferenceOfferFor(address string) (*referenceOffer, bool) {
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
		return offer, false
	}
	if res.RowsAffected == 0 {
		// Not a device we know about in the database, no references to offer
		return offer, false
	}
	offer.destination = dev.ID

	// After a week, we give up trying to deliver messages to a device we haven't seen
	aWeekAgo := time.Now().Add(-7 * 24 * time.Hour).Unix() // TODO: also do a sane LIMIT?

	// All DMs we have sent to or received from this user in the past week
	var dms []DirectMessage
	err := bounce.database.
		Order("created_at asc"). // TODO: receieved at?
		Where("created_at >= ? AND destination = ?", aWeekAgo, dev.UserID).
		Or("created_at >= ? AND source = ?", aWeekAgo, dev.UserID).
		Find(&dms).Error
	if err != nil {
		log.WithFields(log.Fields{
			"peer":  address,
			"error": err.Error(),
		}).Error("error loading DMs for reference offer")
		return offer, false
	}

	// for each dm, if it isn't delivered to this address, include it
	dmsToOffer := []string{}
	for _, dm := range dms {
		if !dm.isAlreadyDeliveredTo(address) {
			dmsToOffer = append(dmsToOffer, dm.ID.String())
		}
	}
	offer.DirectMessages = strings.Join(dmsToOffer, ",")

	needToSend := false
	if len(offer.DirectMessages) > 0 { // TODO: check everything else we're going to send in here
		needToSend = true
	}

	if needToSend {
		err := bounce.database.Create(offer).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error saving referenceOffer")
		}
	}

	return offer, needToSend
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

//	getScope() int
//	getDestination() uuid.UUID
//	getType() uint16
//	getPayload() []byte
