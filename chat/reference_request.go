package chat

import (
	"errors"
	"strings"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
)

type referenceRequest references

func (rr *referenceRequest) getScope() int {
	return DEVICE_SCOPE
}

func (rr *referenceRequest) getDestination() uuid.UUID {
	// TODO: derive from rr.For?
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

func (bounce *Bounce) handleReferenceRequest(peer string, payload []byte) {
	var rr referenceRequest
	err := msgpack.Unmarshal(payload, &rr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling reference request")
		return
	}

	var originalOffer referenceOffer
	err = bounce.database.Where("id = ? AND for = ?", rr.ID, peer).First(&originalOffer).Error
	if err != nil {
		log.WithFields(log.Fields{
			"peer":     peer,
			"offer_id": rr.ID,
		}).Error("peer sent a reference request for an offer not in the database")
		return
	}
	bounce.devicePool.receivedAcksMutex.Lock()
	bounce.devicePool.receivedAcks[rr.ID.String()] = true
	bounce.devicePool.receivedAcksMutex.Unlock()

	var dev device
	res := bounce.database.Model(&device{}).Where("address = ?", peer).First(&dev)
	if res.Error != nil {
		log.WithFields(log.Fields{
			"error": res.Error.Error(),
		}).Error("error loading device that sent reference request")
		return
	}

	dmRequested := make(map[uuid.UUID]bool)
	if len(rr.DirectMessages) > 0 {
		for _, dmIDString := range strings.Split(rr.DirectMessages, ",") {
			dmID, err := uuid.Parse(dmIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":  err.Error(),
					"string": dmIDString,
				}).Error("invalid UUID in reference request")
				continue
			}
			dmRequested[dmID] = true
		}
	}

	catchUpResponse := &catchUp{
		ID:          uuid.New(),
		destination: dev.ID,
	}

	if len(originalOffer.DirectMessages) > 0 {
		for _, dmIDString := range strings.Split(originalOffer.DirectMessages, ",") {
			dmID, err := uuid.Parse(dmIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":  err.Error(),
					"string": dmIDString,
				}).Error("invalid UUID in reference offer generated locally")
				continue
			}

			var dm DirectMessage
			err = bounce.database.Where("id = ? AND (source = ? OR destination = ?)", dmID, dev.UserID, dev.UserID).First(&dm).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("reference request asks for unknown DM")
					// TODO: detect if any are legit but out of scope for security reasons?
					continue
				} else {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Fatal("error loading DM from database")
				}
			}

			if _, present := dmRequested[dmID]; present {
				catchUpResponse.DirectMessages = append(catchUpResponse.DirectMessages, dm.getPayload())
			} else {
				bounce.markDeliveredTo(&dm, peer)
			}
		}
	}

	if catchUpResponse.hasContent() {
		bounce.broadcastCatchUp(catchUpResponse)
	}

	if len(rr.DirectMessages) > len(originalOffer.DirectMessages) {
		log.WithFields(log.Fields{
			"offer_length":   len(originalOffer.DirectMessages),
			"request_length": len(rr.DirectMessages),
		}).Error("reference request is requesting more direct messages than were offered")
		// TODO: save the evidence for analysis?
	}

	bounce.database.Delete(&originalOffer)
}
