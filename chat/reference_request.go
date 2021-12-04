package chat

import (
	"errors"
	"strings"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
)

// DirectMessages are comma separated for consistency with reference offers, which must do this
// since SQLite doesn't support slices
type referenceRequest struct {
	_msgpack              struct{} `msgpack:",omitempty"`
	ID                    uuid.UUID
	DirectMessages        string // Comma-separated list of DM UUIDs
	UpdateLocalDMSettings string
	destination           uuid.UUID
	payload               []byte
}

func (rr *referenceRequest) getScope() int {
	return scopeDevice
}

func (rr *referenceRequest) getDestination(_ uuid.UUID) uuid.UUID {
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

	// get the device from this address
	dev, ok := b.getDeviceFromAddress(peer)
	if !ok {
		log.WithFields(log.Fields{
			"reference_request_id": rr.ID,
			"peer":                 peer,
		}).Warn("got reference request from unknown peer device, ignoring")
		return
	}

	// Find the original offer that we made to this device in order to generate this request
	var originalOffer referenceOffer
	err = b.database.Where("id = ? AND destination = ?", rr.ID, dev.ID).First(&originalOffer).Error
	if err != nil {
		log.WithFields(log.Fields{
			"peer":     peer,
			"offer_id": rr.ID,
		}).Warn("peer sent a reference request for an offer not in the database, ignoring")
		return
	}
	b.devicePool.receivedAcksMutex.Lock()
	b.devicePool.receivedAcks[rr.ID.String()] = true
	b.devicePool.receivedAcksMutex.Unlock()

	// Everything the device has requested will b packed into a "catch up" message
	catchUpResponse := &catchUp{
		ID:          uuid.New(),
		destination: dev.ID,
	}

	catchUpResponse.DirectMessages = b.getRequestedDirectMessagePayloads(dev, rr, originalOffer)
	catchUpResponse.UpdateLocalDMSettings = b.getRequestedUpdateLocalDMSettingsPayloads(dev, rr, originalOffer)

	if catchUpResponse.hasContent() {
		b.broadcastCatchUp(catchUpResponse)
	}

	b.database.Delete(&originalOffer)
}

func (b *bounce) getRequestedDirectMessagePayloads(dev device, rr referenceRequest, originalOffer referenceOffer) [][]byte {
	requestedData := [][]byte{}

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
					}).Warn("reference request asks for unknown DM")
					// TODO: detect if any are legit but out of scope for security reasons
					continue
				} else {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Fatal("error loading DM from database")
				}
			}

			if _, present := dmRequested[dmID]; present {
				requestedData = append(requestedData, dm.getPayload())
			} else {
				b.markDirectMessageDeliveredTo(&dm, dev.Address)
			}
		}
	}

	if len(rr.DirectMessages) > len(originalOffer.DirectMessages) {
		log.WithFields(log.Fields{
			"offer_length":   len(originalOffer.DirectMessages),
			"request_length": len(rr.DirectMessages),
		}).Warn("reference request is requesting more direct messages than were offered")
	}

	return requestedData
}

func (b *bounce) getRequestedUpdateLocalDMSettingsPayloads(dev device, rr referenceRequest, originalOffer referenceOffer) [][]byte {
	requestedData := [][]byte{}

	if len(rr.UpdateLocalDMSettings) > 0 {
		if dev.UserID != b.currentUserID() {
			log.WithFields(log.Fields{
				"peer": dev.Address,
			}).Warn("non-sync device asked for update local DM settings in reference request, ignoring")
			return requestedData
		}
		for _, uldsIDString := range strings.Split(rr.UpdateLocalDMSettings, ",") {
			uldsID, err := uuid.Parse(uldsIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":  err.Error(),
					"string": uldsIDString,
				}).Error("invalid ulds UUID in reference request")
				continue
			}

			// TODO: check the original offer to make sure we offered it, just to detect bugs

			var ulds updateLocalDMSettings
			err = b.database.First(&ulds, "id = ?", uldsID).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					log.WithFields(log.Fields{
						"id":   uldsID,
						"peer": dev.Address,
					}).Warn("reference request asks for unknown update local DM settings")
				} else {
					log.WithFields(log.Fields{
						"id":    uldsID,
						"error": err.Error(),
					}).Fatal("database error querying for update local DM settings")
				}
			} else {
				requestedData = append(requestedData, ulds.getPayload())
			}
		}
	}

	return requestedData
}
