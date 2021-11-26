package chat

import (
	"errors"
	"strings"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
)

type referenceRequest struct {
	_msgpack       struct{} `msgpack:",omitempty"`
	ID             uuid.UUID
	For            string `msgpack:"-"`
	DirectMessages string // Comma-separated list of DM UUIDs
	destination    uuid.UUID
	payload        []byte
}

func (rr *referenceRequest) getScope() int {
	return scopeDevice
}

func (rr *referenceRequest) getDestination() uuid.UUID {
	// TODO: derive from rr.For?
	return rr.destination
}

func (rr *referenceRequest) getType() uint16 {
	return typeReferenceRequest
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

func (b *bounce) handleReferenceRequest(peer string, payload []byte) {
	var rr referenceRequest
	err := msgpack.Unmarshal(payload, &rr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling reference request")
		return
	}

	var originalOffer referenceOffer
	err = b.database.Where("id = ? AND for = ?", rr.ID, peer).First(&originalOffer).Error
	if err != nil {
		log.WithFields(log.Fields{
			"peer":     peer,
			"offer_id": rr.ID,
		}).Error("peer sent a reference request for an offer not in the database")
		return
	}
	b.devicePool.receivedAcksMutex.Lock()
	b.devicePool.receivedAcks[rr.ID.String()] = true
	b.devicePool.receivedAcksMutex.Unlock()

	dev, exists := b.getDeviceFromAddress(peer)
	if !exists {
		log.WithFields(log.Fields{
			"peer": peer,
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
			err = b.database.Where("id = ? AND (source = ? OR destination = ?)", dmID, dev.UserID, dev.UserID).First(&dm).Error
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
				b.markDeliveredTo(&dm, peer)
			}
		}
	}

	if catchUpResponse.hasContent() {
		b.broadcastCatchUp(catchUpResponse)
	}

	if len(rr.DirectMessages) > len(originalOffer.DirectMessages) {
		log.WithFields(log.Fields{
			"offer_length":   len(originalOffer.DirectMessages),
			"request_length": len(rr.DirectMessages),
		}).Error("reference request is requesting more direct messages than were offered")
		// TODO: save the evidence for analysis?
	}

	b.database.Delete(&originalOffer)
}
