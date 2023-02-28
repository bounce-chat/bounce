package chat

import (
	"sync"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"github.com/zeebo/blake3"
)

var handleAddUsersMutex sync.Mutex

//
// An addUser structure contains proof that two users became friends
//
type addUser struct {
	ID                 uuid.UUID `gorm:"type:uuid;primary_key;"`
	Xor                uuid.UUID
	Timestamp          int64
	OfferUser          []byte
	RequesterUser      []byte
	OfferDevice        string
	RequesterDevice    string
	OfferSignature     []byte
	RequesterSignature []byte
	payload            []byte
	payloadMutex       sync.Mutex
}

func (au *addUser) getID() uuid.UUID {
	return au.ID
}

func (au *addUser) getScope(_ uuid.UUID) int {
	return scopeUser
}

func (au *addUser) getDestination(myID uuid.UUID) uuid.UUID {
	return xor(myID, au.Xor)
}

func (au *addUser) getType() uint16 {
	return typeAddUser
}

func (au *addUser) getPayload() []byte {
	au.payloadMutex.Lock()
	defer au.payloadMutex.Unlock()

	if len(au.payload) == 0 {
		bytes, err := msgpack.Marshal(au)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal add user")
		}
		au.payload = bytes
	}
	return au.payload
}

func (au *addUser) getTimestamp() int64 {
	return au.Timestamp
}

func (b *bounce) handleAddUser(peer string, payload []byte) {
	handleAddUsersMutex.Lock()
	defer handleAddUsersMutex.Unlock()

	// Unmarshal the structure
	var au addUser
	err := msgpack.Unmarshal(payload, &au)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling add user")
		return
	}

	// Ensure that all the signatures are correct
	offerUserHash := blake3.Sum256(au.OfferUser)
	ok := b.network.VerifySignature(au.RequesterDevice, offerUserHash[:], au.RequesterSignature)
	if !ok {
		log.Warn("add user has invalid requester signature")
		return

	}
	requesterUserHash := blake3.Sum256(au.RequesterUser)
	ok = b.network.VerifySignature(au.OfferDevice, requesterUserHash[:], au.OfferSignature)
	if !ok {
		log.Warn("add user has invalid offer signature")
		return
	}

	// Unmarshal the users in side the structure
	var offerUser user
	err = msgpack.Unmarshal(au.OfferUser, &offerUser)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling offer user inside of add user")
		return
	}
	var requesterUser user
	err = msgpack.Unmarshal(au.RequesterUser, &requesterUser)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling requester user inside of add user")
		return
	}

	// Make sure the xor matches the users
	if au.Xor != xor(offerUser.ID, requesterUser.ID) {
		log.WithFields(log.Fields{
			"xor":               au.Xor,
			"actual_xor":        xor(offerUser.ID, requesterUser.ID),
			"offer_user_id":     offerUser.ID,
			"requester_user_id": requesterUser.ID,
		}).Warn("rejecting add user with invalid xor")
		return
	}

	// Ensure that the devices that did the signing are part of the users in the structure
	offerDeviceFound := false
	for _, dev := range offerUser.Devices {
		if offerDeviceFound {
			continue
		}
		if dev.Address == au.OfferDevice {
			offerDeviceFound = true
		}
	}
	if !offerDeviceFound {
		log.Warn("add user offer signature not made by a device owned by the offer user")
		return
	}
	requesterDeviceFound := false
	for _, dev := range requesterUser.Devices {
		if requesterDeviceFound {
			continue
		}
		if dev.Address == au.RequesterDevice {
			requesterDeviceFound = true
		}
	}
	if !requesterDeviceFound {
		log.Warn("add user requester signature not made by a device owned by the requester user")
		return
	}

	// Figure out which user is not us
	var counterparty user
	if offerUser.ID == b.currentUserID() && requesterUser.ID != b.currentUserID() {
		counterparty = requesterUser
	} else if requesterUser.ID == b.currentUserID() && offerUser.ID != b.currentUserID() {
		counterparty = offerUser
	} else {
		log.Warn("add user does not contain us and someone else")
		return
	}

	// TODO: see if we can learn anything about new sync devices that belong to us from this?
	// 	would those actually be replicated during the reference flow anyway?
	//	either way, validate that our user is correct

	// Make sure the device that is sending us this makes sense
	peerDevice, exists := b.getDeviceFromAddress(peer)
	if exists && !(b.isSyncDevice(peerDevice) || peerDevice.UserID == counterparty.ID) {
		log.WithFields(log.Fields{
			"peer":            peer,
			"counterparty_id": counterparty.ID,
		}).Warn("add user came from unexpected device, ignoring")
		return
	}

	// Make sure that this new user has a valid device group
	if !b.hasValidDeviceGroup(counterparty) {
		log.Warn("rejecting add user with invalid device group")
		return
	}

	// TODO: validate the new user (doesn't conflict with existing users/devices)

	// Save the addUser
	err = b.database.Create(&au).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving add user")
	}

	// Save the new user
	// TODO: save anything that is missing about the user

	// Do a reference flow with this peer
	go b.sendReferences(peer)

	// Connect to this new user if needed
	go b.auditPeers()

	// Broadcast it
	go b.broadcast(&au)

	// Inform the UI that a new friend has been added
	// TODO: only if this user is actually new
	//b.userInterface.FriendAdded(User{
	//	ID:   requesterUser.ID,
	//	Name: requesterUser.Name,
	//})
}
