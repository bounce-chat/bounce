package chat

import (
	"errors"
	"strings"
	"sync"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"github.com/zeebo/blake3"
	"gorm.io/gorm"
)

var addUserRequestMutex sync.Mutex

type addUserRequest struct {
	Signature    []byte
	Secret       string
	User         []byte
	payload      []byte
	payloadMutex sync.Mutex
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

func (b *bounce) requestToAddUser(data string) error {
	parts := strings.Split(data, ":")
	if len(parts) != 2 {
		return errors.New("invalid sync data")
	}

	address := parts[0]
	// TODO: Check if valid network address, and check if we already know who this device is
	secret := parts[1]

	conn, err := b.network.Dial(address)
	if err != nil {
		return errors.New("could not connect to device") // TODO: ERR_CONNECTION_FAILED
	}
	b.insertConnectionIntoDevicePool(conn)

	rd := b.getRemoteDevice(address)
	rd.messages <- &addUserRequest{
		Signature: b.network.Sign([]byte(address)),
		Secret:    secret,
	}
	return nil
}

func (b *bounce) handleAddUserRequest(peer string, payload []byte) {
	// Mutex lock prcessing to enure an offer can only be used once
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

	// We'll be responding directly to the remote device without using the
	// broadcast function since the destination device doesn't yet exist
	rd := b.getRemoteDevice(peer)

	// Make sure we've got an offer out with this secret
	var offer addUserOffer
	err = b.database.First(&offer, "secret = ?", aur.Secret).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"peer": peer,
			}).Warn("peer sent an add user request with an invalid secret")
			rd.messages <- &addUserRequestRejected{}
			return
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up add user offer by secret")
		}
	}

	// TODO: enforce timestamp

	// Delete the offer
	err = b.database.Delete(&offer).Error
	if err != nil {
		log.WithFields(log.Fields{
			"id":    offer.ID,
			"error": err.Error(),
		}).Fatal("database error deleting add user offer after use")
	}

	// Validate the signature is the peer signing this device
	if !b.network.VerifySignature(peer, []byte(b.network.Address()), aur.Signature) {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("peer sent an add user request with a valid secret but invalid signature")
		rd.messages <- &addUserRequestRejected{}
		return
	}

	// Check if this device is part of a user we are already aware of
	if _, exists := b.getDeviceFromAddress(peer); exists {
		// We already have this device saved, so at some point we approved being friends with them.  This process
		// must have been interrupted on their side, so we can send them a request accepted again.
		rd.messages <- &addUserRequestAccepted{
			// TODO
		}

		err = b.database.Where("destination = ?", peer).Delete(&deliveryRecord{}).Error
		if err != nil {
			log.WithFields(log.Fields{
				"peer":  peer,
				"error": err.Error(),
			}).Error("error deleting old delivery records for user device that is re-requesting to be friends")
		}
		return
	}

	// Unmarshal the user that is requesting to be our friend
	var requesterUser user
	err = msgpack.Unmarshal(aur.User, &requesterUser)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling requester user while handing user request")
		return
	}

	// Validate that this new friend has a valid device group
	if !b.hasValidDeviceGroup(requesterUser) {
		log.WithFields(log.Fields{
			"peer":    peer,
			"user_id": requesterUser.ID,
			"name":    requesterUser.Name,
		}).Warn("rejecting friend request from user with invalid device group")
		rd.messages <- &addUserRequestRejected{}
		return
	}

	// TODO: make sure that this peer is in the user's device group

	// Check if this is a user that we're already aware of
	var existingUser user
	err = b.database.Select("id").Where("id = ?", requesterUser.ID).First(&existingUser).Error
	if err == nil {
		log.Warn("an unknown device is attempting to add a known user as a friend")
		rd.messages <- &addUserRequestRejected{}
		// TODO: is this also a case where we should allow?  friend's new device we don't know about trying to re-add us?
		return
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		// If this is a user that we are not aware of, make sure none of the devices belong to another user
		// TODO
	} else {
		log.WithFields(log.Fields{
			"user_id": requesterUser.ID,
			"error":   err.Error(),
		}).Fatal("database error looking up user")
	}

	// Construct a new addUser object for this interaction
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
	requesterUserHash := blake3.Sum256(aur.User)
	rd.messages <- &addUserRequestAccepted{
		OfferUser:      offerBytes,
		OfferSignature: b.network.Sign(requesterUserHash[:]),
	}

	// TODO: send back the addUser inside of an addUserRequestAccepted

	// Inform the UI that a new friend has been added
	//b.userInterface.FriendAdded(User{
	//	ID:   requesterUser.ID,
	//	Name: requesterUser.Name,
	//})
}

// TODO: request to add friend: make a userRequest with our whole profile (including devices), send to the string
