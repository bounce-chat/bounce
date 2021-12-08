package chat

import (
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
)

type syncDeviceRequestAccepted struct {
	Profile user
	payload []byte
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

	// Save this user as our profile
	err = b.database.Create(&sdra.Profile).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving new profile received from sync device request accepted")
	}

	// Inform the UI
	b.userInterface.SyncDeviceRequestAccepted(sdra.Profile.ID, sdra.Profile.Name)
}
