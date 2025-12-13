package chat

import (
	"errors"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"github.com/zeebo/blake3"
	"gorm.io/gorm"
)

var addUserOfferValidForSeconds = int64(300)

var addUserRequestMutex sync.Mutex

// Add user requests are frames sent by a device that has scanned another device's add user offer.  The
// device sending the request sends that secret that was offered, as well as their marshalled user.
type addUserRequest struct {
	Secret        string
	RequesterUser []byte
}

func (aur *addUserRequest) getType() uint16 {
	return typeAddUserRequest
}

func (aur *addUserRequest) getPayload() []byte {
	bytes, err := msgpack.Marshal(aur)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal add user request")
	}
	return bytes
}

func (b *Bounce) handleAddUserRequest(peer string, payload []byte, _ bool) (broadcastable, bool) {
	addUserRequestMutex.Lock()
	defer addUserRequestMutex.Unlock()

	// Unmarshal the payload
	var aur addUserRequest
	err := msgpack.Unmarshal(payload, &aur)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling add user request")
		return nil, false
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
			return nil, false
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
	if time.Now().Unix() > offer.Timestamp+addUserOfferValidForSeconds {
		b.sendDirect(peer, &addUserRequestRejected{})
		return nil, false
	}

	// Unmarshal the user that is requesting to be our friend
	var requesterUser user
	err = msgpack.Unmarshal(aur.RequesterUser, &requesterUser)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling requester user while handing user request")
		b.sendDirect(peer, &addUserRequestRejected{})
		return nil, false
	}

	// Validate that this new friend has a valid device group
	if !b.hasValidDeviceGroup(requesterUser) {
		log.WithFields(log.Fields{
			"peer":    peer,
			"user_id": requesterUser.ID,
			"name":    requesterUser.Name,
		}).Warn("rejecting friend request from user with invalid device group")
		b.sendDirect(peer, &addUserRequestRejected{})
		return nil, false
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
		return nil, false
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
				return nil, false
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
				return nil, false
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
		return nil, false
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

	return nil, false
}

func (b *Bounce) RequestToAddUser(offer string) error {
	// Parse the offer
	parts := strings.Split(offer, ":")
	if len(parts) != 2 {
		return errors.New("invalid friend request string")
	}
	address := parts[0]
	secret := parts[1]

	// Dial the offer device
	conn, err := b.network.Dial(address)
	if err != nil {
		return errors.New("could not connect to device")
	}
	b.insertConnectionIntoDevicePool(conn)

	// Get our user and marshal it
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

	// Send the request to the offer device
	b.sendDirect(address, &addUserRequest{
		Secret:        secret,
		RequesterUser: requesterBytes,
	})

	return nil
}
