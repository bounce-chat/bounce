package chat

import (
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

//
// A reference offer is a message sent to a newly connected device that provides the UUIDs of any messages that device
// should have, but that we didn't deliver to it.
//
type referenceOffer struct {
	References   []frameReference
	destination  uuid.UUID
	payload      []byte
	payloadMutex sync.Mutex
}

func (ro *referenceOffer) getID() uuid.UUID {
	return uuid.Nil
}

func (ro *referenceOffer) getScope(_ uuid.UUID) int {
	return scopeDevice
}

func (ro *referenceOffer) getDestination(_ uuid.UUID) uuid.UUID {
	return ro.destination // TODO: just make it nil since it's only ever directly broadcast?
}

func (ro *referenceOffer) getType() uint16 {
	return typeReferenceOffer
}

func (ro *referenceOffer) getPayload() []byte {
	ro.payloadMutex.Lock()
	defer ro.payloadMutex.Unlock()

	if len(ro.payload) == 0 {
		bytes, err := msgpack.Marshal(ro)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal reference offer")
		}
		ro.payload = bytes
	}
	return ro.payload
}

func (ro *referenceOffer) shouldDial() bool {
	for _, reference := range ro.References {
		if reference.Type == typeDirectMessage {
			return true
		} else if reference.Type == typeGroupMessage {
			return true
		} else if reference.Type == typeDevice {
			return true
		} else if reference.Type == typeAddUser {
			return true
		} else if reference.Type == typeGroupCreation {
			return true
		} else if reference.Type == typeUpdateGroup {
			return true
		}
	}
	return false
}

func (b *bounce) sendReferences(peerAddress string) {
	if _, exists := b.currentUser(); !exists {
		// Our profile hasn't been setup yet, we have nothing to offer
		return
	}

	_, exists := b.getDeviceFromAddress(peerAddress)
	if !exists {
		// We have nothing to offer a device that we don't know about
		return
	}

	_, exists = b.getDeviceFromAddress(b.network.Address())
	if !exists {
		// Our own devices doesn't exist yet, we're probably going to be added as a new sync
		// device now.  In the meantime there's nothing to do.
		return
	}

	referenceOffer := b.getReferenceOfferFor(peerAddress)
	if len(referenceOffer.References) > 0 {
		go b.broadcastUntilDelivered(referenceOffer)
	}
}

func (b *bounce) getReferenceOfferFor(address string) *referenceOffer {
	dev, ok := b.getDeviceFromAddress(address)
	if !ok {
		return &referenceOffer{}
	}

	references := []frameReference{}
	references = append(references, b.getDirectMessagesToOffer(dev)...)
	references = append(references, b.getGroupMessagesToOffer(dev)...)
	references = append(references, b.getUpdateDMsToOffer(dev)...)
	references = append(references, b.getDevicesToOffer(dev)...)
	references = append(references, b.getAddUsersToOffer(dev)...)
	references = append(references, b.getGroupCreationsToOffer(dev)...)
	references = append(references, b.getUpdateGroupsToOffer(dev)...)

	return &referenceOffer{
		destination: dev.ID,
		References:  references,
	}
}

func (b *bounce) getDirectMessagesToOffer(dev device) []frameReference {
	// All DMs we have sent to or received from this user in the past week
	var dms []DirectMessage
	err := b.database.
		Select("direct_messages.*").
		Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == direct_messages.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", dev.Address, typeDirectMessage).
		Where(
			"delivery_records.id IS NULL AND direct_messages.written_at >= ? AND (direct_messages.destination = ? OR direct_messages.source = ?)",
			time.Now().Add(-undeliverableAfter).Unix(),
			dev.UserID,
			dev.UserID,
		).
		Find(&dms).Error
	if err != nil {
		log.WithFields(log.Fields{
			"peer":  dev.Address,
			"error": err.Error(),
		}).Fatal("error loading DMs for reference offer")
	}

	// Collect IDs from the DMs
	// TODO: only select IDs in the first place?
	references := []frameReference{}
	for _, dm := range dms {
		references = append(references, frameReference{FrameID: dm.ID, Type: typeDirectMessage})
	}
	return references
}

func (b *bounce) getGroupMessagesToOffer(dev device) []frameReference {
	// All group messages that this device's user is a part of that are less than a week old
	var gms []GroupMessage
	err := b.database.
		Select("group_messages.*").
		Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == group_messages.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", dev.Address, typeGroupMessage).
		Where(
			"delivery_records.id IS NULL AND group_messages.written_at >= ? AND group_messages.destination IN (?)",
			time.Now().Add(-undeliverableAfter).Unix(),
			b.database.
				Model(&group{}).
				Distinct().
				Select("groups.id").
				Joins("JOIN group_users ON groups.id = group_users.group_id").
				Where("group_users.user_id = ?", dev.UserID),
		).
		Find(&gms).Error
	if err != nil {
		log.WithFields(log.Fields{
			"peer":  dev.Address,
			"error": err.Error(),
		}).Fatal("error loading DMs for reference offer")
	}

	// TODO: only select IDs in the first place?
	references := []frameReference{}
	for _, gm := range gms {
		references = append(references, frameReference{FrameID: gm.ID, Type: typeGroupMessage})
	}
	return references
}

func (b *bounce) getUpdateDMsToOffer(dev device) []frameReference {
	var unsentUpdateDMs []updateDM

	if b.isSyncDevice(dev) {
		// Select all update DMs from any DM
		err := b.database.
			Select("update_dms.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_dms.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", dev.Address, typeUpdateDM).
			Where("delivery_records.id IS NULL").
			Find(&unsentUpdateDMs).Error // TODO: only select ID?
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error selecting update DM for reference offer")
		}
	} else {
		// Select updateDMs related to this user
		err := b.database.
			Select("update_dms.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_dms.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", dev.Address, typeUpdateDM).
			Where("update_dms.target = ? AND delivery_records.id IS NULL", xor(dev.UserID, b.currentUserID())).
			Find(&unsentUpdateDMs).Error // TODO: only select ID?
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error selecting update DM for reference offer")
		}
	}

	references := []frameReference{}
	for _, ud := range unsentUpdateDMs {
		// Ignore sync scoped update DMs unless this is a sync device
		if ud.getScope(b.currentUserID()) == scopeSync && !b.isSyncDevice(dev) {
			continue
		}
		references = append(references, frameReference{FrameID: ud.ID, Type: typeUpdateDM})
	}

	return references
}

func (b *bounce) getDevicesToOffer(dev device) []frameReference {
	var unsentDevices []device

	if b.isSyncDevice(dev) {
		// Sync devices can learn about any device we know about
		err := b.database.
			Select("devices.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == devices.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", dev.Address, typeDevice).
			Where("delivery_records.id IS NULL").
			Find(&unsentDevices).Error // TODO: only select ID?
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error selecting devices for reference offer")
		}
	} else {
		// TODO: don't just get our devices, get any "overlap" devices
		err := b.database.
			Select("devices.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == devices.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", dev.Address, typeDevice).
			Where("address != ? AND user_id = ? AND delivery_records.id IS NULL", dev.Address, b.currentUserID()).
			Find(&unsentDevices).Error // TODO: only select ID?
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error selecting devices for reference offer")
		}
	}

	references := []frameReference{}
	for _, dev := range unsentDevices {
		references = append(references, frameReference{FrameID: dev.ID, Type: typeDevice})
	}

	return references
}

func (b *bounce) getAddUsersToOffer(dev device) []frameReference {
	var unsentAddUsers []addUser

	if b.isSyncDevice(dev) {
		err := b.database.
			Select("add_users.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == add_users.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", dev.Address, typeAddUser).
			Where("delivery_records.id IS NULL").
			Find(&unsentAddUsers).Error // TODO: only select ID?
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error selecting users for reference offer")
		}
	} else {
		err := b.database.
			Select("add_users.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == add_users.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", dev.Address, typeAddUser).
			Where("delivery_records.id IS NULL AND add_users.xor = ?", xor(b.currentUserID(), dev.UserID)).
			Find(&unsentAddUsers).Error // TODO: only select ID?
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error selecting users for reference offer")
		}
	}

	references := []frameReference{}
	for _, au := range unsentAddUsers {
		references = append(references, frameReference{FrameID: au.ID, Type: typeAddUser})
	}

	return references
}

func (b *bounce) getGroupCreationsToOffer(dev device) []frameReference {
	var unsentGroupCreations []groupCreation

	err := b.database.
		Preload(clause.Associations).
		Select("group_creations.*").
		Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == group_creations.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", dev.Address, typeGroupCreation).
		Joins("JOIN groups ON groups.id = group_creations.id").
		Joins("JOIN group_users ON groups.id = group_users.group_id JOIN users ON group_users.user_id = users.id").
		Where("delivery_records.id IS NULL AND group_users.user_id = ?", dev.UserID).
		Find(&unsentGroupCreations).Error // TODO: only select ID?
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error selecting unsent group creations in reference offer")
	}

	references := []frameReference{}
	for _, gc := range unsentGroupCreations {
		references = append(references, frameReference{FrameID: gc.ID, Type: typeGroupCreation})
	}

	return references
}

func (b *bounce) getUpdateGroupsToOffer(dev device) []frameReference {
	var unsentUpdateGroups []updateGroup

	err := b.database.
		Preload(clause.Associations).
		Select("update_groups.*").
		Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_groups.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", dev.Address, typeUpdateGroup).
		Joins("JOIN group_users ON update_groups.target = group_users.group_id JOIN users ON group_users.user_id = users.id"). // TODO: users join needed?
		Where("delivery_records.id IS NULL AND group_users.user_id = ?", dev.UserID).
		Find(&unsentUpdateGroups).Error // TODO: only select ID?
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error selecting unsent update groups in reference offer")
	}

	references := []frameReference{}
	for _, ug := range unsentUpdateGroups {
		// Ignore sync scoped update groups unless this is a sync device
		if ug.getScope(b.currentUserID()) == scopeSync && !b.isSyncDevice(dev) {
			continue
		}
		references = append(references, frameReference{FrameID: ug.ID, Type: typeUpdateGroup})
	}

	return references
}

func (b *bounce) handleReferenceOffer(peer string, payload []byte) {
	var ro referenceOffer
	err := msgpack.Unmarshal(payload, &ro)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling reference offer")
		return
	}

	dev, deviceExists := b.getDeviceFromAddress(peer)

	// Create a set of references for all of the offered references that we don't have, ACKing the ones we do have in the process
	typesToIDs := referencedIDs(ro.References)
	references := []frameReference{}
	references = append(references, b.getDirectMessagesToRequest(dev, deviceExists, typesToIDs[typeDirectMessage])...)
	references = append(references, b.getGroupMessagesToRequest(dev, deviceExists, typesToIDs[typeGroupMessage])...)
	references = append(references, b.getUpdateDMsToRequest(dev, deviceExists, typesToIDs[typeUpdateDM])...)
	references = append(references, b.getDevicesToRequest(dev, deviceExists, typesToIDs[typeDevice])...)
	references = append(references, b.getAddUsersToRequest(dev, deviceExists, typesToIDs[typeAddUser])...)
	references = append(references, b.getGroupCreationsToRequest(dev, deviceExists, typesToIDs[typeGroupCreation])...)
	references = append(references, b.getUpdateGroupsToRequest(dev, deviceExists, typesToIDs[typeUpdateGroup])...)

	// Inform the reference engine that this peer has these frames that we don't know about
	b.loadReferenceOffer(peer, references)
}

// TODO: experiment with gorm to see if these can be DRY'd:

func (b *bounce) getDirectMessagesToRequest(dev device, deviceExists bool, offeredIDs []uuid.UUID) []frameReference {
	references := []frameReference{}

	for _, dmID := range offeredIDs {
		var dm DirectMessage
		err := b.database.First(&dm, "id = ?", dmID).Error
		if err == nil {
			if deviceExists && !b.isDeliveredTo(&dm, dev.Address) {
				b.markDeliveredTo(&dm, dev.Address)
			}
			go b.sendAck(dev.Address, typeDirectMessage, dmID)
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			references = append(references, frameReference{FrameID: dmID, Type: typeDirectMessage})
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error getting direct message from database")
		}
	}

	return references
}

func (b *bounce) getGroupMessagesToRequest(dev device, deviceExists bool, offeredIDs []uuid.UUID) []frameReference {
	references := []frameReference{}

	for _, gmID := range offeredIDs {
		var gm GroupMessage
		err := b.database.First(&gm, "id = ?", gmID).Error
		if err == nil {
			if deviceExists && !b.isDeliveredTo(&gm, dev.Address) {
				b.markDeliveredTo(&gm, dev.Address)
			}
			go b.sendAck(dev.Address, typeGroupMessage, gm.ID)
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			references = append(references, frameReference{FrameID: gmID, Type: typeGroupMessage})
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error getting group message from database")
		}
	}

	return references
}

func (b *bounce) getUpdateDMsToRequest(dev device, deviceExists bool, offeredIDs []uuid.UUID) []frameReference {
	references := []frameReference{}

	for _, udID := range offeredIDs {
		var ud updateDM
		err := b.database.First(&ud, "id = ?", udID).Error
		if err == nil {
			if deviceExists && !b.isDeliveredTo(&ud, dev.Address) {
				b.markDeliveredTo(&ud, dev.Address)
			}
			go b.sendAck(dev.Address, typeUpdateDM, udID)
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			references = append(references, frameReference{FrameID: udID, Type: typeUpdateDM})
		} else {
			log.WithFields(log.Fields{
				"id":    udID,
				"error": err.Error(),
			}).Fatal("database error querying for update DM settings")
		}
	}

	return references
}

func (b *bounce) getDevicesToRequest(dev device, deviceExists bool, offeredIDs []uuid.UUID) []frameReference {
	references := []frameReference{}

	for _, deviceID := range offeredIDs {
		var existingDev device
		err := b.database.First(&existingDev, "id = ?", deviceID).Error
		if err == nil {
			if deviceExists && !b.isDeliveredTo(&existingDev, dev.Address) {
				b.markDeliveredTo(&existingDev, dev.Address)
			}
			go b.sendAck(dev.Address, typeDevice, deviceID)
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			references = append(references, frameReference{FrameID: deviceID, Type: typeDevice})
		} else {
			log.WithFields(log.Fields{
				"id":    deviceID,
				"error": err.Error(),
			}).Fatal("database error querying for device")
		}
	}

	return references
}

func (b *bounce) getAddUsersToRequest(dev device, deviceExists bool, offeredIDs []uuid.UUID) []frameReference {
	references := []frameReference{}

	for _, addUserID := range offeredIDs {
		var au addUser
		err := b.database.First(&au, "id = ?", addUserID).Error
		if err == nil {
			if deviceExists && !b.isDeliveredTo(&au, dev.Address) {
				b.markDeliveredTo(&au, dev.Address)
			}
			go b.sendAck(dev.Address, typeAddUser, addUserID)
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			references = append(references, frameReference{FrameID: addUserID, Type: typeAddUser})
		} else {
			log.WithFields(log.Fields{
				"id":    addUserID,
				"error": err.Error(),
			}).Fatal("database error querying for add user")
		}
	}

	return references
}

func (b *bounce) getGroupCreationsToRequest(dev device, deviceExists bool, offeredIDs []uuid.UUID) []frameReference {
	references := []frameReference{}

	for _, groupCreationID := range offeredIDs {
		var gc groupCreation
		err := b.database.First(&gc, "id = ?", groupCreationID).Error
		if err == nil {
			if deviceExists && !b.isDeliveredTo(&gc, dev.Address) {
				b.markDeliveredTo(&gc, dev.Address)
			}
			go b.sendAck(dev.Address, typeGroupCreation, groupCreationID)
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			references = append(references, frameReference{FrameID: groupCreationID, Type: typeGroupCreation})
		} else {
			log.WithFields(log.Fields{
				"id":    groupCreationID,
				"error": err.Error(),
			}).Fatal("database error querying for group creation")
		}
	}

	return references
}

func (b *bounce) getUpdateGroupsToRequest(dev device, deviceExists bool, offeredIDs []uuid.UUID) []frameReference {
	references := []frameReference{}

	for _, updateGroupID := range offeredIDs {
		var ug updateGroup
		err := b.database.First(&ug, "id = ?", updateGroupID).Error
		if err == nil {
			if deviceExists && !b.isDeliveredTo(&ug, dev.Address) {
				b.markDeliveredTo(&ug, dev.Address)
			}
			go b.sendAck(dev.Address, typeUpdateGroup, updateGroupID)
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			references = append(references, frameReference{FrameID: updateGroupID, Type: typeUpdateGroup})
		} else {
			log.WithFields(log.Fields{
				"id":    updateGroupID,
				"error": err.Error(),
			}).Fatal("database error querying for update group")
		}
	}

	return references
}
