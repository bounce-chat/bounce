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
)

//
// A reference offer is a message sent to a newly connected device that provides the UUIDs of any messages that device
// should have, but that we didn't deliver to it.
//
type referenceOffer struct {
	_msgpack              struct{}  `msgpack:",omitempty"`
	ID                    uuid.UUID `gorm:"type:uuid;primary_key;"`
	CreatedAt             int64     // Used to delete old reference offers that were not responded to
	DirectMessages        string    // Comma-separated list of DM UUIDs
	UpdateLocalDMSettings string    // only for sync devices
	Devices               string
	Users                 string    // only for sync devices
	Destination           uuid.UUID `msgpack:"-"`
	payload               []byte
	payloadMutex          sync.Mutex
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
	if len(ro.UpdateLocalDMSettings) > 0 {
		return true
	}
	if len(ro.Devices) > 0 {
		return true
	}
	if len(ro.Users) > 0 {
		return true
	}
	// TODO: changes to my device group, clearing thread histories, etc
	return false
}

func (ro *referenceOffer) hasContent() bool {
	if len(ro.DirectMessages) > 0 {
		return true
	}
	if len(ro.UpdateLocalDMSettings) > 0 {
		return true
	}
	if len(ro.Devices) > 0 {
		return true
	}
	if len(ro.Users) > 0 {
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

	myDevice, exists := b.getDeviceFromAddress(b.network.Address())
	if !exists {
		// We're going to be inserting connections in the device pool when we add a device as
		// a new sync device.  During that time we haven't saved anything as a local device,
		// so we wouldn't expect anything here.  Nothing to do.
		return
	}

	if !b.isDeliveredTo(&myDevice, peerAddress) { // TODO: use broadcastUntilDelivered?
		for i := 0; i < 5; i++ {
			rd := b.getRemoteDevice(peerAddress)
			rd.messages <- &myDevice
			time.Sleep(3 * time.Second)
			if b.isDeliveredTo(&myDevice, peerAddress) {
				break
			}
		}
		if !b.isDeliveredTo(&myDevice, peerAddress) {
			log.WithFields(log.Fields{
				"peer": peerAddress,
			}).Warn("was not able to send device to peer that might not have it before reference offer")
			return
		}
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
		ID:                    uuid.New(),
		Destination:           dev.ID,
		DirectMessages:        b.getDirectMessagesToOffer(dev),
		UpdateLocalDMSettings: b.getUpdateLocalDMSettingsToOffer(dev),
		Devices:               b.getDevicesToOffer(dev),
		Users:                 b.getUsersToOffer(dev),
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

func (b *bounce) getUpdateLocalDMSettingsToOffer(dev device) string {
	if !b.isSyncDevice(dev.Address) {
		return ""
	}
	uldsToOffer := []string{}
	var localDMSettingsUpdates []updateLocalDMSettings
	err := b.database.
		Select("local_dm_settings_updates.*").
		Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == local_dm_settings_updates.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", dev.Address, typeUpdateLocalDMSettings).
		Where("delivery_records.id IS NULL").
		Find(&localDMSettingsUpdates).Error // TODO: only select ID?
	// TODO: only get one per unix timestamp?  UNIQUE timestamp LIMIT 1?  Only get the latest one?
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error selecting update local DM settings for reference offer")
	}
	for _, ulds := range localDMSettingsUpdates {
		uldsToOffer = append(uldsToOffer, ulds.ID.String())
	}
	return strings.Join(uldsToOffer, ",")
}

func (b *bounce) getDevicesToOffer(dev device) string {
	offerString := ""

	if b.isSyncDevice(dev.Address) {
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

	if b.isSyncDevice(dev.Address) {
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
		ID:                    ro.ID,
		destination:           dev.ID,
		DirectMessages:        b.getDirectMessagesToRequest(dev, ro),
		UpdateLocalDMSettings: b.getUpdateLocalDMSettingsToRequest(dev, ro),
		Devices:               b.getDevicesToRequest(dev, ro),
		Users:                 b.getUsersToRequest(dev, ro),
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

func (b *bounce) getUpdateLocalDMSettingsToRequest(dev device, ro referenceOffer) string {
	var resp string

	if len(ro.UpdateLocalDMSettings) > 0 {
		if dev.UserID != b.currentUserID() {
			log.WithFields(log.Fields{
				"peer": dev.Address,
			}).Warn("a non-sync device offered update local DM settings in a reference offer, refusing to request them")
			return resp
		}

		desiredUpdateLocalDMSettings := []string{}
		for _, uldsIDString := range strings.Split(ro.UpdateLocalDMSettings, ",") {
			uldsID, err := uuid.Parse(uldsIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":  err.Error(),
					"string": uldsIDString,
				}).Error("invalid ulds UUID in reference offer")
				continue
			}

			var ulds updateLocalDMSettings
			err = b.database.First(&ulds, "id = ?", uldsID).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					desiredUpdateLocalDMSettings = append(desiredUpdateLocalDMSettings, uldsID.String())
				} else {
					log.WithFields(log.Fields{
						"id":    uldsID,
						"error": err.Error(),
					}).Fatal("database error querying for update local DM settings")
				}
			}
		}
		resp = strings.Join(desiredUpdateLocalDMSettings, ",")
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
