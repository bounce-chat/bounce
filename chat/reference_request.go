package chat

import (
	"errors"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/Basekick-Labs/msgpack/v6"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// A reference request is sent in response to a reference offer if the offer contained frame references that
// are needed by the device
type referenceRequest struct {
	References []frameReference
}

func (rr *referenceRequest) getType() uint16 {
	return typeReferenceRequest
}

func (rr *referenceRequest) getPayload() []byte {
	bytes, err := msgpack.Marshal(rr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal reference request")
	}
	return bytes
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

	// Get all of the requested frames and pack them into a catch up frame
	offerable := b.getReferenceOfferFor(peer)
	offeredIDs := referencedIDs(offerable.References)
	requestedIDs := referencedIDs(rr.References)
	broadcastables := []broadcastable{}
	broadcastables = append(broadcastables, b.getRequestedDirectMessagePayloads(requestedIDs[typeDirectMessage], offeredIDs[typeDirectMessage])...)
	broadcastables = append(broadcastables, b.getRequestedGroupMessagePayloads(requestedIDs[typeGroupMessage], offeredIDs[typeGroupMessage])...)
	broadcastables = append(broadcastables, b.getRequestedUpdateDMsPayloads(requestedIDs[typeUpdateDM], offeredIDs[typeUpdateDM])...)
	broadcastables = append(broadcastables, b.getRequestedDevicesPayloads(requestedIDs[typeDevice], offeredIDs[typeDevice])...)
	broadcastables = append(broadcastables, b.getRequestedAddUsersPayloads(requestedIDs[typeAddUser], offeredIDs[typeAddUser])...)
	broadcastables = append(broadcastables, b.getRequestedGroupCreationPayloads(requestedIDs[typeGroupCreation], offeredIDs[typeGroupCreation])...)
	broadcastables = append(broadcastables, b.getRequestedUpdateGroupsPayloads(requestedIDs[typeUpdateGroup], offeredIDs[typeUpdateGroup])...)
	broadcastables = append(broadcastables, b.getRequestedConfirmationsPayloads(requestedIDs[typeConfirmation], offeredIDs[typeConfirmation], getValidRequestedUUIDs(offeredIDs[typeUpdateGroup], requestedIDs[typeUpdateGroup]))...)
	broadcastables = append(broadcastables, b.getRequestedUpdateUsersPayloads(requestedIDs[typeUpdateUser], offeredIDs[typeUpdateUser])...)
	broadcastables = append(broadcastables, b.getRequestedUpdateDevicesPayloads(requestedIDs[typeUpdateDevice], offeredIDs[typeUpdateDevice])...)
	broadcastables = append(broadcastables, b.getRequestedReadReceiptPayloads(requestedIDs[typeReadReceipt], offeredIDs[typeReadReceipt])...)
	broadcastables = append(broadcastables, b.getRequestedUpdateSettingsPayloads(requestedIDs[typeUpdateSettings], offeredIDs[typeUpdateSettings])...)
	broadcastables = append(broadcastables, b.getRequestedFilePayloads(requestedIDs[typeFile], offeredIDs[typeFile])...)
	broadcastables = append(broadcastables, b.getRequestedChunkOfferPayloads(requestedIDs[typeChunkOffer], offeredIDs[typeChunkOffer])...)

	if len(broadcastables) == 0 {
		return nil, false
	}

	if _, ok := encryptedDeviceCache[peer]; ok {
		// If we're preparing this catch up for an encrypted device, pack it with encrypted versions of the frames
		cuForEncryptedDevice := &catchUp{
			sendables: sortableSendables{},
		}

		for _, br := range broadcastables {
			s := b.encryptFrameForDevice(br, peer)
			if s != nil {
				cuForEncryptedDevice.sendables = append(cuForEncryptedDevice.sendables, s)
			} else {
				log.WithFields(log.Fields{
					"id":   br.getID(),
					"type": br.getType(),
				}).Warn("excluding broadcastable from catch up that could not be encrypted")
			}
		}

		ars := b.getRequestedAppendRecipientPayloads(requestedIDs[typeAppendRecipient], offeredIDs[typeAppendRecipient])
		for _, ar := range ars {
			cuForEncryptedDevice.sendables = append(cuForEncryptedDevice.sendables, ar)
		}

		go b.sendDirect(peer, cuForEncryptedDevice)
	} else {
		// If we're preparing this catch up for a normal device, pack the frames in directly
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

func (b *Bounce) getRequestedDirectMessagePayloads(requestedIDs, offeredIDs []uuid.UUID) []broadcastable {
	requestedData := []broadcastable{}

	requestedDirectMessageIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

	for _, dmID := range requestedDirectMessageIDs {
		var dm directMessage
		err := b.database.Preload(clause.Associations).Where("id = ?", dmID).First(&dm).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id": dmID,
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

func (b *Bounce) getRequestedGroupMessagePayloads(requestedIDs, offeredIDs []uuid.UUID) []broadcastable {
	requestedData := []broadcastable{}

	requestedGroupMessageIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

	for _, gmID := range requestedGroupMessageIDs {
		var gm groupMessage
		err := b.database.Preload(clause.Associations).Where("id = ?", gmID).First(&gm).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id": gmID,
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

func (b *Bounce) getRequestedUpdateDMsPayloads(requestedIDs, offeredIDs []uuid.UUID) []broadcastable {
	requestedData := []broadcastable{}

	requestedUpdateDMsIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

	for _, udID := range requestedUpdateDMsIDs {
		var ud updateDM
		err := b.database.First(&ud, "id = ?", udID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id": udID,
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

func (b *Bounce) getRequestedUpdateGroupsPayloads(requestedIDs, offeredIDs []uuid.UUID) []broadcastable {
	requestedData := []broadcastable{}

	requestedUpdateGroupsIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

	for _, ugID := range requestedUpdateGroupsIDs {
		var ug updateGroup
		err := b.database.Preload(clause.Associations).First(&ug, "id = ?", ugID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id": ugID,
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

func (b *Bounce) getRequestedGroupCreationPayloads(requestedIDs, offeredIDs []uuid.UUID) []broadcastable {
	requestedData := []broadcastable{}

	requestedGroupCreationIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

	for _, groupCreationID := range requestedGroupCreationIDs {
		var gc groupCreation
		err := b.database.First(&gc, "id = ?", groupCreationID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id": groupCreationID,
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

func (b *Bounce) getRequestedDevicesPayloads(requestedIDs, offeredIDs []uuid.UUID) []broadcastable {
	requestedData := []broadcastable{}

	requestedDeviceIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

	for _, deviceID := range requestedDeviceIDs {
		var dev device
		err := b.database.Preload(clause.Associations).First(&dev, "id = ?", deviceID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id": deviceID,
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

func (b *Bounce) getRequestedAddUsersPayloads(requestedIDs, offeredIDs []uuid.UUID) []broadcastable {
	requestedData := []broadcastable{}

	requestedAddUserIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

	for _, addUserID := range requestedAddUserIDs {
		var au addUser
		err := b.database.First(&au, "id = ?", addUserID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id": addUserID,
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

func (b *Bounce) getRequestedConfirmationsPayloads(requestedIDs, offeredIDs, ugsToDeliver []uuid.UUID) []broadcastable {
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
					"id": confirmationID,
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

func (b *Bounce) getRequestedUpdateUsersPayloads(requestedIDs, offeredIDs []uuid.UUID) []broadcastable {
	requestedData := []broadcastable{}

	requestedUpdateUserIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

	for _, updateUserID := range requestedUpdateUserIDs {
		var uu updateUser
		err := b.database.First(&uu, "id = ?", updateUserID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id": updateUserID,
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

func (b *Bounce) getRequestedUpdateDevicesPayloads(requestedIDs, offeredIDs []uuid.UUID) []broadcastable {
	requestedData := []broadcastable{}

	requestedUpdateDevicesIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

	for _, updateDeviceID := range requestedUpdateDevicesIDs {
		var ud updateDevice
		err := b.database.First(&ud, "id = ?", updateDeviceID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id": updateDeviceID,
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

func (b *Bounce) getRequestedReadReceiptPayloads(requestedIDs, offeredIDs []uuid.UUID) []broadcastable {
	requestedData := []broadcastable{}

	requestedReadReceiptIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

	for _, readReceiptID := range requestedReadReceiptIDs {
		var rr readReceipt
		err := b.database.First(&rr, "id = ?", readReceiptID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id": readReceiptID,
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

func (b *Bounce) getRequestedUpdateSettingsPayloads(requestedIDs, offeredIDs []uuid.UUID) []broadcastable {
	requestedData := []broadcastable{}

	requestedUpdateSettingsIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

	for _, updateSettingsID := range requestedUpdateSettingsIDs {
		var us updateSettings
		err := b.database.First(&us, "id = ?", updateSettingsID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id": updateSettingsID,
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

func (b *Bounce) getRequestedFilePayloads(requestedIDs, offeredIDs []uuid.UUID) []broadcastable {
	requestedData := []broadcastable{}

	requestedFileIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

	for _, fileID := range requestedFileIDs {
		var f file
		err := b.database.First(&f, "id = ?", fileID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id": fileID,
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

func (b *Bounce) getRequestedChunkOfferPayloads(requestedIDs, offeredIDs []uuid.UUID) []broadcastable {
	requestedData := []broadcastable{}

	requestedChunkOfferIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

	for _, chunkOfferID := range requestedChunkOfferIDs {
		var co chunkOffer
		err := b.database.First(&co, "id = ?", chunkOfferID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id": chunkOfferID,
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

func (b *Bounce) getRequestedAppendRecipientPayloads(requestedIDs, offeredIDs []uuid.UUID) []sortableSendable {
	requestedData := []sortableSendable{}

	requestedAppendRecipientIDs := getValidRequestedUUIDs(offeredIDs, requestedIDs)

	for _, appendRecipientID := range requestedAppendRecipientIDs {
		var ar appendRecipient
		err := b.database.First(&ar, "id = ?", appendRecipientID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id": appendRecipientID,
				}).Warn("reference request asks for unknown append recipient we offered")
			} else {
				log.WithFields(log.Fields{
					"id":    appendRecipientID,
					"error": err.Error(),
				}).Fatal("database error querying for appendRecipient")
			}
		} else {
			requestedData = append(requestedData, ar)
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
