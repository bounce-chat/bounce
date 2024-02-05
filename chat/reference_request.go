package chat

import (
	"errors"
	"sync"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

//
// A reference request is sent in response to a reference offer if the offer contained frame references that
// are needed by the device
//
type referenceRequest struct {
	References   []frameReference
	payload      []byte
	payloadMutex sync.Mutex
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
	// Unmarshal the reference request
	var rr referenceRequest
	err := msgpack.Unmarshal(payload, &rr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling reference request")
		return
	}

	// Get the device for this peer
	dev, ok := b.getDeviceFromAddress(peer)
	if !ok {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("got reference request from unknown peer device, ignoring")
		return
	}

	// Get all of the requested frames and pack them into a catch up frame
	offerable := b.getReferenceOfferFor(peer)
	offeredIDs := referencedIDs(offerable.References)
	requestedIDs := referencedIDs(rr.References)
	cu := &catchUp{
		broadcastables: sortableBroadcastables{},
	}
	cu.broadcastables = append(cu.broadcastables, b.getRequestedDirectMessagePayloads(dev, requestedIDs[typeDirectMessage], offeredIDs[typeDirectMessage])...)
	cu.broadcastables = append(cu.broadcastables, b.getRequestedGroupMessagePayloads(dev, requestedIDs[typeGroupMessage], offeredIDs[typeGroupMessage])...)
	cu.broadcastables = append(cu.broadcastables, b.getRequestedUpdateDMsPayloads(dev, requestedIDs[typeUpdateDM], offeredIDs[typeUpdateDM])...)
	cu.broadcastables = append(cu.broadcastables, b.getRequestedDevicesPayloads(dev, requestedIDs[typeDevice], offeredIDs[typeDevice])...)
	cu.broadcastables = append(cu.broadcastables, b.getRequestedAddUsersPayloads(dev, requestedIDs[typeAddUser], offeredIDs[typeAddUser])...)
	cu.broadcastables = append(cu.broadcastables, b.getRequestedGroupCreationPayloads(dev, requestedIDs[typeGroupCreation], offeredIDs[typeGroupCreation])...)
	cu.broadcastables = append(cu.broadcastables, b.getRequestedUpdateGroupsPayloads(dev, requestedIDs[typeUpdateGroup], offeredIDs[typeUpdateGroup])...)
	cu.broadcastables = append(cu.broadcastables, b.getRequestedConfirmationsPayloads(dev, requestedIDs[typeConfirmation], offeredIDs[typeConfirmation], getValidRequestedUUIDs(offeredIDs[typeUpdateGroup], requestedIDs[typeUpdateGroup]))...)

	// Send the catchup if there's anything to send
	if len(cu.broadcastables) > 0 {
		go b.sendDirect(peer, cu)
	}
}

func (b *bounce) getRequestedDirectMessagePayloads(peer device, requestedIDs, offeredIDs []uuid.UUID) sortableBroadcastables {
	requestedData := sortableBroadcastables{}

	requestedDirectMessageIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

	for _, dmID := range requestedDirectMessageIDs {
		var dm directMessage
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
			requestedData = append(requestedData, &dm)
		}
	}

	return requestedData
}

func (b *bounce) getRequestedGroupMessagePayloads(peer device, requestedIDs, offeredIDs []uuid.UUID) sortableBroadcastables {
	requestedData := sortableBroadcastables{}

	requestedGroupMessageIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

	for _, gmID := range requestedGroupMessageIDs {
		var gm groupMessage
		err := b.database.Where("id = ?", gmID).First(&gm).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   gmID,
					"peer": peer.Address,
				}).Warn("reference request asks for an unknown group message")
			} else {
				log.WithFields(log.Fields{
					"id":    gmID,
					"error": err.Error(),
				}).Fatal("database error querying for group message")
			}
		} else {
			requestedData = append(requestedData, &gm)
		}
	}

	return requestedData
}

func (b *bounce) getRequestedUpdateDMsPayloads(peer device, requestedIDs, offeredIDs []uuid.UUID) sortableBroadcastables {
	requestedData := sortableBroadcastables{}

	requestedUpdateDMsIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

	for _, udID := range requestedUpdateDMsIDs {
		var ud updateDM
		err := b.database.First(&ud, "id = ?", udID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   udID,
					"peer": peer.Address,
				}).Warn("reference request asks for an unknown update DM settings")
			} else {
				log.WithFields(log.Fields{
					"id":    udID,
					"error": err.Error(),
				}).Fatal("database error querying for update DM settings")
			}
		} else {
			requestedData = append(requestedData, &ud)
		}
	}

	return requestedData
}

func (b *bounce) getRequestedUpdateGroupsPayloads(peer device, requestedIDs, offeredIDs []uuid.UUID) sortableBroadcastables {
	requestedData := sortableBroadcastables{}

	requestedUpdateGroupsIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

	for _, ugID := range requestedUpdateGroupsIDs {
		var ug updateGroup
		err := b.database.Preload(clause.Associations).First(&ug, "id = ?", ugID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   ugID,
					"peer": peer.Address,
				}).Warn("reference request asks for an unknown update group")
			} else {
				log.WithFields(log.Fields{
					"id":    ugID,
					"error": err.Error(),
				}).Fatal("database error querying for update group")
			}
		} else {
			requestedData = append(requestedData, &ug)
		}
	}

	return requestedData
}

func (b *bounce) getRequestedGroupCreationPayloads(peer device, requestedIDs, offeredIDs []uuid.UUID) sortableBroadcastables {
	requestedData := sortableBroadcastables{}

	requestedGroupCreationIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

	for _, groupCreationID := range requestedGroupCreationIDs {
		var gc groupCreation
		err := b.database.First(&gc, "id = ?", groupCreationID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   groupCreationID,
					"peer": peer.Address,
				}).Warn("reference request asks for unknown group creation we offered")
			} else {
				log.WithFields(log.Fields{
					"id":    groupCreationID,
					"error": err.Error(),
				}).Fatal("database error querying for group creation")
			}
		} else {
			requestedData = append(requestedData, &gc)
		}
	}

	return requestedData
}

func (b *bounce) getRequestedDevicesPayloads(peer device, requestedIDs, offeredIDs []uuid.UUID) sortableBroadcastables {
	requestedData := sortableBroadcastables{}

	requestedDeviceIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

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
			requestedData = append(requestedData, &dev)
		}
	}

	return requestedData
}

func (b *bounce) getRequestedAddUsersPayloads(peer device, requestedIDs, offeredIDs []uuid.UUID) sortableBroadcastables {
	requestedData := sortableBroadcastables{}

	requestedAddUserIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

	for _, addUserID := range requestedAddUserIDs {
		var au addUser
		err := b.database.First(&au, "id = ?", addUserID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   addUserID,
					"peer": peer.Address,
				}).Warn("reference request asks for unknown add user we offered")
			} else {
				log.WithFields(log.Fields{
					"id":    addUserID,
					"error": err.Error(),
				}).Fatal("database error querying for add user")
			}
		} else {
			requestedData = append(requestedData, &au)
		}
	}

	return requestedData
}

func (b *bounce) getRequestedConfirmationsPayloads(peer device, requestedIDs, offeredIDs, ugsToDeliver []uuid.UUID) sortableBroadcastables {
	requestedData := sortableBroadcastables{}

	includedInUG := map[uuid.UUID]bool{}
	for _, id := range ugsToDeliver {
		includedInUG[id] = true
	}

	requestedConfirmationIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

	for _, confirmationID := range requestedConfirmationIDs {
		var c confirmation
		err := b.database.First(&c, "id = ?", confirmationID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   confirmationID,
					"peer": peer.Address,
				}).Warn("reference request asks for unknown confirmation we offered")
			} else {
				log.WithFields(log.Fields{
					"id":    confirmationID,
					"error": err.Error(),
				}).Fatal("database error querying for confirmation")
			}
		} else {
			if _, present := includedInUG[c.UpdateGroupID]; present {
				continue
			}
			requestedData = append(requestedData, &c)
		}

	}

	return requestedData
}

//
// Given two lists of UUIDs, one representing the original offer and the other representing what
// was requested by the peer, separate them into the valid requested UUIDs and the UUIDs that we
// can assume were already delivered because they were not requested.
//
func getValidRequestedUUIDs(originalOffer []uuid.UUID, requested []uuid.UUID) []uuid.UUID {
	requestedSet := []uuid.UUID{}

	offeredCache := make(map[uuid.UUID]bool)
	for _, offeredID := range originalOffer {
		if _, present := offeredCache[offeredID]; present {
			log.WithFields(log.Fields{
				"id": offeredID,
			}).Warn("duplicate UUID in reference offer generated locally")
			continue
		}
		offeredCache[offeredID] = true
	}

	for _, requestedID := range requested {
		// Make sure that this requested UUID was actually offered and skip it if not
		if _, present := offeredCache[requestedID]; !present {
			log.WithFields(log.Fields{
				"id": requestedID,
			}).Warn("reference request asks for UUID not present in reference offer")
			continue
		}

		// Include the requested UUID in the requested set
		requestedSet = append(requestedSet, requestedID)
	}

	return requestedSet
}
