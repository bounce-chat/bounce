package chat

import (
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	log "github.com/sirupsen/logrus"
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
	// Make sure we've sent a sync request to this peer before, and note how many requests have been cancelled as of
	// that moment, so that one cancelled while this answer is being checked is seen before the profile is saved
	syncRequestCancelMutex.Lock()
	expected := pendingSyncRequests.isExpectedFrom(peer)
	cancelled := syncRequestsCancelled
	syncRequestCancelMutex.Unlock()
	if !expected {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("ignoring sync device request accepted from a peer we did not recently make a sync request to")
		return nil, false
	}

	// Make sure a profile doesn't already exist.  This comes before anything that tells the UI the answer was refused,
	// so that nothing arriving after this device has its profile can raise that
	_, exists := b.currentUser()
	if exists {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("peer sent a sync device request accepted after a profile exists")
		return nil, false
	}

	// From here on the device we asked has answered, and the user is waiting on that answer.  An answer that cannot be
	// used is refused out loud: the usual cause is a device running another version, or one whose profile was made by
	// one, and with only a log line the user is left looking at a pairing that never finishes.

	// Unmarshal the payload
	var sdra syncDeviceRequestAccepted
	err := msgpack.Unmarshal(payload, &sdra)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling sync device request accepted")
		b.ui.SyncDeviceProfileRefused("its answer could not be read")
		return nil, false
	}

	// Profile is excluded in msgpack, make sure it is set here
	sdra.Profile.Profile = true
	sdra.Profile.PrivateECDHKey = sdra.PrivateECDHKey
	sdra.Profile.PublicECDHKey = sdra.PublicECDHKey
	sdra.Profile.PrivateECDSAKey = sdra.PrivateECDSAKey
	sdra.Profile.PublicECDSAKey = sdra.PublicECDSAKey

	// Make sure the profile has valid fields
	if err := validUserRecord(sdra.Profile); err != nil {
		log.WithFields(log.Fields{
			"peer":  peer,
			"error": err.Error(),
		}).Error("sdra contains invalid profile record")
		b.ui.SyncDeviceProfileRefused("its profile is not valid: " + err.Error())
		return nil, false
	}

	// The offer device cannot sign for us. Complete our provisional record before validating the whole device group.
	localDeviceIndex := -1
	for i := range sdra.Profile.Devices {
		dev := &sdra.Profile.Devices[i]
		if dev.Address != b.network.Address() {
			continue
		}
		if localDeviceIndex != -1 || !idMatchesContent(dev) || dev.Signature == nil || dev.Signature.PreexistingDevice != peer {
			log.WithFields(log.Fields{
				"peer": peer,
			}).Warn("rejecting sync device request accepted with an invalid provisional device")
			b.ui.SyncDeviceProfileRefused("the record it made for this device is not valid")
			return nil, false
		}
		localDeviceIndex = i
		b.signDevice(dev)
	}
	if localDeviceIndex == -1 {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("rejecting sync device request accepted that does not contain this device in the device group")
		b.ui.SyncDeviceProfileRefused("its profile does not include this device")
		return nil, false
	}
	if !b.hasValidDeviceGroup(sdra.Profile) {
		log.Error("sdra contains profile with invalid device group")
		b.ui.SyncDeviceProfileRefused("a device in its profile is not valid")
		return nil, false
	}

	// The offer device must be in the group as our introducer
	peerFound := false
	for _, dev := range sdra.Profile.Devices {
		if dev.Address == peer {
			peerFound = true
		}
	}
	if !peerFound {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("rejecting sync device request accepted that does not contain the peer in the device group")
		b.ui.SyncDeviceProfileRefused("its profile does not include the device that answered")
		return nil, false
	}

	// Save the new profile
	if sdra.Settings == nil {
		sdra.Settings = defaultProfileSettings()
	}
	sdra.Profile.ProfileSettings = sdra.Settings
	sdra.Profile.OpenDM = true
	sdra.Profile.IntroductionMethod = userIntroductionProfile
	sdra.Profile.IntroductionTime = time.Now().Unix()
	syncRequestCancelMutex.Lock()
	if syncRequestsCancelled != cancelled {
		syncRequestCancelMutex.Unlock()
		log.WithFields(log.Fields{
			"peer": peer,
		}).Info("ignoring the answer to a sync device request that was cancelled while the answer was being checked")
		return nil, false
	}
	err = b.database.Create(&sdra.Profile).Error
	syncRequestCancelMutex.Unlock()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving new sync profile")
	}
	b.applyCarriedDeviceRevocations(peer, sdra.Profile)
	b.broadcast(&sdra.Profile.Devices[localDeviceIndex])
	b.createAndShareDeviceECDHKey()

	// Collect the sync devices for the UI from the database so any carried revocations are reflected
	var storedDevices []device
	err = b.database.Where("user_id = ?", sdra.Profile.ID).Find(&storedDevices).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error loading sync devices after applying carried revocations")
	}
	var devices []Device
	for _, dev := range storedDevices {
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
			b.devicePool.revokedMutex.Lock()
			b.devicePool.revokedDevices[dev.Address] = true
			b.devicePool.revokedMutex.Unlock()
		}
	}

	// Inform the UI
	b.ui.SyncDeviceRequestAccepted(sdra.Profile.exportedUser(), devices, sdra.References)

	// Connect to any other sync devices now
	b.auditPeers()
	b.updateUserOnlineStatus(peer)

	// Mark that we're now waiting for a catch up for this device
	if sdra.References {
		setWaitingForInitialSyncFrom(peer)
	}

	return nil, false
}
