package chat

import (
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
)

// A sync device request accepted frame is sent from an offer device to a requester device when it is approving the requester
// device as a new sync device.  It contains the full profile that the requester device is being added to, this includes the
// requester device in the device group.
type syncDeviceRequestAccepted struct {
	Profile         user
	PrivateECDHKey  []byte
	PublicECDHKey   []byte
	PrivateECDSAKey []byte
	PublicECDSAKey  []byte
	Settings        *profileSettings
	References      bool // TODO: just send the actual offer in here?
}

func (sdra *syncDeviceRequestAccepted) getType() uint16 {
	return typeSyncDeviceRequestAccepted
}

func (sdra *syncDeviceRequestAccepted) getPayload() []byte {
	bytes, err := msgpack.Marshal(sdra)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal sync device request accepted")
	}
	return bytes
}

func (b *Bounce) handleSyncDeviceRequestAccepted(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	// Unmarshal the payload
	var sdra syncDeviceRequestAccepted
	err := msgpack.Unmarshal(payload, &sdra)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling sync device request accepted")
		return nil, false
	}

	// Make sure a profile doesn't already exist
	_, exists := b.currentUser()
	if exists {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("peer sent a sync device request accepted after a profile exists")
		return nil, false
	}

	// Profile is excluded in msgpack, make sure it is set here
	sdra.Profile.Profile = true
	sdra.Profile.PrivateECDHKey = sdra.PrivateECDHKey
	sdra.Profile.PublicECDHKey = sdra.PublicECDHKey
	sdra.Profile.PrivateECDSAKey = sdra.PrivateECDSAKey
	sdra.Profile.PublicECDSAKey = sdra.PublicECDSAKey

	// Make sure this profile has a valid device group
	if !b.hasValidDeviceGroup(sdra.Profile) {
		log.Error("sdra contains profile with invalid device group")
		return nil, false
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
	sdra.Settings.ID = uuid.New()
	sdra.Profile.ProfileSettings = sdra.Settings
	sdra.Profile.OpenDM = true
	err = b.database.Create(&sdra.Profile).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving new sync profile")
	}
	b.createAndShareDeviceECDHKey()

	// Collect the sync devices for the UI
	var devices []Device
	for _, dev := range sdra.Profile.Devices {
		if dev.RevokedAt == 0 {
			syncDev := Device{
				ID:        dev.ID,
				Name:      dev.Name,
				Address:   dev.Address,
				CreatedAt: dev.Timestamp,
				Local:     dev.Address == b.network.Address(),
				Online:    dev.Address == peer,
			}
			if peer == dev.Address {
				syncDev.LastSeen = time.Now().Unix()
			}
			devices = append(devices, syncDev)
		} else {
			b.devicePool.revokedDevices[dev.Address] = true
		}
	}

	// Inform the UI
	exportedUser := User{
		ID:               sdra.Profile.ID,
		Name:             sdra.Profile.Name,
		Images:           sdra.Profile.images(),
		IntroductionTime: sdra.Profile.IntroductionTime,
		State: DMState{
			Open: true,
		},
	}
	b.ui.SyncDeviceRequestAccepted(exportedUser, devices, sdra.References)

	// Connect to any other sync devices now
	b.auditPeers()
	b.updateUserOnlineStatus(peer)

	// Mark that we're now waiting for a catch up for this device
	if sdra.References {
		waitingForInitialSyncFrom = peer
	}

	return nil, false
}
