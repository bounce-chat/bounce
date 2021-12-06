package chat

import (
	"errors"
	"strings"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
)

type syncDeviceRequest struct {
	Name      string
	Signature []byte
	Secret    string
	payload   []byte
}

func (sdr *syncDeviceRequest) getScope() int {
	return scopeDevice
}

func (sdr *syncDeviceRequest) getDestination(myID uuid.UUID) uuid.UUID {
	return uuid.Nil
}

func (sdr *syncDeviceRequest) getType() uint16 {
	return typeSyncDeviceRequest
}

func (sdr *syncDeviceRequest) getPayload() []byte {
	if len(sdr.payload) == 0 {
		bytes, err := msgpack.Marshal(sdr)
		if err != nil {
			// TODO: how to handle?
		}
		sdr.payload = bytes
	}
	return sdr.payload
}

func (sdr *syncDeviceRequest) isAlreadyDeliveredTo(address string) bool {
	return false
}

func (b *bounce) requestToSync(data string) error {
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
	err = b.database.Find(&offer, "secret = ?", sdr.Secret).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// This secret doesn't match an offer we've made, reject the request
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
			"error": err.Error(),
		}).Fatal("database error deleting sync device offer after use")
	}

	// Validate the signature is the peer signing this device
	// TODO

	// Save this new device in our database
	newDevice := device{
		Name:    sdr.Name,
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
	profile, exists := b.currentUser()
	if !exists {
		log.Fatal("cannot accept new sync device when no profile exists")
	}
	rd.messages <- &syncDeviceRequestAccepted{
		Profile: profile,
	} // TODO: should this be acked to ensure delivery?

	// Tell the UI that we've accepted the sync device
	b.userInterface.NewSyncDeviceAdded(sdr.Name)

	// Tell the rest of our sync devices about this new sync device
	//b.broadcast(&newSyncDevice{Device: newDevice})

	// Send a reference offer to the new device
	b.sendReferences(peer)
}
