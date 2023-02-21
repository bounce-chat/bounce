package chat

import (
	"errors"
	"strings"
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
	_msgpack       struct{}  `msgpack:",omitempty"`
	ID             uuid.UUID `gorm:"type:uuid;primary_key;"`
	CreatedAt      int64     // Used to delete old reference offers that were not responded to
	DirectMessages string    // Comma-separated list of DM UUIDs
	GroupMessages  string
	UpdateDMs      string
	Devices        string
	Users          string // only for sync devices
	GroupCreations string
	UpdateGroups   string
	Destination    uuid.UUID `msgpack:"-"`
	payload        []byte
	payloadMutex   sync.Mutex
}

func (ro *referenceOffer) BeforeCreate(tx *gorm.DB) error {
	if ro.ID == uuid.Nil {
		log.Fatal("reference offer must have ID assigned before creation")
	}
	ro.CreatedAt = time.Now().Unix()
	return nil
}

func (ro *referenceOffer) getID() uuid.UUID {
	return ro.ID
}

func (ro *referenceOffer) getScope(_ uuid.UUID) int {
	return scopeDevice
}

func (ro *referenceOffer) getDestination(_ uuid.UUID) uuid.UUID {
	return ro.Destination
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
	if len(ro.DirectMessages) > 0 {
		return true
	}
	if len(ro.GroupMessages) > 0 {
		return true
	}
	if len(ro.Devices) > 0 {
		return true
	}
	if len(ro.Users) > 0 {
		return true
	}
	if len(ro.GroupCreations) > 0 {
		return true
	}
	if len(ro.UpdateGroups) > 0 {
		return true
	}
	// TODO: changes to my device group, clearing thread histories, etc
	return false
}

func (ro *referenceOffer) hasContent() bool {
	if len(ro.DirectMessages) > 0 {
		return true
	}
	if len(ro.GroupMessages) > 0 {
		return true
	}
	if len(ro.UpdateDMs) > 0 {
		return true
	}
	if len(ro.Devices) > 0 {
		return true
	}
	if len(ro.Users) > 0 {
		return true
	}
	if len(ro.GroupCreations) > 0 {
		return true
	}
	if len(ro.UpdateGroups) > 0 {
		return true
	}
	// TODO: check for any future referenced content
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

	references := b.getReferenceOfferFor(peerAddress)
	if references.hasContent() {
		err := b.database.Create(references).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error saving referenceOffer")
		} else {
			go b.broadcastUntilDelivered(references)
		}
	}
}

func (b *bounce) getReferenceOfferFor(address string) *referenceOffer {
	dev, ok := b.getDeviceFromAddress(address)
	if !ok {
		return &referenceOffer{}
	}

	return &referenceOffer{
		ID:             uuid.New(),
		Destination:    dev.ID,
		DirectMessages: b.getDirectMessagesToOffer(dev),
		GroupMessages:  b.getGroupMessagesToOffer(dev),
		UpdateDMs:      b.getUpdateDMsToOffer(dev),
		Devices:        b.getDevicesToOffer(dev),
		Users:          b.getUsersToOffer(dev),
		GroupCreations: b.getGroupCreationsToOffer(dev),
		UpdateGroups:   b.getUpdateGroupsToOffer(dev),
	}
}

func (b *bounce) getDirectMessagesToOffer(dev device) string {
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
	dmsToOffer := []string{}
	for _, dm := range dms {
		dmsToOffer = append(dmsToOffer, dm.ID.String())
	}
	return strings.Join(dmsToOffer, ",")
}

func (b *bounce) getGroupMessagesToOffer(dev device) string {
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
	gmsToOffer := []string{}
	for _, gm := range gms {
		gmsToOffer = append(gmsToOffer, gm.ID.String())
	}
	return strings.Join(gmsToOffer, ",")
}

func (b *bounce) getUpdateDMsToOffer(dev device) string {
	var unsentUpdateDMs []updateDM
	updateDMsToOffer := []string{}

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

	for _, ud := range unsentUpdateDMs {
		// Ignore sync scoped update DMs unless this is a sync device
		if ud.getScope(b.currentUserID()) == scopeSync && !b.isSyncDevice(dev) {
			continue
		}
		updateDMsToOffer = append(updateDMsToOffer, ud.ID.String())
	}

	return strings.Join(updateDMsToOffer, ",")
}

func (b *bounce) getDevicesToOffer(dev device) string {
	offerString := ""

	if b.isSyncDevice(dev) {
		// Sync devices can learn about any device we know about
		devicesToOffer := []string{}
		var unsentDevices []device
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
		for _, dev := range unsentDevices {
			devicesToOffer = append(devicesToOffer, dev.ID.String())
		}
		offerString = strings.Join(devicesToOffer, ",")
	} else {
		// TODO: don't just get our devices, get any "overlap" devices
		devicesToOffer := []string{}
		var unsentDevices []device
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
		for _, dev := range unsentDevices {
			devicesToOffer = append(devicesToOffer, dev.ID.String())
		}
		offerString = strings.Join(devicesToOffer, ",")
	}

	return offerString
}

func (b *bounce) getUsersToOffer(dev device) string {
	offerString := ""

	if b.isSyncDevice(dev) {
		// Users
		usersToOffer := []string{}
		var unsentUsers []user
		err := b.database.
			Select("users.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == users.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", dev.Address, typeUser).
			Where("delivery_records.id IS NULL").
			Find(&unsentUsers).Error // TODO: only select ID?
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error selecting users for reference offer")
		}
		for _, u := range unsentUsers {
			usersToOffer = append(usersToOffer, u.ID.String())
		}
		offerString = strings.Join(usersToOffer, ",")
	} else {
		// TODO: offer "overlap" users to other people, users who share a group with this user
	}

	return offerString
}

func (b *bounce) getGroupCreationsToOffer(dev device) string {
	groupCreationsToOffer := []string{}

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

	for _, gc := range unsentGroupCreations {
		groupCreationsToOffer = append(groupCreationsToOffer, gc.ID.String())
	}

	return strings.Join(groupCreationsToOffer, ",")
}

func (b *bounce) getUpdateGroupsToOffer(dev device) string {
	updateGroupsToOffer := []string{}

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

	for _, ug := range unsentUpdateGroups {
		// Ignore sync scoped update groups unless this is a sync device
		if ug.getScope(b.currentUserID()) == scopeSync && !b.isSyncDevice(dev) {
			continue
		}
		updateGroupsToOffer = append(updateGroupsToOffer, ug.ID.String())
	}

	return strings.Join(updateGroupsToOffer, ",")
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

	dev, exists := b.getDeviceFromAddress(peer)
	if !exists {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Info("error loading device for reference offer handling")
		return
	}

	response := &referenceRequest{
		ID:             ro.ID,
		destination:    dev.ID,
		DirectMessages: b.getDirectMessagesToRequest(dev, ro),
		GroupMessages:  b.getGroupMessagesToRequest(dev, ro),
		UpdateDMs:      b.getUpdateDMsToRequest(dev, ro),
		Devices:        b.getDevicesToRequest(dev, ro),
		Users:          b.getUsersToRequest(dev, ro),
		GroupCreations: b.getGroupCreationsToRequest(dev, ro),
		UpdateGroups:   b.getUpdateGroupsToRequest(dev, ro),
	}

	b.broadcast(response)
}

func (b *bounce) getDirectMessagesToRequest(dev device, ro referenceOffer) string {
	requestedDMs := []string{}
	if len(ro.DirectMessages) > 0 {
		for _, dmIDString := range strings.Split(ro.DirectMessages, ",") {
			dmID, err := uuid.Parse(dmIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":  err.Error(),
					"string": dmIDString,
				}).Error("invalid UUID in reference offer")
				continue
			}

			var count int64
			err = b.database.Model(&DirectMessage{}).Where("id = ?", dmID).Count(&count).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error getting message from database")
				continue
			}
			if count == 0 {
				// TODO: want to make sure this can't be used to probe a devices for messages that it might already have from another chat,
				// to test group membership if a chat message from another group is known
				requestedDMs = append(requestedDMs, dmID.String())
			} else {
				// We already have this message, but if we didn't already know they had it, we update our records
				var dm DirectMessage
				err = b.database.First(&dm, "id = ?", dmID).Error
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error looking up known DM in reference offer")
					continue
				}
				if !b.isDeliveredTo(&dm, dev.Address) {
					b.markDeliveredTo(&dm, dev.Address)
				}
			}
		}
	}

	return strings.Join(requestedDMs, ",")

}

func (b *bounce) getGroupMessagesToRequest(dev device, ro referenceOffer) string {
	requestedGMs := []string{}
	if len(ro.GroupMessages) > 0 {
		for _, gmIDString := range strings.Split(ro.GroupMessages, ",") {
			gmID, err := uuid.Parse(gmIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":  err.Error(),
					"string": gmIDString,
				}).Error("invalid UUID in reference offer")
				continue
			}

			var count int64
			err = b.database.Model(&GroupMessage{}).Where("id = ?", gmID).Count(&count).Error // TODO: refactor this away from using count everywhere
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error getting group message from database")
				continue
			}
			if count == 0 {
				// TODO: want to make sure this can't be used to probe a devices for messages that it might already have from another chat,
				// to test group membership if a chat message from another group is known
				requestedGMs = append(requestedGMs, gmID.String())
			} else {
				// We already have this message, but if we didn't already know they had it, we update our records
				var gm GroupMessage
				err = b.database.First(&gm, "id = ?", gmID).Error
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error looking up known GM in reference offer")
					continue
				}
				if !b.isDeliveredTo(&gm, dev.Address) {
					b.markDeliveredTo(&gm, dev.Address)
				}
			}
		}
	}

	return strings.Join(requestedGMs, ",")

}

func (b *bounce) getUpdateDMsToRequest(dev device, ro referenceOffer) string {
	var resp string

	if len(ro.UpdateDMs) > 0 {
		desiredUpdateDMs := []string{}
		for _, udIDString := range strings.Split(ro.UpdateDMs, ",") {
			udID, err := uuid.Parse(udIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":  err.Error(),
					"string": udIDString,
				}).Error("invalid ud UUID in reference offer")
				continue
			}

			var ud updateDM
			err = b.database.First(&ud, "id = ?", udID).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					desiredUpdateDMs = append(desiredUpdateDMs, udID.String())
				} else {
					log.WithFields(log.Fields{
						"id":    udID,
						"error": err.Error(),
					}).Fatal("database error querying for update DM settings")
				}
			}
		}
		resp = strings.Join(desiredUpdateDMs, ",")
	}

	return resp
}

func (b *bounce) getDevicesToRequest(dev device, ro referenceOffer) string {
	var resp string

	if len(ro.Devices) > 0 {
		desiredDevices := []string{}
		for _, deviceIDString := range strings.Split(ro.Devices, ",") {
			deviceID, err := uuid.Parse(deviceIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":  err.Error(),
					"string": deviceIDString,
				}).Error("invalid device UUID in reference offer")
				continue
			}

			var dev device
			err = b.database.First(&dev, "id = ?", deviceID).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					desiredDevices = append(desiredDevices, deviceID.String())
				} else {
					log.WithFields(log.Fields{
						"id":    deviceID,
						"error": err.Error(),
					}).Fatal("database error querying for device")
				}
			}
		}
		resp = strings.Join(desiredDevices, ",")
	}

	return resp
}

func (b *bounce) getUsersToRequest(dev device, ro referenceOffer) string {
	var resp string

	if len(ro.Users) > 0 {
		if dev.UserID != b.currentUserID() {
			log.WithFields(log.Fields{
				"peer": dev.Address,
			}).Warn("a non-sync device offered users in a reference offer, refusing to request them")
			return resp
		}

		desiredUsers := []string{}
		for _, userIDString := range strings.Split(ro.Users, ",") {
			userID, err := uuid.Parse(userIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":  err.Error(),
					"string": userIDString,
				}).Error("invalid user UUID in reference offer")
				continue
			}

			var u user
			err = b.database.First(&u, "id = ?", userID).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					desiredUsers = append(desiredUsers, userID.String())
				} else {
					log.WithFields(log.Fields{
						"id":    userID,
						"error": err.Error(),
					}).Fatal("database error querying for user")
				}
			}
		}
		resp = strings.Join(desiredUsers, ",")
	}

	return resp
}

func (b *bounce) getGroupCreationsToRequest(dev device, ro referenceOffer) string {
	var resp string

	if len(ro.GroupCreations) > 0 {
		desiredGroupCreations := []string{}
		for _, groupCreationIDString := range strings.Split(ro.GroupCreations, ",") {
			groupCreationID, err := uuid.Parse(groupCreationIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":  err.Error(),
					"string": groupCreationIDString,
				}).Error("invalid group creation UUID in reference offer")
				continue
			}

			var gc groupCreation
			err = b.database.First(&gc, "id = ?", groupCreationID).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					desiredGroupCreations = append(desiredGroupCreations, groupCreationID.String())
				} else {
					log.WithFields(log.Fields{
						"id":    groupCreationID,
						"error": err.Error(),
					}).Fatal("database error querying for group creation")
				}
			}
		}
		resp = strings.Join(desiredGroupCreations, ",")
	}

	return resp
}

func (b *bounce) getUpdateGroupsToRequest(dev device, ro referenceOffer) string {
	var resp string

	if len(ro.UpdateGroups) > 0 {
		desiredUpdateGroups := []string{}
		for _, updateGroupIDString := range strings.Split(ro.UpdateGroups, ",") {
			updateGroupID, err := uuid.Parse(updateGroupIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":  err.Error(),
					"string": updateGroupIDString,
				}).Error("invalid update group UUID in reference offer")
				continue
			}

			var ug updateGroup
			err = b.database.First(&ug, "id = ?", updateGroupID).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					desiredUpdateGroups = append(desiredUpdateGroups, updateGroupID.String())
				} else {
					log.WithFields(log.Fields{
						"id":    updateGroupID,
						"error": err.Error(),
					}).Fatal("database error querying for update group")
				}
			}
		}
		resp = strings.Join(desiredUpdateGroups, ",")
	}

	return resp
}
