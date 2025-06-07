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

var syncDeviceOfferValidForSeconds = int64(300)

var syncDeviceRequestMutex sync.Mutex

var waitingForInitialSyncFrom string

//
// A sync device request is our request to join an existing profile.  We do this by sending the secret that was present in an offer,
// as well as our signature of the address of the device that made the offer.
//
type syncDeviceRequest struct {
	Signature    []byte
	Secret       string
	payload      []byte
	payloadMutex sync.Mutex
}

func (sdr *syncDeviceRequest) getType() uint16 {
	return typeSyncDeviceRequest
}

func (sdr *syncDeviceRequest) getPayload() []byte {
	sdr.payloadMutex.Lock()
	defer sdr.payloadMutex.Unlock()

	if len(sdr.payload) == 0 {
		bytes, err := msgpack.Marshal(sdr)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal sync device request")
		}
		sdr.payload = bytes
	}
	return sdr.payload
}

func (b *Bounce) handleSyncDeviceRequest(peer string, payload []byte, catchUp bool) broadcastable {
	// Mutex lock prcessing to enure an offer can only be used once
	syncDeviceRequestMutex.Lock()
	defer syncDeviceRequestMutex.Unlock()

	// Unmarshal the payload
	var sdr syncDeviceRequest
	err := msgpack.Unmarshal(payload, &sdr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling sync device request")
		return nil
	}

	// Make sure we've got an offer out with this secret
	var offer syncDeviceOffer
	err = b.database.First(&offer, "secret = ?", sdr.Secret).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"peer": peer,
			}).Warn("peer sent a sync device request with an invalid secret")
			b.sendDirect(peer, &syncDeviceRequestRejected{})
			return nil
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up sync device offer by secret")
		}
	}

	// Delete the offer
	err = b.database.Delete(&offer).Error
	if err != nil {
		log.WithFields(log.Fields{
			"id":    offer.ID,
			"error": err.Error(),
		}).Fatal("database error deleting sync device offer after use")
	}

	// Enforce the timestamp on the offer
	if time.Now().Unix() > offer.Timestamp+syncDeviceOfferValidForSeconds {
		b.sendDirect(peer, &syncDeviceRequestRejected{})
		return nil
	}

	// Validate the signature is the peer signing this device
	if !b.network.VerifySignature(peer, []byte(b.network.Address()), sdr.Signature) {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("peer sent a sync device request with a valid secret but invalid signature")
		b.sendDirect(peer, &syncDeviceRequestRejected{})
		return nil
	}

	// Look up our user
	profile, exists := b.currentUser()
	if !exists {
		log.Error("cannot accept new sync device when no profile exists")
		return nil
	}

	// Make sure we don't already know this device belongs to another user
	dev, exists := b.getDeviceFromAddress(peer)
	if exists && dev.UserID != profile.ID {
		log.WithFields(log.Fields{
			"peer": peer,
			"user": dev.UserID,
		}).Warn("a peer that is known to belong to another user sent a valid sync device request, ignoring")
		return nil
	}

	if exists {
		// This is already a known sync device.  The device must be requesting to sync again because something went
		// wrong on their end during the process.  That's fine, everything about this device has been validated in
		// the past, so we just send our information over again and drop all delivery records in case they lost data.
		err = b.database.Where("destination = ?", peer).Delete(&deliveryRecord{}).Error
		if err != nil {
			log.WithFields(log.Fields{
				"peer":  peer,
				"error": err.Error(),
			}).Error("error deleting old delivery records for sync device that is re-syncing")
		}

		b.sendDirect(peer, &syncDeviceRequestAccepted{
			Profile:    profile,
			Settings:   profile.ProfileSettings,
			References: b.hasAnyReferencesFor(peer),
		})
	} else {
		// Save this new device in our database
		newDevice := device{
			ID:        uuid.New(),
			UserID:    b.currentUserID(),
			Address:   peer,
			Timestamp: time.Now().Unix(),
			LastSeen:  time.Now().Unix(),
			Signature: &introductionSignature{
				PreexistingDevice:            b.network.Address(),
				SignatureOfNewDevice:         b.network.Sign([]byte(peer)),
				SignatureOfPreexistingDevice: sdr.Signature,
			},
		}
		err = b.database.Create(&newDevice).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error saving new sync device")
		}

		// Reload the profile to include the new device we just saved
		profile, exists = b.currentUser()
		if !exists {
			log.Fatal("profile disappeared after new sync device was saved")
		}

		// Accept this device as a new sync device by responding with our updated profile information
		// that includes this new device
		b.sendDirect(peer, &syncDeviceRequestAccepted{
			Profile:    profile,
			Settings:   profile.ProfileSettings,
			References: b.hasAnyReferencesFor(peer),
		})

		// Tell the UI that we've accepted the sync device
		b.ui.NewSyncDeviceAdded()
		b.ui.DeviceAdded(Device{
			ID:        newDevice.ID,
			Address:   newDevice.Address,
			CreatedAt: newDevice.Timestamp,
			Local:     false,
			Online:    true,
		})

		// Store that we've told this device about themselves
		b.markDeliveredTo(&newDevice, peer)

		// Tell everyone about the new device
		b.broadcast(&newDevice)
	}

	// Send a reference offer to the new device
	b.sendReferences(peer)

	return nil
}

func (b *Bounce) RequestToSync(data string) error {
	// Make sure we don't have a profile before trying to become a sync device for another profile
	if _, exists := b.currentUser(); exists {
		return errors.New("profile already exists")
	}

	// Parse the offer
	parts := strings.Split(data, ":")
	if len(parts) != 2 {
		return errors.New("invalid sync data")
	}
	address := parts[0]
	secret := parts[1]

	// Dial the offer device
	conn, err := b.network.Dial(address)
	if err != nil {
		return errors.New("could not connect to device")
	}
	b.insertConnectionIntoDevicePool(conn)

	// Send our request
	b.sendDirect(address, &syncDeviceRequest{
		Signature: b.network.Sign([]byte(address)),
		Secret:    secret,
	})

	return nil
}
