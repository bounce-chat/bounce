package chat

import (
	"errors"
	"strings"
	"sync"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
)

type syncDeviceRequest struct {
	Signature    []byte
	Secret       string
	payload      []byte
	payloadMutex sync.Mutex
}

func (sdr *syncDeviceRequest) getID() uuid.UUID {
	return uuid.Nil
}

func (sdr *syncDeviceRequest) getScope(_ uuid.UUID) int {
	return scopeDevice
}

func (sdr *syncDeviceRequest) getDestination(myID uuid.UUID) uuid.UUID {
	return uuid.Nil
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

func (sdr *syncDeviceRequest) deliveryTrackingSupported() bool {
	return false
}

func (b *bounce) requestToSync(data string) error {
	if _, exists := b.currentUser(); exists {
		return errors.New("profile already exists")
	}

	parts := strings.Split(data, ":")
	if len(parts) != 2 {
		return errors.New("invalid sync data")
	}

	address := parts[0]
	// TODO: Check if valid network address
	secret := parts[1]

	conn, err := b.network.Dial(address)
	if err != nil {
		return errors.New("could not connect to device")
	}
	b.insertConnectionIntoDevicePool(conn)

	rd := b.getRemoteDevice(address)
	rd.messages <- &syncDeviceRequest{
		Signature: b.network.Sign([]byte(address)),
		Secret:    secret,
	}
	return nil
}

func (b *bounce) handleSyncDeviceRequest(peer string, payload []byte) {
	// Mutex lock prcessing to enure an offer can only be used once
	b.syncDeviceRequest.Lock()
	defer b.syncDeviceRequest.Unlock()

	// Unmarshal the payload
	var sdr syncDeviceRequest
	err := msgpack.Unmarshal(payload, &sdr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling sync device request")
		return
	}

	// We'll be responding directly to the remote device without using the
	// broadcast function since the destinatino device doesn't yet exist
	rd := b.getRemoteDevice(peer)

	// Make sure we've got an offer out with this secret
	var offer syncDeviceOffer
	err = b.database.First(&offer, "secret = ?", sdr.Secret).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"peer": peer,
			}).Warn("peer sent a sync device request with an invalid secret")
			rd.messages <- &syncDeviceRequestRejected{}
			return
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

	// Validate the signature is the peer signing this device
	if !b.network.VerifySignature(peer, []byte(b.network.Address()), sdr.Signature) {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("peer sent a sync device request with a valid secret but invalid signature")
		rd.messages <- &syncDeviceRequestRejected{}
		return
	}

	// Look up our user
	profile, exists := b.currentUser()
	if !exists {
		log.Error("cannot accept new sync device when no profile exists")
		return
	}

	// Make sure we don't already know this device belongs to another user
	dev, exists := b.getDeviceFromAddress(peer)
	if exists && dev.UserID != profile.ID {
		log.WithFields(log.Fields{
			"peer": peer,
			"user": dev.UserID,
		}).Warn("a peer that is known to belong to another user sent a valid sync device request, ignoring")
		return
	}

	if exists {
		// This is already a known sync device.  The device must be requesting to sync again because something went
		// wrong on their end during the process.  That's fine, everything about this device has been validated in
		// the past, so we just send our information over again.
		rd.messages <- &syncDeviceRequestAccepted{
			Profile:     profile,
			SyncDevices: profile.Devices,
		}

		// TODO: anything that we think we've delivered to this device we can no longer be sure was delivered, wipe
		// all delivery records before the reference flow
	} else {
		// Save this new device in our database
		newDevice := device{
			UserID:  b.currentUserID(),
			Address: peer,
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

		// Accept this device as a new sync device by responding with our updated profile information
		// that includes this new device
		rd.messages <- &syncDeviceRequestAccepted{
			Profile:     profile,
			SyncDevices: append(profile.Devices, newDevice),
		}

		// Tell the UI that we've accepted the sync device
		b.userInterface.NewSyncDeviceAdded()

		// Tell everyone about the new device
		b.markDeliveredTo(&newDevice, peer)
		b.broadcast(&newDevice)
	}

	// Send a reference offer to the new device
	b.sendReferences(peer)
}
