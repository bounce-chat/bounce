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

var referenceOfferMutexLock sync.Mutex
var referenceOfferMutexes = map[string]*sync.Mutex{}

//
// A reference offer is a message sent to a newly connected device that provides the UUIDs of any frames that device
// should have, but that we didn't deliver to it.  Reference offers are delivery tracked using acks in order to
// support re-send logic, so they do have an ID even though they are only ever sent directly.
//
type referenceOffer struct {
	ID           uuid.UUID
	References   []frameReference
	payload      []byte
	payloadMutex sync.Mutex
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

// Check if anything in the reference offer is not global scope, or justifies dialing a user
func (ro *referenceOffer) shouldDialUser() bool {
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
		} else if reference.Type == typeConfirmation {
			return true
		} else if reference.Type == typeUpdateDevice {
			return true
		}
	}
	return false
}

// Check if a reference offer only contains content destined for a group
func (ro *referenceOffer) onlyGroupContent() bool {
	onlyGroupContent := true

	for _, reference := range ro.References {
		if reference.Type == typeDirectMessage {
			onlyGroupContent = false
		} else if reference.Type == typeDevice {
			onlyGroupContent = false
		} else if reference.Type == typeAddUser {
			onlyGroupContent = false
		} else if reference.Type == typeGroupCreation {
			onlyGroupContent = false
		} else if reference.Type == typeUpdateUser {
			onlyGroupContent = false
		} else if reference.Type == typeUpdateDevice {
			onlyGroupContent = false
		}
	}

	return onlyGroupContent
}

func getReferenceOfferMutexForPeer(peer string) *sync.Mutex {
	referenceOfferMutexLock.Lock()
	defer referenceOfferMutexLock.Unlock()

	mutex, ok := referenceOfferMutexes[peer]
	if !ok {
		mutex = &sync.Mutex{}
		referenceOfferMutexes[peer] = mutex
	}

	return mutex
}

func (b *bounce) sendReferences(peer string) {
	mutex := getReferenceOfferMutexForPeer(peer)
	mutex.Lock()
	defer mutex.Unlock()

	if _, exists := b.currentUser(); !exists {
		// Our profile hasn't been setup yet, we have nothing to offer
		return
	}

	rd := b.getRemoteDevice(peer)
	if rd.connectedSockets < 1 {
		// Can't send references to a device we're not connected to
		return
	}

	_, exists := b.getDeviceFromAddress(peer)
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

	// Reference offers are often sent when connections are interrupted and sockets might be disconnected
	// but not yet reporting errors.  It's important to ensure references are delivered, so we send them
	// until they are ack'd, then delete the delivery record since this isn't a stored frame.
	giveUpTime := time.Now().Add(1 * time.Minute)
	for {
		// Generate a reference offer, and stop here if there's nothing to offer
		referenceOffer := b.getReferenceOfferFor(peer)
		if len(referenceOffer.References) == 0 {
			return
		}

		// Send the offer to the peer
		b.sendDirect(peer, referenceOffer)

		// Wait for an ack before checking if it was delivered
		time.Sleep(10 * time.Second)

		// Manually check the reference database for a delivery record, and delete it and return if found
		var dr deliveryRecord
		err := b.referenceDatabase.Where("destination = ? AND frame_id = ? AND frame_type = ?", peer, referenceOffer.ID, typeReferenceOffer).First(&dr).Error
		if err == nil {
			err = b.referenceDatabase.Where("destination = ? AND frame_id = ? AND frame_type = ?", peer, referenceOffer.ID, typeReferenceOffer).Delete(&deliveryRecord{}).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("error deleting reference offer delivery record")
			}
			return
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error looking up reference offer delivery record")
		}

		// Stop attempting to deliver an offer after a timeout
		if time.Now().After(giveUpTime) {
			log.WithFields(log.Fields{
				"destination": peer,
			}).Warn("gave up attempting to deliver reference offer")
			return
		}
	}
}

func (b *bounce) hasAnyReferencesFor(address string) bool {
	// Make sure we don't generate references while we're in the middle of handling a catch up,
	// in order to not generate incomplete references of update group histories
	catchUpMutex.Lock()
	defer catchUpMutex.Unlock()

	dev, ok := b.getDeviceFromAddress(address)
	if !ok {
		return false
	}

	references := []frameReference{}
	references = append(references, b.getDirectMessagesToOffer(dev)...)
	if len(references) > 0 {
		return true
	}
	references = append(references, b.getGroupMessagesToOffer(dev)...)
	if len(references) > 0 {
		return true
	}
	references = append(references, b.getUpdateDMsToOffer(dev)...)
	if len(references) > 0 {
		return true
	}
	references = append(references, b.getDevicesToOffer(dev)...)
	if len(references) > 0 {
		return true
	}
	references = append(references, b.getAddUsersToOffer(dev)...)
	if len(references) > 0 {
		return true
	}
	references = append(references, b.getGroupCreationsToOffer(dev)...)
	if len(references) > 0 {
		return true
	}
	references = append(references, b.getUpdateGroupsToOffer(dev)...)
	if len(references) > 0 {
		return true
	}
	references = append(references, b.getConfirmationsToOffer(dev)...)
	if len(references) > 0 {
		return true
	}
	references = append(references, b.getUpdateUsersToOffer(dev)...)
	if len(references) > 0 {
		return true
	}
	references = append(references, b.getUpdateDevicesToOffer(dev)...)
	if len(references) > 0 {
		return true
	}

	return false
}

func (b *bounce) getReferenceOfferFor(address string) *referenceOffer {
	// Make sure we don't generate references while we're in the middle of handling a catch up,
	// in order to not generate incomplete references of update group histories
	catchUpMutex.Lock()
	defer catchUpMutex.Unlock()

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
	references = append(references, b.getConfirmationsToOffer(dev)...)
	references = append(references, b.getUpdateUsersToOffer(dev)...)
	references = append(references, b.getUpdateDevicesToOffer(dev)...)

	return &referenceOffer{
		ID:         uuid.New(),
		References: references,
	}
}

func (b *bounce) getDirectMessagesToOffer(dev device) []frameReference {
	if _, revoked := b.devicePool.revokedDevices[dev.Address]; revoked {
		return []frameReference{}
	}

	// All DMs we have sent to or received from this user in the past week
	var dms []directMessage

	if b.isSyncDevice(dev) {
		err := b.database.
			Select("direct_messages.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == direct_messages.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", dev.Address, typeDirectMessage).
			Where("delivery_records.id IS NULL").
			Find(&dms).Error
		if err != nil {
			log.WithFields(log.Fields{
				"peer":  dev.Address,
				"error": err.Error(),
			}).Fatal("error loading DMs for reference offer")
		}
	} else {
		err := b.database.
			Select("direct_messages.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == direct_messages.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", dev.Address, typeDirectMessage).
			Where(
				"delivery_records.id IS NULL AND direct_messages.written_at >= ? AND direct_messages.xor = ?",
				time.Now().Add(-undeliverableAfter).Unix(),
				xor(dev.UserID, b.currentUserID()),
			).
			Find(&dms).Error
		if err != nil {
			log.WithFields(log.Fields{
				"peer":  dev.Address,
				"error": err.Error(),
			}).Fatal("error loading DMs for reference offer")
		}
	}

	// Collect IDs from the DMs
	references := []frameReference{}
	for _, dm := range dms {
		references = append(references, frameReference{FrameID: dm.ID, Type: typeDirectMessage})
	}
	return references
}

func (b *bounce) getGroupMessagesToOffer(dev device) []frameReference {
	if _, revoked := b.devicePool.revokedDevices[dev.Address]; revoked {
		return []frameReference{}
	}

	oldestAllowedMessage := int64(0)
	if !b.isSyncDevice(dev) {
		oldestAllowedMessage = time.Now().Add(-undeliverableAfter).Unix()
	}

	// All group messages that this device's user is a part of that are less than a week old
	var gms []groupMessage
	err := b.database.
		Select("group_messages.*").
		Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == group_messages.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", dev.Address, typeGroupMessage).
		Where(
			"delivery_records.id IS NULL AND group_messages.written_at >= ? AND group_messages.destination IN (?)",
			oldestAllowedMessage,
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

	references := []frameReference{}
	for _, gm := range gms {
		references = append(references, frameReference{FrameID: gm.ID, Type: typeGroupMessage})
	}
	return references
}

func (b *bounce) getUpdateDMsToOffer(dev device) []frameReference {
	if _, revoked := b.devicePool.revokedDevices[dev.Address]; revoked {
		return []frameReference{}
	}

	var unsentUpdateDMs []updateDM

	if b.isSyncDevice(dev) {
		// Select all update DMs from any DM
		err := b.database.
			Select("update_dms.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_dms.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", dev.Address, typeUpdateDM).
			Where("delivery_records.id IS NULL").
			Find(&unsentUpdateDMs).Error
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
			Find(&unsentUpdateDMs).Error
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
	if _, revoked := b.devicePool.revokedDevices[dev.Address]; revoked {
		return []frameReference{}
	}

	var unsentDevices []device

	if b.isSyncDevice(dev) {
		// Sync devices can learn about any device we know about
		err := b.database.
			Select("devices.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == devices.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", dev.Address, typeDevice).
			Where("delivery_records.id IS NULL").
			Find(&unsentDevices).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error selecting devices for reference offer")
		}
	} else {
		// Get any device that belongs to us or them, or any users that share a group with this user
		err := b.database.
			Distinct("devices.id").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == devices.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", dev.Address, typeDevice).
			Where(
				"devices.address != ? AND (devices.user_id = ? OR devices.user_id = ? OR devices.user_id IN (?)) AND delivery_records.id IS NULL",
				dev.Address,
				b.currentUserID(),
				dev.UserID,
				b.database.
					Model(&user{}).
					Distinct().
					Select("users.id").
					Joins("JOIN group_users ON group_users.user_id = users.id").
					Where(
						"group_users.group_id IN (?)",
						b.database.
							Model(&group{}).
							Distinct().
							Select("groups.id").
							Joins("JOIN group_users ON group_users.group_id = groups.id").
							Where("user_id = ?", dev.UserID),
					),
			).
			Find(&unsentDevices).Error
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
	if _, revoked := b.devicePool.revokedDevices[dev.Address]; revoked {
		return []frameReference{}
	}

	var unsentAddUsers []addUser

	if b.isSyncDevice(dev) {
		err := b.database.
			Select("add_users.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == add_users.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", dev.Address, typeAddUser).
			Where("delivery_records.id IS NULL").
			Find(&unsentAddUsers).Error
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
			Find(&unsentAddUsers).Error
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
	if _, revoked := b.devicePool.revokedDevices[dev.Address]; revoked {
		return []frameReference{}
	}

	var unsentGroupCreations []groupCreation

	err := b.database.
		Preload(clause.Associations).
		Select("group_creations.*").
		Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == group_creations.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", dev.Address, typeGroupCreation).
		Joins("JOIN groups ON groups.id = group_creations.id").
		Joins("JOIN group_users ON groups.id = group_users.group_id JOIN users ON group_users.user_id = users.id").
		Where("delivery_records.id IS NULL AND group_users.user_id = ?", dev.UserID).
		Find(&unsentGroupCreations).Error
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
	if _, revoked := b.devicePool.revokedDevices[dev.Address]; revoked {
		return []frameReference{}
	}

	var unsentUpdateGroups []updateGroup

	if b.isSyncDevice(dev) {
		err := b.database.
			Preload(clause.Associations).
			Select("update_groups.id").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_groups.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", dev.Address, typeUpdateGroup).
			Where("delivery_records.id IS NULL").
			Find(&unsentUpdateGroups).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error selecting unsent update groups in reference offer")
		}
	} else {
		// Find all update groups for groups this user is in
		err := b.database.
			Preload(clause.Associations).
			Select("update_groups.id").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_groups.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", dev.Address, typeUpdateGroup).
			Joins("LEFT JOIN group_users ON update_groups.target = group_users.group_id").
			Where(
				"delivery_records.id IS NULL AND ((group_users.user_id = ? AND update_groups.custom_scope == ?) OR update_groups.custom_scope IN (?))",
				dev.UserID,
				uuid.Nil,
				b.database.
					Model(&customScope{}).
					Distinct().
					Select("id").
					Where("addresses LIKE ?", "%"+dev.Address+"%"),
			).
			Find(&unsentUpdateGroups).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error selecting unsent update groups in reference offer")
		}

		// Find all the groups this user has ever left or been kicked from
		groupsUserLeft := []uuid.UUID{}
		err = b.database.
			Model(&updateGroup{}).
			Distinct().
			Select("target").
			Where(
				"type = ? AND data = ?",
				updateGroupTypeRemoveUser,
				dev.UserID[:],
			).
			Find(&groupsUserLeft).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error finding groups a user has left")

		}
		for _, groupID := range groupsUserLeft {
			// Make sure they were not re-added to the group
			if b.userIsInGroup(groupID, dev.UserID) {
				continue
			}

			// Find the timestamp of their latest departure
			var lastRemoval int64
			row := b.database.Table("update_groups").Where("type = ? AND data = ?", updateGroupTypeRemoveUser, dev.UserID[:]).Select("MAX(timestamp)").Row()
			err := row.Scan(&lastRemoval)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("error parsing timestamp of users last departure from group")
			}

			// Find all the undelivered update groups up until their last departure
			var ugs []updateGroup
			err = b.database.
				Select("update_groups.id").
				Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_groups.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", dev.Address, typeUpdateGroup).
				Where(
					"delivery_records.id IS NULL AND update_groups.target = ? AND update_groups.timestamp <= ?",
					groupID,
					lastRemoval,
				).Find(&ugs).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("error getting update groups for departed user")
			}

			// Include these update groups in the reference offer
			for _, ug := range ugs {
				unsentUpdateGroups = append(unsentUpdateGroups, ug)
			}
		}

		// Find all the groups this user has blocked, where we haven't delivered the block to this device
		var ugs []updateGroup
		err = b.database.
			Select("update_groups.id").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_groups.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", dev.Address, typeUpdateGroup).
			Where(
				"delivery_records.id IS NULL AND update_groups.actor = ? AND update_groups.type = ?",
				dev.UserID,
				updateGroupTypeBlock,
			).Find(&ugs).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error getting update group blocks for user")
		}
		for _, ug := range ugs {
			unsentUpdateGroups = append(unsentUpdateGroups, ug)
		}
	}

	// Collect all these update groups into a set of references
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

func (b *bounce) getConfirmationsToOffer(dev device) []frameReference {
	if _, revoked := b.devicePool.revokedDevices[dev.Address]; revoked {
		return []frameReference{}
	}

	var unsentConfirmations []confirmation
	err := b.database.
		Preload(clause.Associations).
		Select("confirmations.*").
		Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == confirmations.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", dev.Address, typeConfirmation).
		Joins("JOIN update_groups ON update_groups.id == confirmations.update_group_id").
		Joins("LEFT JOIN group_users ON update_groups.target == group_users.group_id").
		Where(
			"delivery_records.id IS NULL AND ((group_users.user_id = ? AND confirmations.custom_scope == ?) OR confirmations.custom_scope IN (?))",
			dev.UserID,
			uuid.Nil,
			b.database.
				Model(&customScope{}).
				Distinct().
				Select("id").
				Where("addresses LIKE ?", "%"+dev.Address+"%"),
		).
		Find(&unsentConfirmations).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error selecting unsent confirmations in reference offer")
	}

	references := []frameReference{}
	for _, c := range unsentConfirmations {
		references = append(references, frameReference{FrameID: c.ID, Type: typeConfirmation})
	}

	return references
}

func (b *bounce) getUpdateUsersToOffer(dev device) []frameReference {
	if _, revoked := b.devicePool.revokedDevices[dev.Address]; revoked {
		return []frameReference{}
	}

	var unsentUpdateUsers []updateUser

	if b.isSyncDevice(dev) {
		err := b.database.
			Distinct("update_users.id").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_users.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", dev.Address, typeUpdateUser).
			Where("delivery_records.id IS NULL").
			Find(&unsentUpdateUsers).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error selecting unsent update users in reference offer")
		}
	} else {
		err := b.database.
			Distinct("update_users.id").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_users.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", dev.Address, typeUpdateUser).
			Where(
				"(update_users.target = ? OR update_users.target = ? OR update_users.target IN (?)) AND delivery_records.id IS NULL",
				dev.UserID,
				b.currentUserID(),
				b.database.
					Model(&user{}).
					Distinct().
					Select("users.id").
					Joins("JOIN group_users ON group_users.user_id = users.id").
					Where(
						"group_users.group_id IN (?)",
						b.database.
							Model(&group{}).
							Distinct().
							Select("groups.id").
							Joins("JOIN group_users ON group_users.group_id = groups.id").
							Where("user_id = ?", dev.UserID),
					),
			).
			Find(&unsentUpdateUsers).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error selecting unsent update devices in reference offer")
		}
	}

	references := []frameReference{}
	for _, uu := range unsentUpdateUsers {
		references = append(references, frameReference{FrameID: uu.ID, Type: typeUpdateUser})
	}

	return references
}

func (b *bounce) getUpdateDevicesToOffer(dev device) []frameReference {
	if _, revoked := b.devicePool.revokedDevices[dev.Address]; revoked {
		var revoke updateDevice
		err := b.database.Where("target = ? AND type = ?", dev.ID, updateDeviceTypeRevoke).First(&revoke).Error
		if err != nil {
			log.WithFields(log.Fields{
				"peer":  dev.Address,
				"error": err.Error(),
			}).Error("error finding revoke update for revoked device")
			return []frameReference{}
		}
		return []frameReference{frameReference{
			FrameID: revoke.ID,
			Type:    typeUpdateDevice,
		}}
	}

	var unsentUpdateDevices []updateDevice

	if b.isSyncDevice(dev) {
		err := b.database.
			Distinct("update_devices.id").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_devices.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", dev.Address, typeUpdateDevice).
			Where("delivery_records.id IS NULL").
			Find(&unsentUpdateDevices).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error selecting unsent update devices in reference offer")
		}
	} else {
		// Only get updates that revoke a device, using overlap scope
		err := b.database.
			Distinct("update_devices.id").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_devices.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", dev.Address, typeUpdateDevice).
			Where(
				"update_devices.type = ? AND (update_devices.author = ? OR update_devices.author = ? OR update_devices.author IN (?)) AND delivery_records.id IS NULL",
				updateDeviceTypeRevoke,
				dev.UserID,
				b.currentUserID(),
				b.database.
					Model(&user{}).
					Distinct().
					Select("users.id").
					Joins("JOIN group_users ON group_users.user_id = users.id").
					Where(
						"group_users.group_id IN (?)",
						b.database.
							Model(&group{}).
							Distinct().
							Select("groups.id").
							Joins("JOIN group_users ON group_users.group_id = groups.id").
							Where("user_id = ?", dev.UserID),
					),
			).
			Find(&unsentUpdateDevices).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error selecting unsent update devices in reference offer")
		}
	}

	references := []frameReference{}
	for _, ud := range unsentUpdateDevices {
		references = append(references, frameReference{FrameID: ud.ID, Type: typeUpdateDevice})
	}

	return references
}

func (b *bounce) handleReferenceOffer(peer string, payload []byte, catchUp bool) broadcastable {
	if _, revoked := b.devicePool.revokedDevices[peer]; revoked {
		return nil
	}

	// Unpack the offer
	var ro referenceOffer
	err := msgpack.Unmarshal(payload, &ro)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling reference offer")
		return nil
	}

	dev, deviceExists := b.getDeviceFromAddress(peer)

	go b.sendAck(peer, typeReferenceOffer, ro.ID)

	// Make sure we aren't still handling a catch up while determining what we need to request
	catchUpMutex.Lock()

	// Create a set of references for all of the offered references that we don't have, ACKing the ones we do have in the process
	typesToIDs := referencedIDs(ro.References)
	references := []frameReference{}
	acks := []frameReference{}

	dmRefs, dmAcks := b.getDirectMessagesToRequestAndAck(dev, deviceExists, typesToIDs[typeDirectMessage])
	references = append(references, dmRefs...)
	acks = append(acks, dmAcks...)

	gmRefs, gmAcks := b.getGroupMessagesToRequestAndAck(dev, deviceExists, typesToIDs[typeGroupMessage])
	references = append(references, gmRefs...)
	acks = append(acks, gmAcks...)

	udmRefs, udmAcks := b.getUpdateDMsToRequestAndAck(dev, deviceExists, typesToIDs[typeUpdateDM])
	references = append(references, udmRefs...)
	acks = append(acks, udmAcks...)

	dRefs, dAcks := b.getDevicesToRequestAndAck(dev, deviceExists, typesToIDs[typeDevice])
	references = append(references, dRefs...)
	acks = append(acks, dAcks...)

	auRefs, auAcks := b.getAddUsersToRequestAndAck(dev, deviceExists, typesToIDs[typeAddUser])
	references = append(references, auRefs...)
	acks = append(acks, auAcks...)

	gcRefs, gcAcks := b.getGroupCreationsToRequestAndAck(dev, deviceExists, typesToIDs[typeGroupCreation])
	references = append(references, gcRefs...)
	acks = append(acks, gcAcks...)

	ugRefs, ugAcks := b.getUpdateGroupsToRequestAndAck(dev, deviceExists, typesToIDs[typeUpdateGroup])
	references = append(references, ugRefs...)
	acks = append(acks, ugAcks...)

	cRefs, cAcks := b.getConfirmationsToRequestAndAck(dev, deviceExists, typesToIDs[typeConfirmation])
	references = append(references, cRefs...)
	acks = append(acks, cAcks...)

	uuRefs, uuAcks := b.getUpdateUsersToRequestAndAck(dev, deviceExists, typesToIDs[typeUpdateUser])
	references = append(references, uuRefs...)
	acks = append(acks, uuAcks...)

	udRefs, udAcks := b.getUpdateDevicesToRequestAndAck(dev, deviceExists, typesToIDs[typeUpdateDevice])
	references = append(references, udRefs...)
	acks = append(acks, udAcks...)

	// Unlock the catch up mutex
	catchUpMutex.Unlock()

	// Inform the reference engine that this peer has these frames that we don't know about
	b.loadReferenceOffer(peer, references)

	// Send an ack for everything we already have in the database
	go b.sendDirect(peer, &ack{
		References: acks,
	})

	return nil
}

func (b *bounce) getDirectMessagesToRequestAndAck(dev device, deviceExists bool, offeredIDs []uuid.UUID) ([]frameReference, []frameReference) {
	references := []frameReference{}
	acks := []frameReference{}

	for _, dmID := range offeredIDs {
		ref := frameReference{FrameID: dmID, Type: typeDirectMessage}
		var dm directMessage
		err := b.database.First(&dm, "id = ?", dmID).Error
		if err == nil {
			if deviceExists && !b.isDeliveredTo(&dm, dev.Address) {
				b.markDeliveredTo(&dm, dev.Address)
			}
			acks = append(acks, ref)
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			references = append(references, ref)
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error getting direct message from database")
		}
	}

	return references, acks
}

func (b *bounce) getGroupMessagesToRequestAndAck(dev device, deviceExists bool, offeredIDs []uuid.UUID) ([]frameReference, []frameReference) {
	references := []frameReference{}
	acks := []frameReference{}

	for _, gmID := range offeredIDs {
		ref := frameReference{FrameID: gmID, Type: typeGroupMessage}
		var gm groupMessage
		err := b.database.First(&gm, "id = ?", gmID).Error
		if err == nil {
			if deviceExists && !b.isDeliveredTo(&gm, dev.Address) {
				b.markDeliveredTo(&gm, dev.Address)
			}
			acks = append(acks, ref)
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			references = append(references, ref)
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error getting group message from database")
		}
	}

	return references, acks
}

func (b *bounce) getUpdateDMsToRequestAndAck(dev device, deviceExists bool, offeredIDs []uuid.UUID) ([]frameReference, []frameReference) {
	references := []frameReference{}
	acks := []frameReference{}

	for _, udID := range offeredIDs {
		ref := frameReference{FrameID: udID, Type: typeUpdateDM}
		var ud updateDM
		err := b.database.First(&ud, "id = ?", udID).Error
		if err == nil {
			if deviceExists && !b.isDeliveredTo(&ud, dev.Address) {
				b.markDeliveredTo(&ud, dev.Address)
			}
			acks = append(acks, ref)
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			references = append(references, ref)
		} else {
			log.WithFields(log.Fields{
				"id":    udID,
				"error": err.Error(),
			}).Fatal("database error querying for update DM settings")
		}
	}

	return references, acks
}

func (b *bounce) getDevicesToRequestAndAck(dev device, deviceExists bool, offeredIDs []uuid.UUID) ([]frameReference, []frameReference) {
	references := []frameReference{}
	acks := []frameReference{}

	for _, deviceID := range offeredIDs {
		ref := frameReference{FrameID: deviceID, Type: typeDevice}
		var existingDev device
		err := b.database.First(&existingDev, "id = ?", deviceID).Error
		if err == nil {
			if deviceExists && !b.isDeliveredTo(&existingDev, dev.Address) {
				b.markDeliveredTo(&existingDev, dev.Address)
			}
			acks = append(acks, ref)
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			references = append(references, ref)
		} else {
			log.WithFields(log.Fields{
				"id":    deviceID,
				"error": err.Error(),
			}).Fatal("database error querying for device")
		}
	}

	return references, acks
}

func (b *bounce) getAddUsersToRequestAndAck(dev device, deviceExists bool, offeredIDs []uuid.UUID) ([]frameReference, []frameReference) {
	references := []frameReference{}
	acks := []frameReference{}

	for _, addUserID := range offeredIDs {
		ref := frameReference{FrameID: addUserID, Type: typeAddUser}
		var au addUser
		err := b.database.First(&au, "id = ?", addUserID).Error
		if err == nil {
			if deviceExists && !b.isDeliveredTo(&au, dev.Address) {
				b.markDeliveredTo(&au, dev.Address)
			}
			acks = append(acks, ref)
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			references = append(references, ref)
		} else {
			log.WithFields(log.Fields{
				"id":    addUserID,
				"error": err.Error(),
			}).Fatal("database error querying for add user")
		}
	}

	return references, acks
}

func (b *bounce) getGroupCreationsToRequestAndAck(dev device, deviceExists bool, offeredIDs []uuid.UUID) ([]frameReference, []frameReference) {
	references := []frameReference{}
	acks := []frameReference{}

	for _, groupCreationID := range offeredIDs {
		ref := frameReference{FrameID: groupCreationID, Type: typeGroupCreation}
		var gc groupCreation
		err := b.database.First(&gc, "id = ?", groupCreationID).Error
		if err == nil {
			if deviceExists && !b.isDeliveredTo(&gc, dev.Address) {
				b.markDeliveredTo(&gc, dev.Address)
			}
			acks = append(acks, ref)
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			references = append(references, ref)
		} else {
			log.WithFields(log.Fields{
				"id":    groupCreationID,
				"error": err.Error(),
			}).Fatal("database error querying for group creation")
		}
	}

	return references, acks
}

func (b *bounce) getUpdateGroupsToRequestAndAck(dev device, deviceExists bool, offeredIDs []uuid.UUID) ([]frameReference, []frameReference) {
	references := []frameReference{}
	acks := []frameReference{}

	for _, updateGroupID := range offeredIDs {
		ref := frameReference{FrameID: updateGroupID, Type: typeUpdateGroup}
		var ug updateGroup
		err := b.database.First(&ug, "id = ?", updateGroupID).Error
		if err == nil {
			if deviceExists && !b.isDeliveredTo(&ug, dev.Address) {
				b.markDeliveredTo(&ug, dev.Address)
			}
			acks = append(acks, ref)
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			references = append(references, ref)
		} else {
			log.WithFields(log.Fields{
				"id":    updateGroupID,
				"error": err.Error(),
			}).Fatal("database error querying for update group")
		}
	}

	return references, acks
}

func (b *bounce) getConfirmationsToRequestAndAck(dev device, deviceExists bool, offeredIDs []uuid.UUID) ([]frameReference, []frameReference) {
	references := []frameReference{}
	acks := []frameReference{}

	for _, confirmationID := range offeredIDs {
		ref := frameReference{FrameID: confirmationID, Type: typeConfirmation}
		var c confirmation
		err := b.database.First(&c, "id = ?", confirmationID).Error
		if err == nil {
			if deviceExists && !b.isDeliveredTo(&c, dev.Address) {
				b.markDeliveredTo(&c, dev.Address)
			}
			acks = append(acks, ref)
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			references = append(references, ref)
		} else {
			log.WithFields(log.Fields{
				"id":    confirmationID,
				"error": err.Error(),
			}).Fatal("database error querying for confirmation")
		}
	}

	return references, acks
}

func (b *bounce) getUpdateUsersToRequestAndAck(dev device, deviceExists bool, offeredIDs []uuid.UUID) ([]frameReference, []frameReference) {
	references := []frameReference{}
	acks := []frameReference{}

	for _, updateUserID := range offeredIDs {
		ref := frameReference{FrameID: updateUserID, Type: typeUpdateUser}
		var uu updateUser
		err := b.database.First(&uu, "id = ?", updateUserID).Error
		if err == nil {
			if deviceExists && !b.isDeliveredTo(&uu, dev.Address) {
				b.markDeliveredTo(&uu, dev.Address)
			}
			acks = append(acks, ref)
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			references = append(references, ref)
		} else {
			log.WithFields(log.Fields{
				"id":    updateUserID,
				"error": err.Error(),
			}).Fatal("database error querying for update user")
		}
	}

	return references, acks
}

func (b *bounce) getUpdateDevicesToRequestAndAck(dev device, deviceExists bool, offeredIDs []uuid.UUID) ([]frameReference, []frameReference) {
	references := []frameReference{}
	acks := []frameReference{}

	for _, updateDeviceID := range offeredIDs {
		ref := frameReference{FrameID: updateDeviceID, Type: typeUpdateDevice}
		var ud updateDevice
		err := b.database.First(&ud, "id = ?", updateDeviceID).Error
		if err == nil {
			if deviceExists && !b.isDeliveredTo(&ud, dev.Address) {
				b.markDeliveredTo(&ud, dev.Address)
			}
			acks = append(acks, ref)
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			references = append(references, ref)
		} else {
			log.WithFields(log.Fields{
				"id":    updateDeviceID,
				"error": err.Error(),
			}).Fatal("database error querying for update device")
		}
	}

	return references, acks
}
