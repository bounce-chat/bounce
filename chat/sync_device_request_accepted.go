package chat

import (
	"sync"

	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
)

//
// A sync device request accepted frame is sent from an offer device to a requester device when it is approving the requester
// device as a new sync device.  It contains the full profile that the requester device is being added to, this includes the
// requester device in the device group.
//
type syncDeviceRequestAccepted struct {
	Profile      user
	payload      []byte
	payloadMutex sync.Mutex
}

func (sdra *syncDeviceRequestAccepted) getType() uint16 {
	return typeSyncDeviceRequestAccepted
}

func (sdra *syncDeviceRequestAccepted) getPayload() []byte {
	sdra.payloadMutex.Lock()
	defer sdra.payloadMutex.Unlock()

	if len(sdra.payload) == 0 {
		bytes, err := msgpack.Marshal(sdra)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal sync device request accepted")
		}
		sdra.payload = bytes
	}
	return sdra.payload
}

func (b *bounce) handleSyncDeviceRequestAccepted(peer string, payload []byte, catchUp bool) broadcastable {
	// Unmarshal the payload
	var sdra syncDeviceRequestAccepted
	err := msgpack.Unmarshal(payload, &sdra)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling sync device request accepted")
		return nil
	}

	// Make sure a profile doesn't already exist
	_, exists := b.currentUser()
	if exists {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("peer sent a sync device request accepted after a profile exists")
		return nil
	}

	// Profile is excluded in msgpack, make sure it is set here
	sdra.Profile.Profile = true

	// Make sure this profile has a valid device group
	if !b.hasValidDeviceGroup(sdra.Profile) {
		log.Error("sdra contains profile with invalid device group")
		return nil
	}

	// Make sure that our device is in the device group of this new profile
	localDeviceFound := false
	for _, dev := range sdra.Profile.Devices {
		if dev.Address == b.network.Address() {
			localDeviceFound = true
		}
	}
	if !localDeviceFound {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("rejecting sync device request accepted that does not contain this device in the device group")
	}

	// Save the new profile
	err = b.database.Create(&sdra.Profile).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving new sync profile")
	}

	// Inform the UI
	b.userInterface.SyncDeviceRequestAccepted(sdra.Profile.ID, sdra.Profile.Name)

	// Connect to any other sync devices now
	b.auditPeers()

	return nil
}
