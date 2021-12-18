package chat

import (
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
)

type syncDeviceRequestAccepted struct {
	Profile     user
	SyncDevices []device
	payload     []byte
}

func (sdra *syncDeviceRequestAccepted) getScope(_ uuid.UUID) int {
	return scopeDevice
}

func (sdra *syncDeviceRequestAccepted) getDestination(_ uuid.UUID) uuid.UUID {
	return uuid.Nil
}

func (sdra *syncDeviceRequestAccepted) getType() uint16 {
	return typeSyncDeviceRequestAccepted
}

func (sdra *syncDeviceRequestAccepted) getPayload() []byte {
	if len(sdra.payload) == 0 {
		bytes, err := msgpack.Marshal(sdra)
		if err != nil {
			// TODO: how to handle?
		}
		sdra.payload = bytes
	}
	return sdra.payload
}

func (sdra *syncDeviceRequestAccepted) isAlreadyDeliveredTo(address string) bool {
	return false
}

func (b *bounce) handleSyncDeviceRequestAccepted(peer string, payload []byte) {
	// Unmarshal the payload
	var sdra syncDeviceRequestAccepted
	err := msgpack.Unmarshal(payload, &sdra)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling sync device request accepted")
		return
	}

	// Make sure a profile doesn't already exist
	_, exists := b.currentUser()
	if exists {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("peer sent a sync device request accepted after a profile exists")
		return
	}

	// TODO: make sure the set of sync devices contains this device and is a valid device group

	err = b.database.Transaction(func(tx *gorm.DB) error {
		// Save this user as our profile
		err = tx.Create(&sdra.Profile).Error
		if err != nil {
			return err
		}

		// Save our current sync devices
		for _, dev := range sdra.SyncDevices {
			err = tx.Create(&dev).Error
			if err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error running transaction for sync device acceptance")
	}

	// Inform the UI
	b.userInterface.SyncDeviceRequestAccepted(sdra.Profile.ID, sdra.Profile.Name)

	b.auditPeers()
}
