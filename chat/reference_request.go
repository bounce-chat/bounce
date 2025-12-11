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

// A reference request is sent in response to a reference offer if the offer contained frame references that
// are needed by the device
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

func (b *Bounce) handleReferenceRequest(peer string, payload []byte, _ bool) (broadcastable, bool) {
	// Unmarshal the reference request
	var rr referenceRequest
	err := msgpack.Unmarshal(payload, &rr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling reference request")
		return nil, false
	}

	if b.encrypted {
		go b.sendDirect(peer, b.generateEncryptedCatchUpFor(peer, rr))
		return nil, false
	}

	// Get the device for this peer
	dev, ok := b.getDeviceFromAddress(peer)
	if !ok {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("got reference request from unknown peer device, ignoring")
		return nil, false
	}

	// Get all of the requested frames and pack them into a catch up frame
	offerable := b.getReferenceOfferFor(peer)
	offeredIDs := referencedIDs(offerable.References)
	requestedIDs := referencedIDs(rr.References)
	broadcastables := []broadcastable{}
	broadcastables = append(broadcastables, b.getRequestedDirectMessagePayloads(dev, requestedIDs[typeDirectMessage], offeredIDs[typeDirectMessage])...)
	broadcastables = append(broadcastables, b.getRequestedGroupMessagePayloads(dev, requestedIDs[typeGroupMessage], offeredIDs[typeGroupMessage])...)
	broadcastables = append(broadcastables, b.getRequestedUpdateDMsPayloads(dev, requestedIDs[typeUpdateDM], offeredIDs[typeUpdateDM])...)
	broadcastables = append(broadcastables, b.getRequestedDevicesPayloads(dev, requestedIDs[typeDevice], offeredIDs[typeDevice])...)
	broadcastables = append(broadcastables, b.getRequestedAddUsersPayloads(dev, requestedIDs[typeAddUser], offeredIDs[typeAddUser])...)
	broadcastables = append(broadcastables, b.getRequestedGroupCreationPayloads(dev, requestedIDs[typeGroupCreation], offeredIDs[typeGroupCreation])...)
	broadcastables = append(broadcastables, b.getRequestedUpdateGroupsPayloads(dev, requestedIDs[typeUpdateGroup], offeredIDs[typeUpdateGroup])...)
	broadcastables = append(broadcastables, b.getRequestedConfirmationsPayloads(dev, requestedIDs[typeConfirmation], offeredIDs[typeConfirmation], getValidRequestedUUIDs(offeredIDs[typeUpdateGroup], requestedIDs[typeUpdateGroup]))...)
	broadcastables = append(broadcastables, b.getRequestedUpdateUsersPayloads(dev, requestedIDs[typeUpdateUser], offeredIDs[typeUpdateUser])...)
	broadcastables = append(broadcastables, b.getRequestedUpdateDevicesPayloads(dev, requestedIDs[typeUpdateDevice], offeredIDs[typeUpdateDevice])...)
	broadcastables = append(broadcastables, b.getRequestedReadReceiptPayloads(dev, requestedIDs[typeReadReceipt], offeredIDs[typeReadReceipt])...)
	broadcastables = append(broadcastables, b.getRequestedUpdateSettingsPayloads(dev, requestedIDs[typeUpdateSettings], offeredIDs[typeUpdateSettings])...)
	broadcastables = append(broadcastables, b.getRequestedFilePayloads(dev, requestedIDs[typeFile], offeredIDs[typeFile])...)
	broadcastables = append(broadcastables, b.getRequestedChunkOfferPayloads(dev, requestedIDs[typeChunkOffer], offeredIDs[typeChunkOffer])...)

	if len(broadcastables) == 0 {
		return nil, false
	}

	if _, ok := encryptedDeviceCache[peer]; ok {
		// If we're preparing this catch up for an encrypted device, pack it with encrypted versions of the frames
		cuForEncryptedDevice := &catchUp{
			sendables: sortableSendables{},
		}
		for _, br := range broadcastables {
			cuForEncryptedDevice.sendables = append(cuForEncryptedDevice.sendables, b.encryptFrameForDevice(br, peer))
		}
		go b.sendDirect(peer, cuForEncryptedDevice)
	} else {
		// If we're preparing this catch up for a formal device, pack the frames in directly
		cu := &catchUp{
			sendables: sortableSendables{},
		}
		for _, br := range broadcastables {
			s, ok := br.(sortableSendable)
			if !ok {
				log.Error("broadcastable frame is not also a sortableSendable frame")
			}
			cu.sendables = append(cu.sendables, s)
		}
		go b.sendDirect(peer, cu)
	}

	return nil, false
}

func (b *Bounce) getRequestedDirectMessagePayloads(peer device, requestedIDs, offeredIDs []uuid.UUID) []broadcastable {
	requestedData := []broadcastable{}

	requestedDirectMessageIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

	for _, dmID := range requestedDirectMessageIDs {
		var dm directMessage
		err := b.database.Preload(clause.Associations).Where("id = ?", dmID).First(&dm).Error
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

func (b *Bounce) getRequestedGroupMessagePayloads(peer device, requestedIDs, offeredIDs []uuid.UUID) []broadcastable {
	requestedData := []broadcastable{}

	requestedGroupMessageIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

	for _, gmID := range requestedGroupMessageIDs {
		var gm groupMessage
		err := b.database.Preload(clause.Associations).Where("id = ?", gmID).First(&gm).Error
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

func (b *Bounce) getRequestedUpdateDMsPayloads(peer device, requestedIDs, offeredIDs []uuid.UUID) []broadcastable {
	requestedData := []broadcastable{}

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

func (b *Bounce) getRequestedUpdateGroupsPayloads(peer device, requestedIDs, offeredIDs []uuid.UUID) []broadcastable {
	requestedData := []broadcastable{}

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

func (b *Bounce) getRequestedGroupCreationPayloads(peer device, requestedIDs, offeredIDs []uuid.UUID) []broadcastable {
	requestedData := []broadcastable{}

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

func (b *Bounce) getRequestedDevicesPayloads(peer device, requestedIDs, offeredIDs []uuid.UUID) []broadcastable {
	requestedData := []broadcastable{}

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

func (b *Bounce) getRequestedAddUsersPayloads(peer device, requestedIDs, offeredIDs []uuid.UUID) []broadcastable {
	requestedData := []broadcastable{}

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

func (b *Bounce) getRequestedConfirmationsPayloads(peer device, requestedIDs, offeredIDs, ugsToDeliver []uuid.UUID) []broadcastable {
	requestedData := []broadcastable{}

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

func (b *Bounce) getRequestedUpdateUsersPayloads(peer device, requestedIDs, offeredIDs []uuid.UUID) []broadcastable {
	requestedData := []broadcastable{}

	requestedUpdateUserIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

	for _, updateUserID := range requestedUpdateUserIDs {
		var uu updateUser
		err := b.database.First(&uu, "id = ?", updateUserID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   updateUserID,
					"peer": peer.Address,
				}).Warn("reference request asks for unknown update user we offered")
			} else {
				log.WithFields(log.Fields{
					"id":    updateUserID,
					"error": err.Error(),
				}).Fatal("database error querying for update user")
			}
		} else {
			requestedData = append(requestedData, &uu)
		}
	}

	return requestedData
}

func (b *Bounce) getRequestedUpdateDevicesPayloads(peer device, requestedIDs, offeredIDs []uuid.UUID) []broadcastable {
	requestedData := []broadcastable{}

	requestedUpdateDevicesIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

	for _, updateDeviceID := range requestedUpdateDevicesIDs {
		var ud updateDevice
		err := b.database.First(&ud, "id = ?", updateDeviceID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   updateDeviceID,
					"peer": peer.Address,
				}).Warn("reference request asks for unknown update device we offered")
			} else {
				log.WithFields(log.Fields{
					"id":    updateDeviceID,
					"error": err.Error(),
				}).Fatal("database error querying for update device")
			}
		} else {
			requestedData = append(requestedData, &ud)
		}
	}

	return requestedData
}

func (b *Bounce) getRequestedReadReceiptPayloads(peer device, requestedIDs, offeredIDs []uuid.UUID) []broadcastable {
	requestedData := []broadcastable{}

	requestedReadReceiptIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

	for _, readReceiptID := range requestedReadReceiptIDs {
		var rr readReceipt
		err := b.database.First(&rr, "id = ?", readReceiptID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   readReceiptID,
					"peer": peer.Address,
				}).Warn("reference request asks for unknown read receipt we offered")
			} else {
				log.WithFields(log.Fields{
					"id":    readReceiptID,
					"error": err.Error(),
				}).Fatal("database error querying for read receipt")
			}
		} else {
			requestedData = append(requestedData, &rr)
		}
	}

	return requestedData
}

func (b *Bounce) getRequestedUpdateSettingsPayloads(peer device, requestedIDs, offeredIDs []uuid.UUID) []broadcastable {
	requestedData := []broadcastable{}

	requestedUpdateSettingsIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

	for _, updateSettingsID := range requestedUpdateSettingsIDs {
		var us updateSettings
		err := b.database.First(&us, "id = ?", updateSettingsID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   updateSettingsID,
					"peer": peer.Address,
				}).Warn("reference request asks for unknown update settings we offered")
			} else {
				log.WithFields(log.Fields{
					"id":    updateSettingsID,
					"error": err.Error(),
				}).Fatal("database error querying for update settings")
			}
		} else {
			requestedData = append(requestedData, &us)
		}
	}

	return requestedData
}

func (b *Bounce) getRequestedFilePayloads(peer device, requestedIDs, offeredIDs []uuid.UUID) []broadcastable {
	requestedData := []broadcastable{}

	requestedFileIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

	for _, fileID := range requestedFileIDs {
		var f file
		err := b.database.First(&f, "id = ?", fileID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   fileID,
					"peer": peer.Address,
				}).Warn("reference request asks for unknown file we offered")
			} else {
				log.WithFields(log.Fields{
					"id":    fileID,
					"error": err.Error(),
				}).Fatal("database error querying for file")
			}
		} else {
			requestedData = append(requestedData, &f)
		}
	}

	return requestedData
}

func (b *Bounce) getRequestedChunkOfferPayloads(peer device, requestedIDs, offeredIDs []uuid.UUID) []broadcastable {
	requestedData := []broadcastable{}

	requestedChunkOfferIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

	for _, chunkOfferID := range requestedChunkOfferIDs {
		var co chunkOffer
		err := b.database.First(&co, "id = ?", chunkOfferID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   chunkOfferID,
					"peer": peer.Address,
				}).Warn("reference request asks for unknown chunk offer we offered")
			} else {
				log.WithFields(log.Fields{
					"id":    chunkOfferID,
					"error": err.Error(),
				}).Fatal("database error querying for chunk offer")
			}
		} else {
			requestedData = append(requestedData, &co)
		}
	}

	return requestedData
}

// Given two lists of UUIDs, one representing the original offer and the other representing what
// was requested by the peer, separate them into the valid requested UUIDs and the UUIDs that we
// can assume were already delivered because they were not requested.
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
		// Make sure that this requested UUID is something we are still offering, and skip it if not.
		// This can sometimes happen for legitimate reasons and is not automatically evidence of bad
		// behavior from a client.
		if _, present := offeredCache[requestedID]; !present {
			log.WithFields(log.Fields{
				"id": requestedID,
			}).Debug("reference request asks for UUID not present in reference offer")
			continue
		}

		// Include the requested UUID in the requested set
		requestedSet = append(requestedSet, requestedID)
	}

	return requestedSet
}
