package chat

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"github.com/zeebo/blake3"
	"gorm.io/gorm"
)

var addUserRequestMutex sync.Mutex

type addUserRequest struct {
	Secret        string
	RequesterUser []byte
	payload       []byte
	payloadMutex  sync.Mutex
}

func (aur *addUserRequest) getID() uuid.UUID {
	return uuid.Nil
}

func (aur *addUserRequest) getScope(_ uuid.UUID) int {
	return scopeDevice
}

func (aur *addUserRequest) getDestination(myID uuid.UUID) uuid.UUID {
	return uuid.Nil
}

func (aur *addUserRequest) getType() uint16 {
	return typeAddUserRequest
}

func (aur *addUserRequest) getPayload() []byte {
	aur.payloadMutex.Lock()
	defer aur.payloadMutex.Unlock()

	if len(aur.payload) == 0 {
		bytes, err := msgpack.Marshal(aur)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal add user request")
		}
		aur.payload = bytes
	}
	return aur.payload
}

func (b *bounce) handleAddUserRequest(peer string, payload []byte) {
	addUserRequestMutex.Lock()
	defer addUserRequestMutex.Unlock()

	// Unmarshal the payload
	var aur addUserRequest
	err := msgpack.Unmarshal(payload, &aur)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling add user request")
		return
	}

	// Make sure we've got an offer out with this secret
	var offer addUserOffer
	err = b.database.First(&offer, "secret = ?", aur.Secret).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"peer": peer,
			}).Warn("peer sent an add user request with an invalid secret")
			b.sendDirect(peer, &addUserRequestRejected{})
			return
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up add user offer by secret")
		}
	}

	// Delete the offer
	err = b.database.Delete(&offer).Error
	if err != nil {
		log.WithFields(log.Fields{
			"id":    offer.ID,
			"error": err.Error(),
		}).Fatal("database error deleting add user offer after use")
	}

	// Enforce the timestamp on the offer
	if time.Now().Unix() > offer.Timestamp+300 { // TODO: constant
		b.sendDirect(peer, &addUserRequestRejected{})
		return
	}

	// Unmarshal the user that is requesting to be our friend
	var requesterUser user
	err = msgpack.Unmarshal(aur.RequesterUser, &requesterUser)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling requester user while handing user request")
		b.sendDirect(peer, &addUserRequestRejected{})
		return
	}

	// Validate that this new friend has a valid device group
	if !b.hasValidDeviceGroup(requesterUser) {
		log.WithFields(log.Fields{
			"peer":    peer,
			"user_id": requesterUser.ID,
			"name":    requesterUser.Name,
		}).Warn("rejecting friend request from user with invalid device group")
		b.sendDirect(peer, &addUserRequestRejected{})
		return
	}

	// Make sure that the peer that sent us this is part of the requester user's device group
	found := false
	for _, dev := range requesterUser.Devices {
		if dev.Address == peer {
			found = true
		}
	}
	if !found {
		log.Warn("add user request came from device that is not part of the requester user's device group")
		b.sendDirect(peer, &addUserRequestRejected{})
		return
	}

	// Make sure that we don't have any of these devices already associated with another user
	for _, dev := range requesterUser.Devices {
		// Addresses cannot collide
		if existingDevice, exists := b.getDeviceFromAddress(dev.Address); exists {
			if existingDevice.UserID != requesterUser.ID {
				log.WithFields(log.Fields{
					"address":        dev.Address,
					"new_user":       requesterUser.ID,
					"exisiting_user": existingDevice.UserID,
				}).Warn("rejecting add user request with device address collision with separate existing user")
				b.sendDirect(peer, &addUserRequestRejected{})
				return
			}
		}
		// Primary keys cannot collise
		var existingDevice device
		err = b.database.Where("id = ?", dev.ID).First(&existingDevice).Error
		if err == nil {
			if existingDevice.UserID != requesterUser.ID {
				log.WithFields(log.Fields{
					"id":             dev.ID,
					"new_user":       requesterUser.ID,
					"exisiting_user": existingDevice.UserID,
				}).Warn("rejecting add user with device ID collision with separate existing user")
				b.sendDirect(peer, &addUserRequestRejected{})
				return
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up device")
		}
	}

	// Accept the request and send back our user
	offerUser, ok := b.currentUser()
	if !ok {
		log.Error("cannot handle add user request when no profile exists")
		return
	}
	offerBytes, err := msgpack.Marshal(offerUser)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal offer user while handling user request")
	}
	requesterUserHash := blake3.Sum256(aur.RequesterUser)
	b.sendDirect(peer, &addUserRequestAccepted{
		OfferUser:      offerBytes,
		OfferSignature: b.network.Sign(requesterUserHash[:]),
	})
}

func (b *bounce) requestToAddUser(offer string) error {
	parts := strings.Split(offer, ":")
	if len(parts) != 2 {
		return errors.New("invalid friend request string")
	}

	address := parts[0]
	// TODO: Check if valid network address
	secret := parts[1]

	conn, err := b.network.Dial(address)
	if err != nil {
		return errors.New("could not connect to device")
	}
	b.insertConnectionIntoDevicePool(conn)

	requesterUser, ok := b.currentUser()
	if !ok {
		err := errors.New("cannot add friends when no profile exists")
		log.Error(err.Error())
		return err
	}
	requesterBytes, err := msgpack.Marshal(requesterUser)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal requester user while adding friend")
	}
	b.sendDirect(address, &addUserRequest{
		Secret:        secret,
		RequesterUser: requesterBytes,
	})

	return nil
}
