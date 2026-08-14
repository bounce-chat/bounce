package chat

import (
	"errors"
	"sync"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var referenceOfferMutexLock sync.Mutex
var referenceOfferMutexes = map[string]*sync.Mutex{}

// A reference offer is a message sent to a newly connected device that provides the UUIDs of any frames that device
// should have, but that we didn't deliver to it.  Reference offers are delivery tracked using acks in order to
// support re-send logic, so they do have an ID even though they are only ever sent directly.
type referenceOffer struct {
	ID         uuid.UUID
	References []frameReference
}

func (ro *referenceOffer) getType() uint16 {
	return typeReferenceOffer
}

func (ro *referenceOffer) getPayload() []byte {
	bytes, err := msgpack.Marshal(ro)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal reference offer")
	}
	return bytes
}

// Check if anything in the reference offer is not global scope, or justifies dialing a user
func (ro *referenceOffer) shouldDialUser() bool {
	for _, reference := range ro.References {
		if spec, ok := specsByType[reference.Type]; ok && spec.dialWorthy {
			return true
		}
	}
	return false
}

// Check if a reference offer only contains content destined for a group
func (ro *referenceOffer) onlyGroupContent() bool {
	for _, reference := range ro.References {
		if !(reference.Type == typeGroupMessage || reference.Type == typeUpdateGroup) {
			return false
		}
	}

	return true
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

func (b *Bounce) sendReferences(peer string) {
	mutex := getReferenceOfferMutexForPeer(peer)
	mutex.Lock()
	defer mutex.Unlock()

	if _, exists := b.currentUser(); !exists {
		// Our profile hasn't been setup yet, we have nothing to offer
		return
	}

	dev, exists := b.getDeviceFromAddress(peer)
	encryptedDeviceCacheMutex.Lock()
	_, encrypted := encryptedDeviceCache[peer]
	encryptedDeviceCacheMutex.Unlock()
	if !exists && !encrypted {
		// We have nothing to offer a device that we don't know about
		return
	}

	if exists && blockedUser(dev.UserID) {
		// Reject any reference offer from a block user's device
		return
	}

	_, exists = b.getDeviceFromAddress(b.network.Address())
	if !exists {
		// Our own devices doesn't exist yet, we're probably going to be added as a new sync
		// device now.  In the meantime there's nothing to do.
		return
	}

	rd := b.getRemoteDevice(peer)
	if rd.connectedSockets() < 1 {
		// Can't send references to a device we're not connected to
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
		time.Sleep(time.Duration(referenceRetrySeconds) * time.Second)

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

func (b *Bounce) hasAnyReferencesFor(address string) bool {
	// Make sure we don't generate references while we're in the middle of handling a catch up,
	// in order to not generate incomplete references of update group histories
	catchUpMutex.Lock()
	defer catchUpMutex.Unlock()

	ro := b.getReferenceOfferFor(address)
	return len(ro.References) > 0
}

func (b *Bounce) getReferenceOfferFor(address string) *referenceOffer {
	// Make sure we don't generate references while we're in the middle of handling a catch up,
	// in order to not generate incomplete references of update group histories
	catchUpMutex.Lock()
	defer catchUpMutex.Unlock()

	if b.encrypted {
		log.Fatal("cannot generate reference offer from encrypted device")
	}

	var userID uuid.UUID
	dev, ok := b.getDeviceFromAddress(address)
	if ok {
		userID = dev.UserID
	} else {
		encryptedDeviceCacheMutex.Lock()
		userID, ok = encryptedDeviceCache[address]
		encryptedDeviceCacheMutex.Unlock()
		if !ok {
			log.WithFields(log.Fields{
				"address": address,
			}).Warn("cannot generate reference offer for unknown device")
			return &referenceOffer{}
		}
	}

	references := []frameReference{}
	for _, spec := range syncableSpecs {
		references = append(references, spec.offer(b, address, userID)...)
	}

	return &referenceOffer{
		ID:         uuid.New(),
		References: references,
	}
}

func (b *Bounce) getEncryptedReferenceOfferFor(address string, publicKey []byte) *referenceOffer {
	// Frame IDs this public key can have
	var allowedPubkeyIDs []string
	b.database.Model(&recipient{}).
		Where("public_key = ?", publicKey).
		Pluck("encrypted_frame_id", &allowedPubkeyIDs)

	// Frmae IDs this device can have
	var allowedDeviceIDs []string
	b.database.Model(&deviceRecipient{}).
		Where("recipient_address = ?", address).
		Pluck("encrypted_frame_id", &allowedDeviceIDs)

	allowedIDs := append(allowedPubkeyIDs, allowedDeviceIDs...)

	// Frame IDs we have delivered
	var deliveredIDs []string
	b.database.Model(&deliveryRecord{}).
		Where("destination = ?", address).
		Pluck("frame_id", &deliveredIDs)
	deliveredMap := map[string]bool{}
	for _, id := range deliveredIDs {
		deliveredMap[id] = true
	}

	offerIDs := []string{}
	for _, id := range allowedIDs {
		if _, present := deliveredMap[id]; !present {
			offerIDs = append(offerIDs, id)
		}
	}

	var encryptedFrames []encryptedFrame
	b.database.
		Select("id", "type").
		Where("encrypted_frames.id IN (?)", offerIDs).
		Find(&encryptedFrames)

	references := []frameReference{}
	for _, ef := range encryptedFrames {
		references = append(references, frameReference{FrameID: ef.ID, Type: ef.Type})
	}

	return &referenceOffer{
		ID:         uuid.New(),
		References: references,
	}
}

func (b *Bounce) getDirectMessagesToOffer(address string, userID uuid.UUID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	// All DMs we have sent to or received from this user in the past week
	var dms []directMessage
	if userID == b.currentUserID() {
		err := b.database.
			Select("direct_messages.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == direct_messages.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeDirectMessage).
			Where("delivery_records.id IS NULL").
			Find(&dms).Error
		if err != nil {
			log.WithFields(log.Fields{
				"peer":  address,
				"error": err.Error(),
			}).Fatal("error loading DMs for reference offer")
		}
	} else {
		err := b.database.
			Select("direct_messages.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == direct_messages.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeDirectMessage).
			Where(
				"delivery_records.id IS NULL AND direct_messages.written_at >= ? AND direct_messages.xor = ?",
				time.Now().Add(-undeliverableAfter).Unix(),
				xor(userID, b.currentUserID()),
			).
			Find(&dms).Error
		if err != nil {
			log.WithFields(log.Fields{
				"peer":  address,
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

func (b *Bounce) getGroupMessagesToOffer(address string, userID uuid.UUID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	oldestAllowedMessage := int64(0)
	if userID != b.currentUserID() {
		oldestAllowedMessage = time.Now().Add(-undeliverableAfter).Unix()
	}

	// All group messages that this device's user is a part of that are less than a week old
	var gms []groupMessage
	err := b.database.
		Select("group_messages.*").
		Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == group_messages.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeGroupMessage).
		Where(
			"delivery_records.id IS NULL AND group_messages.written_at >= ? AND group_messages.destination IN (?)",
			oldestAllowedMessage,
			b.database.
				Model(&group{}).
				Distinct().
				Select("groups.id").
				Joins("JOIN group_users ON groups.id = group_users.group_id").
				Where("group_users.user_id = ?", userID),
		).
		Find(&gms).Error
	if err != nil {
		log.WithFields(log.Fields{
			"peer":  address,
			"error": err.Error(),
		}).Fatal("error loading DMs for reference offer")
	}

	references := []frameReference{}
	for _, gm := range gms {
		references = append(references, frameReference{FrameID: gm.ID, Type: typeGroupMessage})
	}
	return references
}

func (b *Bounce) getUpdateDMsToOffer(address string, userID uuid.UUID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	var unsentUpdateDMs []updateDM

	if userID == b.currentUserID() {
		// Select all update DMs from any DM
		err := b.database.
			Select("update_dms.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_dms.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeUpdateDM).
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
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_dms.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeUpdateDM).
			Where("update_dms.target = ? AND delivery_records.id IS NULL", xor(userID, b.currentUserID())).
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
		if ud.getScope(b.currentUserID()) == scopeSync && userID != b.currentUserID() {
			continue
		}
		references = append(references, frameReference{FrameID: ud.ID, Type: typeUpdateDM})
	}

	return references
}

func (b *Bounce) getDevicesToOffer(address string, userID uuid.UUID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	var unsentDevices []device

	if userID == b.currentUserID() {
		// Sync devices can learn about any device we know about
		err := b.database.
			Select("devices.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == devices.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeDevice).
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
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == devices.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeDevice).
			Where(
				"devices.address != ? AND (devices.user_id = ? OR devices.user_id = ? OR devices.user_id IN (?)) AND delivery_records.id IS NULL",
				address,
				b.currentUserID(),
				userID,
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
							Where("user_id = ?", userID),
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

func (b *Bounce) getAddUsersToOffer(address string, userID uuid.UUID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	var unsentAddUsers []addUser

	if userID == b.currentUserID() {
		err := b.database.
			Select("add_users.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == add_users.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeAddUser).
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
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == add_users.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeAddUser).
			Where("delivery_records.id IS NULL AND add_users.xor = ?", xor(b.currentUserID(), userID)).
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

func (b *Bounce) getGroupCreationsToOffer(address string, userID uuid.UUID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	var unsentGroupCreations []groupCreation

	err := b.database.
		Preload(clause.Associations).
		Distinct().
		Select("group_creations.*").
		Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == group_creations.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeGroupCreation).
		Joins("JOIN groups ON groups.id = group_creations.id").
		Joins("JOIN group_users ON groups.id = group_users.group_id JOIN users ON group_users.user_id = users.id").
		Where("delivery_records.id IS NULL AND (group_users.user_id = ? OR groups.invites LIKE ?)", userID, "%"+userID.String()+"%").
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

func (b *Bounce) getUpdateGroupsToOffer(address string, userID uuid.UUID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	var unsentUpdateGroups []updateGroup

	if userID == b.currentUserID() {
		err := b.database.
			Preload(clause.Associations).
			Select("update_groups.id").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_groups.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeUpdateGroup).
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
			Distinct().
			Select("update_groups.id").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_groups.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeUpdateGroup).
			Joins("LEFT JOIN groups ON groups.id = update_groups.target").
			Joins("LEFT JOIN group_users ON update_groups.target = group_users.group_id").
			Where(
				"delivery_records.id IS NULL AND (((group_users.user_id = ? OR groups.invites LIKE ?) AND update_groups.custom_scope == ?) OR update_groups.custom_scope IN (?))",
				userID,
				"%"+userID.String()+"%",
				uuid.Nil,
				b.database.
					Model(&customScope{}).
					Distinct().
					Select("id").
					Where("addresses LIKE ?", "%"+address+"%"),
			).
			Find(&unsentUpdateGroups).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error selecting unsent update groups in reference offer")
		}

		// Find all the groups this user has ever left, rejected, or been kicked from
		groupsUserLeft := []uuid.UUID{}
		err = b.database.
			Model(&updateGroup{}).
			Distinct().
			Select("target").
			Where(
				"((type = ? OR type = ?) AND data = ?) OR (type = ? AND data = ? AND actor = ?)",
				updateGroupTypeRemoveUser,
				updateGroupTypeRevokeInvite,
				userID[:],
				updateGroupTypeRespondToInvite,
				[]byte{rejectInvite},
				userID,
			).
			Find(&groupsUserLeft).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error finding groups a user has left")

		}
		for _, groupID := range groupsUserLeft {
			// Make sure they were not re-added to the group
			if b.userIsInGroup(groupID, userID) || b.isInvited(groupID, userID) {
				continue
			}

			// Find the timestamp of their latest departure
			var lastRemoval int64
			row := b.database.
				Table("update_groups").
				Where(
					"((type = ? OR type = ?) AND data = ?) OR (type = ? AND data = ? AND actor = ?)",
					updateGroupTypeRemoveUser,
					updateGroupTypeRevokeInvite,
					userID[:],
					updateGroupTypeRespondToInvite,
					[]byte{rejectInvite},
					userID,
				).
				Select("MAX(timestamp)").
				Row()
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
				Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_groups.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeUpdateGroup).
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
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_groups.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeUpdateGroup).
			Where(
				"delivery_records.id IS NULL AND update_groups.actor = ? AND update_groups.type = ?",
				userID,
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
	included := map[uuid.UUID]bool{}
	for _, ug := range unsentUpdateGroups {
		if _, ok := included[ug.ID]; ok {
			continue
		} else {
			included[ug.ID] = true
		}
		// Ignore sync scoped update groups unless this is a sync device
		if ug.getScope(b.currentUserID()) == scopeSync && userID != b.currentUserID() {
			continue
		}
		references = append(references, frameReference{FrameID: ug.ID, Type: typeUpdateGroup})
	}

	return references
}

func (b *Bounce) getConfirmationsToOffer(address string, userID uuid.UUID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	var unsentConfirmations []confirmation
	err := b.database.
		Preload(clause.Associations).
		Select("confirmations.*").
		Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == confirmations.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeConfirmation).
		Joins("JOIN update_groups ON update_groups.id == confirmations.update_group_id").
		Joins("LEFT JOIN group_users ON update_groups.target == group_users.group_id").
		Where(
			"delivery_records.id IS NULL AND ((group_users.user_id = ? AND confirmations.custom_scope == ?) OR confirmations.custom_scope IN (?))",
			userID,
			uuid.Nil,
			b.database.
				Model(&customScope{}).
				Distinct().
				Select("id").
				Where("addresses LIKE ?", "%"+address+"%"),
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

func (b *Bounce) getUpdateUsersToOffer(address string, userID uuid.UUID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	var unsentUpdateUsers []updateUser

	if userID == b.currentUserID() {
		err := b.database.
			Distinct("update_users.id").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_users.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeUpdateUser).
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
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_users.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeUpdateUser).
			Where(
				"update_users.type != ? AND (update_users.target = ? OR update_users.target = ? OR update_users.target IN (?)) AND delivery_records.id IS NULL",
				updateUserTypeSetEncryptedDeviceName,
				userID,
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
							Where("user_id = ?", userID),
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

func (b *Bounce) getUpdateDevicesToOffer(address string, userID uuid.UUID) []frameReference {
	if b.devicePool.isRevoked(address) {
		dev, ok := b.getDeviceFromAddress(address)
		if ok {
			var revoke updateDevice
			err := b.database.Where("target = ? AND type = ?", dev.ID, updateDeviceTypeRevoke).First(&revoke).Error
			if err != nil {
				log.WithFields(log.Fields{
					"peer":  address,
					"error": err.Error(),
				}).Error("error finding revoke update for revoked device")
				return []frameReference{}
			}
			return []frameReference{frameReference{
				FrameID: revoke.ID,
				Type:    typeUpdateDevice,
			}}
		}
	}

	var unsentUpdateDevices []updateDevice

	if userID == b.currentUserID() {
		err := b.database.
			Distinct("update_devices.id").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_devices.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeUpdateDevice).
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
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_devices.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeUpdateDevice).
			Where(
				"update_devices.type = ? AND (update_devices.author = ? OR update_devices.author = ? OR update_devices.author IN (?)) AND delivery_records.id IS NULL",
				updateDeviceTypeRevoke,
				userID,
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
							Where("user_id = ?", userID),
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

func (b *Bounce) getReadReceiptsToOffer(address string, userID uuid.UUID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	var unsentReadReceipts []readReceipt

	if userID == b.currentUserID() {
		err := b.database.
			Distinct("read_receipts.id").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == read_receipts.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeReadReceipt).
			Where("delivery_records.id IS NULL").
			Find(&unsentReadReceipts).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error selecting unsent read receipts in reference offer")
		}
	} else {
		// read receipts where
		//	target type is direct message and destination is this device's user
		//	target type is group message and destination is any group this user is in
		//	either way, scope is not sync
		err := b.database.
			Distinct("read_receipts.id").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == read_receipts.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeReadReceipt).
			Where(
				"((read_receipts.target_type == ? AND read_receipts.destination = ?) OR (read_receipts.target_type = ? AND read_receipts.destination IN (?))) AND read_receipts.scope != ? AND delivery_records.id IS NULL",
				typeDirectMessage,
				userID,
				typeGroupMessage,
				b.database.
					Model(&group{}).
					Distinct().
					Select("groups.id").
					Joins("JOIN group_users ON group_users.group_id = groups.id").
					Where("user_id = ?", userID),
				scopeSync,
			).
			Find(&unsentReadReceipts).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error selecting unsent update devices in reference offer")
		}
	}

	references := []frameReference{}
	for _, rr := range unsentReadReceipts {
		references = append(references, frameReference{FrameID: rr.ID, Type: typeReadReceipt})
	}

	return references
}

func (b *Bounce) getUpdateSettingsToOffer(address string, userID uuid.UUID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	if userID != b.currentUserID() {
		return []frameReference{}
	}

	// Select all update setting that have not been delivered
	var unsentUpdateSettings []updateSettings
	err := b.database.
		Select("update_settings.*").
		Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_settings.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeUpdateSettings).
		Where("delivery_records.id IS NULL").
		Find(&unsentUpdateSettings).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error selecting update settings for reference offer")
	}

	references := []frameReference{}
	for _, ud := range unsentUpdateSettings {
		references = append(references, frameReference{FrameID: ud.ID, Type: typeUpdateSettings})
	}

	return references
}

func (b *Bounce) getFilesToOffer(address string, userID uuid.UUID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	var unsentFiles []file

	if userID == b.currentUserID() {
		// If this is a sync device, send all unsent files
		err := b.database.
			Select("files.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == files.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeFile).
			Where("delivery_records.id IS NULL").
			Find(&unsentFiles).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error selecting files for reference offer")
		}
	} else {
		// If this is not a sync device, get all files where
		// 	scope is not sync AND
		// 	scope is user and the destination is this device's user OR
		// 	scope is group and the destination is a group that this device's user is in OR
		//	scope is group with invites and the destination is a group that this device's user is in OR it is a group that this device's user has been invited to OR
		// 	scope is global and the author is me or this device's user OR
		// 	scope is global and the author is someone who shares a group with this device's user
		err := b.database.
			Distinct("files.id").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == files.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeFile).
			Where(
				`
					delivery_records.id IS NULL AND files.scope != ? AND
					(
						(
							files.scope == ? AND files.destination = ?
						) OR (
							files.scope = ? AND files.destination IN (?)
						) OR (
							files.scope = ? AND (files.destination IN (?) OR files.destination IN (?))
						) OR (
							files.scope == ? AND (
								files.author == ? OR
								files.author == ? OR
								files.author IN (?)
							)
						)
					)`,
				scopeSync,
				scopeUser,
				xor(b.currentUserID(), userID),
				scopeGroup,
				b.database.
					Model(&group{}).
					Distinct().
					Select("groups.id").
					Joins("JOIN group_users ON group_users.group_id = groups.id").
					Where("user_id = ?", userID),
				scopeGroupWithInvites,
				b.database.
					Model(&group{}).
					Distinct().
					Select("groups.id").
					Joins("JOIN group_users ON group_users.group_id = groups.id").
					Where("user_id = ?", userID),
				b.database.
					Model(&group{}).
					Distinct().
					Select("id").
					Where("invites LIKE ?", "%"+userID.String()+"%"),
				scopeGlobal,
				b.currentUserID(),
				userID,
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
							Where("user_id = ?", userID),
					),
			).
			Find(&unsentFiles).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error selecting files for reference offer")
		}
	}

	references := []frameReference{}
	for _, f := range unsentFiles {
		references = append(references, frameReference{FrameID: f.ID, Type: typeFile})
	}

	return references
}

func (b *Bounce) getChunkOffersToOffer(address string, userID uuid.UUID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	var unsentChunkOffers []chunkOffer

	if userID == b.currentUserID() {
		var deliveredIDs []string
		b.database.Model(&deliveryRecord{}).
			Where("destination = ? AND frame_type = ?", address, typeChunkOffer).
			Pluck("frame_id", &deliveredIDs)

		b.database.Model(&chunkOffer{}).
			Distinct("id").
			Where("id NOT IN (?)", deliveredIDs).
			Find(&unsentChunkOffers)
	} else {
		// Groups the user is in
		var groupIDs []string
		b.database.Table("group_users").
			Where("user_id = ?", userID).
			Pluck("group_id", &groupIDs)

		// All the users in the groups this user is in
		var peerIDs []string
		if len(groupIDs) > 0 {
			b.database.Table("group_users").
				Where("group_id IN ?", groupIDs).
				Pluck("user_id", &peerIDs)
		}

		// Groups that this user is invited to
		var groupInviteIDs []string
		b.database.Model(&group{}).
			Where("invites LIKE ?", "%"+userID.String()+"%").
			Pluck("id", &groupInviteIDs)

		// All groups that the user has metadata access to
		allAccessibleGroupIDs := append(groupIDs, groupInviteIDs...)

		// All delivered chunk offers
		var deliveredIDs []string
		b.database.Model(&deliveryRecord{}).
			Where("destination = ? AND frame_type = ?", address, typeChunkOffer).
			Pluck("frame_id", &deliveredIDs)

		var myDevices []string
		b.database.Model(&device{}).
			Where("user_id = ?", b.currentUserID()).
			Pluck("address", &myDevices)

		if len(deliveredIDs) == 0 {
			// Get all of the chunk offers for my profile images
			var globalProfileScoped []chunkOffer
			b.database.Model(&chunkOffer{}).
				Distinct("id").
				Where("location IN (?) AND scope = ? AND destination = ?", myDevices, scopeGlobal, b.currentUserID()).
				Find(&globalProfileScoped)
			unsentChunkOffers = append(unsentChunkOffers, globalProfileScoped...)

			// Get all the user scoped chunk offers for our xor
			var userScoped []chunkOffer
			b.database.Model(&chunkOffer{}).
				Distinct("id").
				Where("scope = ? AND destination = ?", scopeUser, xor(userID, b.currentUserID())).
				Find(&userScoped)
			unsentChunkOffers = append(unsentChunkOffers, userScoped...)

			// Get all the group scoped chunks for any group accessible to the user
			var groupScoped []chunkOffer
			b.database.Model(&chunkOffer{}).
				Distinct("id").
				Where("scope IN ? AND destination IN ?", []int{scopeGroup, scopeGroupWithInvites}, allAccessibleGroupIDs).
				Find(&groupScoped)
			unsentChunkOffers = append(unsentChunkOffers, groupScoped...)

			// Get any overlap scoped offers
			var globalScoped []chunkOffer
			b.database.Model(&chunkOffer{}).
				Distinct("id").
				Where("scope = ? AND (author IN ? OR author = ?)", scopeGlobal, peerIDs, b.currentUserID()).
				Find(&globalScoped)
			unsentChunkOffers = append(unsentChunkOffers, globalScoped...)
		} else {
			// Get all of the chunk offers for my profile images
			var globalProfileScoped []chunkOffer
			b.database.Model(&chunkOffer{}).
				Distinct("id").
				Where("id NOT IN (?) AND location IN (?) AND scope = ? AND destination = ?", deliveredIDs, myDevices, scopeGlobal, b.currentUserID()).
				Find(&globalProfileScoped)
			unsentChunkOffers = append(unsentChunkOffers, globalProfileScoped...)

			// Get all the user scoped chunk offers for our xor
			var userScoped []chunkOffer
			b.database.Model(&chunkOffer{}).
				Distinct("id").
				Where("id NOT IN (?) AND scope = ? AND destination = ?", deliveredIDs, scopeUser, xor(userID, b.currentUserID())).
				Find(&userScoped)
			unsentChunkOffers = append(unsentChunkOffers, userScoped...)

			// Get all the group scoped chunks for any group accessible to the user
			var groupScoped []chunkOffer
			b.database.Model(&chunkOffer{}).
				Distinct("id").
				Where("id NOT IN (?) AND scope IN ? AND destination IN ?", deliveredIDs, []int{scopeGroup, scopeGroupWithInvites}, allAccessibleGroupIDs).
				Find(&groupScoped)
			unsentChunkOffers = append(unsentChunkOffers, groupScoped...)

			// Get any overlap scoped offers
			var globalScoped []chunkOffer
			b.database.Model(&chunkOffer{}).
				Distinct("id").
				Where("id NOT IN (?) AND scope = ? AND (author IN ? OR author = ?)", deliveredIDs, scopeGlobal, peerIDs, b.currentUserID()).
				Find(&globalScoped)
			unsentChunkOffers = append(unsentChunkOffers, globalScoped...)
		}
	}

	references := []frameReference{}
	alreadyIn := map[uuid.UUID]bool{}
	for _, co := range unsentChunkOffers {
		if _, in := alreadyIn[co.ID]; in {
			continue
		}
		references = append(references, frameReference{FrameID: co.ID, Type: typeChunkOffer})
		alreadyIn[co.ID] = true
	}

	return references
}

func (b *Bounce) getDraftsToOffer(address string, userID uuid.UUID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	if userID != b.currentUserID() {
		return []frameReference{}
	}

	// Select all update setting that have not been delivered
	var unsentDrafts []draft
	err := b.database.
		Select("drafts.*").
		Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == drafts.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeDraft).
		Where("delivery_records.id IS NULL").
		Find(&unsentDrafts).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error selecting drafts for reference offer")
	}

	references := []frameReference{}
	for _, ud := range unsentDrafts {
		references = append(references, frameReference{FrameID: ud.ID, Type: typeDraft})
	}

	return references
}

func (b *Bounce) getEncryptedChunkOffersToOffer(address string, userID uuid.UUID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	var unsentEncryptedChunkOffers []encryptedChunkOffer

	if userID == b.currentUserID() {
		var deliveredIDs []string
		b.database.Model(&deliveryRecord{}).
			Where("destination = ? AND frame_type = ?", address, typeEncryptedChunkOffer).
			Pluck("frame_id", &deliveredIDs)

		b.database.Model(&encryptedChunkOffer{}).
			Distinct("id").
			Where("id NOT IN (?)", deliveredIDs).
			Find(&unsentEncryptedChunkOffers)
	} else {
		// Groups the user is in
		var groupIDs []string
		b.database.Table("group_users").
			Where("user_id = ?", userID).
			Pluck("group_id", &groupIDs)

		// All the users in the groups this user is in
		var peerIDs []string
		if len(groupIDs) > 0 {
			b.database.Table("group_users").
				Where("group_id IN ?", groupIDs).
				Pluck("user_id", &peerIDs)
		}

		// Groups that this user is invited to
		var groupInviteIDs []string
		b.database.Model(&group{}).
			Where("invites LIKE ?", "%"+userID.String()+"%").
			Pluck("id", &groupInviteIDs)

		// All groups that the user has metadata access to
		allAccessibleGroupIDs := append(groupIDs, groupInviteIDs...)

		// All delivered chunk offers
		var deliveredIDs []string
		b.database.Model(&deliveryRecord{}).
			Where("destination = ? AND frame_type = ?", address, typeEncryptedChunkOffer).
			Pluck("frame_id", &deliveredIDs)

		if len(deliveredIDs) == 0 {
			// First, get all the user scoped chunk offers for our xor
			var userScoped []encryptedChunkOffer
			b.database.Model(&encryptedChunkOffer{}).
				Distinct("id").
				Where("scope = ? AND destination = ?", scopeUser, xor(userID, b.currentUserID())).
				Find(&userScoped)
			unsentEncryptedChunkOffers = append(unsentEncryptedChunkOffers, userScoped...)

			// Next, get all the group scoped chunks for any group accessible to the user
			var groupScoped []encryptedChunkOffer
			b.database.Model(&encryptedChunkOffer{}).
				Distinct("id").
				Where("scope IN ? AND destination IN ?", []int{scopeGroup, scopeGroupWithInvites}, allAccessibleGroupIDs).
				Find(&groupScoped)
			unsentEncryptedChunkOffers = append(unsentEncryptedChunkOffers, groupScoped...)

			// Lastly, get any overlap scoped offers
			var globalScoped []encryptedChunkOffer
			b.database.Model(&encryptedChunkOffer{}).
				Distinct("id").
				Where("scope = ? AND (author IN ? OR author = ?)", scopeGlobal, peerIDs, b.currentUserID()).
				Find(&globalScoped)
			unsentEncryptedChunkOffers = append(unsentEncryptedChunkOffers, globalScoped...)
		} else {
			// First, get all the user scoped chunk offers for our xor
			var userScoped []encryptedChunkOffer
			b.database.Model(&encryptedChunkOffer{}).
				Distinct("id").
				Where("id NOT IN (?) AND scope = ? AND destination = ?", deliveredIDs, scopeUser, xor(userID, b.currentUserID())).
				Find(&userScoped)
			unsentEncryptedChunkOffers = append(unsentEncryptedChunkOffers, userScoped...)

			// Next, get all the group scoped chunks for any group accessible to the user
			var groupScoped []encryptedChunkOffer
			b.database.Model(&encryptedChunkOffer{}).
				Distinct("id").
				Where("id NOT IN (?) AND scope IN ? AND destination IN ?", deliveredIDs, []int{scopeGroup, scopeGroupWithInvites}, allAccessibleGroupIDs).
				Find(&groupScoped)
			unsentEncryptedChunkOffers = append(unsentEncryptedChunkOffers, groupScoped...)

			// Lastly, get any overlap scoped offers
			var globalScoped []encryptedChunkOffer
			b.database.Model(&encryptedChunkOffer{}).
				Distinct("id").
				Where("id NOT IN (?) AND scope = ? AND (author IN ? OR author = ?)", deliveredIDs, scopeGlobal, peerIDs, b.currentUserID()).
				Find(&globalScoped)
			unsentEncryptedChunkOffers = append(unsentEncryptedChunkOffers, globalScoped...)
		}
	}

	references := []frameReference{}
	for _, eco := range unsentEncryptedChunkOffers {
		references = append(references, frameReference{FrameID: eco.ID, Type: typeEncryptedChunkOffer})
	}

	return references
}

func (b *Bounce) handleReferenceOffer(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	if b.devicePool.isRevoked(peer) {
		return nil, false
	}

	// Unpack the offer
	var ro referenceOffer
	err := msgpack.Unmarshal(payload, &ro)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling reference offer")
		return nil, false
	}

	go b.sendAck(peer, typeReferenceOffer, ro.ID)

	lastROTimeMutex.Lock()
	lastROTime[peer] = time.Now().Unix()
	lastROTimeMutex.Unlock()

	encryptedDeviceCacheMutex.Lock()
	_, encryptedPeer := encryptedDeviceCache[peer]
	encryptedDeviceCacheMutex.Unlock()
	if len(ro.References) == 0 && !b.encrypted && encryptedPeer {
		// An encrypted device may send an empty reference offer in order to trigger the peer
		// to request the management key hash, so that the peer can check if the encrypted device needs
		// to be re-keyed.  This is the only case that an empty reference offer should ever be sent.
		b.getManagementKeyHash(peer)
		b.sendEncryptedClearBefores(peer)
		b.sendDirect(peer, requestECRO{})
		return nil, false
	}

	// Make sure we aren't still handling a catch up while determining what we need to request
	catchUpMutex.Lock()

	// Create a set of references for all of the offered references that we don't have, ACKing the ones we do have in the process
	references := []frameReference{}
	acks := []frameReference{}
	typesToIDs := referencedIDs(ro.References)
	for _, frameType := range syncableTypes {
		refs, as := b.getFramesToRequestAndAck(peer, typesToIDs[frameType], frameType)
		references = append(references, refs...)
		acks = append(acks, as...)
	}

	// Unlock the catch up mutex
	catchUpMutex.Unlock()

	// Inform the reference engine that this peer has these frames that we don't know about
	if len(references) > 0 {
		b.loadReferenceOffer(peer, references)
	}

	// Send an ack for everything we already have in the database
	if len(acks) > 0 {
		go b.sendDirect(peer, &ack{
			References: acks,
		})
	}

	return nil, false
}

func (b *Bounce) getFramesToRequestAndAck(address string, offeredIDs []uuid.UUID, frameType uint16) ([]frameReference, []frameReference) {
	references := []frameReference{}
	acks := []frameReference{}

	var table string
	var ok bool
	if !b.encrypted {
		table, ok = typeTable[frameType]
		if !ok {
			log.WithFields(log.Fields{
				"type": frameType,
			}).Warn("attempted to get frame that doesn't have database table")
		}
	}

	for _, id := range offeredIDs {
		ref := frameReference{FrameID: id, Type: frameType}

		var err error
		if b.encrypted {
			var ef encryptedFrame
			err = b.database.Where("id = ? AND type = ?", id, frameType).Take(&ef).Error
		} else {
			var count int64
			err = b.database.Table(table).Where("id = ?", id).Count(&count).Error
			if count == 0 {
				err = gorm.ErrRecordNotFound
			}
		}

		if err == nil {
			b.markFrameDelivered(id, frameType, address)
			acks = append(acks, ref)
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			references = append(references, ref)
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error getting frame to request or ack")
		}
	}

	return references, acks
}
