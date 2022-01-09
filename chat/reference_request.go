package chat

import (
	"errors"
	"strings"
	"sync"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DirectMessages are comma separated for consistency with reference offers, which must do this
// since SQLite doesn't support slices
type referenceRequest struct {
	_msgpack              struct{} `msgpack:",omitempty"`
	ID                    uuid.UUID
	DirectMessages        string // Comma-separated list of DM UUIDs
	UpdateLocalDMSettings string
	Devices               string
	Users                 string
	destination           uuid.UUID
	payload               []byte
	payloadMutex          sync.Mutex
}

func (rr *referenceRequest) getID() uuid.UUID {
	return rr.ID
}

func (rr *referenceRequest) getScope(_ uuid.UUID) int {
	return scopeDevice
}

func (rr *referenceRequest) getDestination(_ uuid.UUID) uuid.UUID {
	return rr.destination
}

func (rr *referenceRequest) getType() uint16 {
	return typeReferenceRequest
}

func (rr *referenceRequest) getPayload() []byte {
	rr.payloadMutex.Lock()
	defer rr.payloadMutex.Unlock()

	if len(rr.payload) == 0 {
		bytes, err := msgpack.Marshal(rr)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal reference request")
		}
		rr.payload = bytes
	}
	return rr.payload
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

	// Mark that the original offer was delivered so that we can stop sending it
	b.markDeliveredTo(&originalOffer, dev.Address)

	// Everything the device has requested will b packed into a "catch up" message
	catchUpResponse := &catchUp{
		ID:                    uuid.New(),
		destination:           dev.ID,
		DirectMessages:        b.getRequestedDirectMessagePayloads(dev, rr, originalOffer),
		UpdateLocalDMSettings: b.getRequestedUpdateLocalDMSettingsPayloads(dev, rr, originalOffer),
		Devices:               b.getRequestedDevicesPayloads(dev, rr, originalOffer),
		Users:                 b.getRequestedUsersPayloads(dev, rr, originalOffer),
	}

	if catchUpResponse.hasContent() {
		b.broadcastUntilDelivered(catchUpResponse)
	}

	// TODO: broadcast separate catchups for each requested image/audio/file here.  Or rather than a catch up, just broadcast the data.

	b.database.Delete(&originalOffer) // TODO: error check
}

func (b *bounce) getRequestedDirectMessagePayloads(peer device, rr referenceRequest, originalOffer referenceOffer) [][]byte {
	requestedData := [][]byte{}

	requestedDirectMessageIDs, deliveredDirectMessageIDs := getRequestedAndDeliveredUUIDs(originalOffer.DirectMessages, rr.DirectMessages)

	for _, dmID := range deliveredDirectMessageIDs {
		var dm DirectMessage
		err := b.database.Where("id = ?", dmID).First(&dm).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   dmID,
					"peer": peer.Address,
				}).Warn("reference request indicates we offered an unknown direct message")
			} else {
				log.WithFields(log.Fields{
					"id":    dmID,
					"error": err.Error(),
				}).Fatal("database error querying for direct message")
			}
		} else {
			b.markDeliveredTo(&dm, peer.Address)
		}
	}

	for _, dmID := range requestedDirectMessageIDs {
		var dm DirectMessage
		err := b.database.Where("id = ?", dmID).First(&dm).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   dmID,
					"peer": peer.Address,
				}).Warn("reference request asks for an unknown direct message")
			} else {
				log.WithFields(log.Fields{
					"id":    dmID,
					"error": err.Error(),
				}).Fatal("database error querying for direct message")
			}
		} else {
			requestedData = append(requestedData, dm.getPayload())
		}
	}

	return requestedData
}

func (b *bounce) getRequestedUpdateLocalDMSettingsPayloads(peer device, rr referenceRequest, originalOffer referenceOffer) [][]byte {
	requestedData := [][]byte{}

	requestedUpdateLocalDMSettingsIDs, deliveredUpdateLocalDMSettingsIDs := getRequestedAndDeliveredUUIDs(originalOffer.UpdateLocalDMSettings, rr.UpdateLocalDMSettings)

	if len(requestedUpdateLocalDMSettingsIDs) > 0 {
		// TODO: since we're never going to offer these and the diff function checks that, this shouldn't be needed,
		// but might catch bugs that result in us offering these
		if peer.UserID != b.currentUserID() {
			log.WithFields(log.Fields{
				"peer": peer.Address,
			}).Warn("non-sync device asked for update local DM settings in reference request, ignoring")
			return requestedData
		}
	}

	for _, uldsID := range deliveredUpdateLocalDMSettingsIDs {
		var ulds updateLocalDMSettings
		err := b.database.First(&ulds, "id = ?", uldsID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   uldsID,
					"peer": peer.Address,
				}).Warn("reference request indicates we offered an unknown update local DM settings")
			} else {
				log.WithFields(log.Fields{
					"id":    uldsID,
					"error": err.Error(),
				}).Fatal("database error querying for update local DM settings")
			}
		} else {
			b.markDeliveredTo(&ulds, peer.Address)
		}
	}

	for _, uldsID := range requestedUpdateLocalDMSettingsIDs {
		var ulds updateLocalDMSettings
		err := b.database.First(&ulds, "id = ?", uldsID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   uldsID,
					"peer": peer.Address,
				}).Warn("reference request asks for an unknown update local DM settings")
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

	return requestedData
}

func (b *bounce) getRequestedDevicesPayloads(peer device, rr referenceRequest, originalOffer referenceOffer) [][]byte {
	requestedData := [][]byte{}

	requestedDeviceIDs, deliveredDeviceIDs := getRequestedAndDeliveredUUIDs(originalOffer.Devices, rr.Devices)

	for _, deviceID := range deliveredDeviceIDs {
		var dev device
		err := b.database.Preload(clause.Associations).First(&dev, "id = ?", deviceID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   deviceID,
					"peer": peer.Address,
				}).Warn("reference request indicates we offered an unknown device")
			} else {
				log.WithFields(log.Fields{
					"id":    deviceID,
					"error": err.Error(),
				}).Fatal("database error querying for device")
			}
		} else {
			b.markDeliveredTo(&dev, peer.Address)
		}
	}

	for _, deviceID := range requestedDeviceIDs {
		var dev device
		err := b.database.Preload(clause.Associations).First(&dev, "id = ?", deviceID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   deviceID,
					"peer": peer.Address,
				}).Warn("reference request asks for unknown device we offered")
			} else {
				log.WithFields(log.Fields{
					"id":    deviceID,
					"error": err.Error(),
				}).Fatal("database error querying for device")
			}
		} else {
			requestedData = append(requestedData, dev.getPayload())
		}
	}

	return requestedData
}

func (b *bounce) getRequestedUsersPayloads(peer device, rr referenceRequest, originalOffer referenceOffer) [][]byte {
	requestedData := [][]byte{}

	requestedUserIDs, deliveredUserIDs := getRequestedAndDeliveredUUIDs(originalOffer.Users, rr.Users)

	if len(requestedUserIDs) > 0 {
		if peer.UserID != b.currentUserID() {
			log.WithFields(log.Fields{
				"peer": peer.Address,
			}).Warn("non-sync device asked for users in reference request, ignoring")
			return requestedData
		}
	}

	for _, userID := range deliveredUserIDs {
		var u user
		err := b.database.First(&u, "id = ?", userID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   userID,
					"peer": peer.Address,
				}).Warn("reference request indicates we offered an unknown user")
			} else {
				log.WithFields(log.Fields{
					"id":    userID,
					"error": err.Error(),
				}).Fatal("database error querying for user")
			}
		} else {
			b.markDeliveredTo(&u, peer.Address)
		}
	}

	for _, userID := range requestedUserIDs {
		var u user
		err := b.database.First(&u, "id = ?", userID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   userID,
					"peer": peer.Address,
				}).Warn("reference request asks for unknown user we offered")
			} else {
				log.WithFields(log.Fields{
					"id":    userID,
					"error": err.Error(),
				}).Fatal("database error querying for user")
			}
		} else {
			requestedData = append(requestedData, u.getPayload())
		}
	}

	return requestedData
}

//
// Given two comma-separated lists of UUIDs, one representing the original offer and the other representing what
// was requested from the peer, parse them and separate them into the valid requested UUIDs and the UUIDs that we
// can assume were already delivered because they were not requested.
//
func getRequestedAndDeliveredUUIDs(originalOffer string, requested string) ([]uuid.UUID, []uuid.UUID) {
	requestedSet := []uuid.UUID{}
	deliveredSet := []uuid.UUID{}

	offeredCache := make(map[uuid.UUID]bool)
	if len(originalOffer) > 0 {
		for _, offeredIDString := range strings.Split(originalOffer, ",") {
			offeredID, err := uuid.Parse(offeredIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":  err.Error(),
					"string": offeredIDString,
				}).Error("invalid UUID in reference offer generated locally")
				continue
			}
			if _, present := offeredCache[offeredID]; present {
				log.WithFields(log.Fields{
					"id": offeredID,
				}).Warn("duplicate UUID in reference offer generated locally")
				continue
			}
			offeredCache[offeredID] = true
		}
	}

	requestedCache := make(map[uuid.UUID]bool)
	if len(requested) > 0 {
		for _, requestedIDString := range strings.Split(requested, ",") {
			// Parse the UUID
			requestedID, err := uuid.Parse(requestedIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":  err.Error(),
					"string": requestedIDString,
				}).Warn("invalid UUID in reference request")
				continue
			}

			// Detect if there's a duplicate UUID in the request
			if _, present := requestedCache[requestedID]; present {
				log.WithFields(log.Fields{
					"id": requestedID,
				}).Warn("duplicate UUID in reference request")
				continue
			}

			// Add this UUID to the cache
			requestedCache[requestedID] = true

			// Make sure that this requested UUID was actually offered and skip it if not
			if _, present := offeredCache[requestedID]; !present {
				log.WithFields(log.Fields{
					"error": err.Error(),
					"id":    requestedID,
				}).Warn("reference request asks for UUID not present in reference offer")
				continue
			}

			// Include the requested UUID in the requested set
			requestedSet = append(requestedSet, requestedID)
		}
	}

	for offeredID, _ := range offeredCache {
		// Check if this UUID was offered but it was not requested, indicating it has already been delivered
		if _, present := requestedCache[offeredID]; !present {
			deliveredSet = append(deliveredSet, offeredID)
		}
	}

	return requestedSet, deliveredSet
}
