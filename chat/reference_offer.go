package chat

import (
	"errors"
	"strings"
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
}

func (referenceOffer *referenceOffer) BeforeCreate(tx *gorm.DB) error {
	referenceOffer.CreatedAt = time.Now().Unix()
	return nil
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
	if len(ro.payload) == 0 {
		bytes, err := msgpack.Marshal(ro)
		if err != nil {
			// TODO: how to handle?
		}
		ro.payload = bytes
	}
	return ro.payload
}

func (ro *referenceOffer) isAlreadyDeliveredTo(address string) bool {
	return false
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
	references := b.getReferenceOfferFor(peerAddress)
	if references.hasContent() {
		err := b.database.Create(references).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error saving referenceOffer")
		} else {
			go b.broadcastReferenceOffer(references)
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
		Order("saved_at asc").
		Where(
			"written_at >= ? AND (destination = ? OR source = ?) AND (delivered_to NOT LIKE ? OR delivered_to IS NULL)",
			time.Now().Add(-undeliverableAfter).Unix(),
			dev.UserID,
			dev.UserID,
			"%"+dev.Address+"%",
		).Find(&dms).Error // TODO: also add a sane limit? TODO: only select the IDs if that's all we're using?
	if err != nil {
		log.WithFields(log.Fields{
			"peer":  dev.Address,
			"error": err.Error(),
		}).Fatal("error loading DMs for reference offer")
	}

	// Collect IDs from the DMs
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
	err := b.database.Where("delivered_to NOT LIKE ? OR delivered_to IS NULL", "%"+dev.Address+"%").Find(&localDMSettingsUpdates).Error // TODO: only select ID?
	// TODO: only get one per unix timestamp?  UNIQUE timestamp LIMIT 1?
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

	if !b.isSyncDevice(dev.Address) {
		// Sync devices can learn about any device we know about
		devicesToOffer := []string{}
		var unsentDevices []device
		err := b.database.Where("delivered_to NOT LIKE ? OR delivered_to IS NULL", "%"+dev.Address+"%").Find(&unsentDevices).Error // TODO: only select ID?
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
		err := b.database.Where("user_id = ? AND (delivered_to NOT LIKE ? OR delivered_to IS NULL)", b.currentUserID(), "%"+dev.Address+"%").Find(&unsentDevices).Error // TODO: only select ID?
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
		err := b.database.Where("delivered_to NOT LIKE ? OR delivered_to IS NULL", "%"+dev.Address+"%").Find(&unsentUsers).Error // TODO: only select ID?
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

func (b *bounce) broadcastReferenceOffer(ro *referenceOffer) {
	giveUpTime := time.Now().Add(5 * time.Minute)
	for {
		b.broadcast(ro)
		time.Sleep(15 * time.Second) // TODO: derive from message size?
		b.devicePool.receivedAcksMutex.Lock()
		_, ok := b.devicePool.receivedAcks[ro.ID.String()]
		b.devicePool.receivedAcksMutex.Unlock()
		if ok {
			// we got the request, our offer was delivered
			b.devicePool.receivedAcksMutex.Lock()
			delete(b.devicePool.receivedAcks, ro.ID.String())
			b.devicePool.receivedAcksMutex.Unlock()
			return
		}
		if time.Now().After(giveUpTime) {
			log.WithFields(log.Fields{
				"id":          ro.ID,
				"destination": ro.Destination,
			}).Warn("gave up attempting to deliver reference offer")
			err := b.database.Delete(ro).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("error deleting reference offer after gave up on broadcasting")
			}
			return
		}
	}
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
				if !dm.isAlreadyDeliveredTo(dev.Address) {
					b.markDirectMessageDeliveredTo(&dm, dev.Address)
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
