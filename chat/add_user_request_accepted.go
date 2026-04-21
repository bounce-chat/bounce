package chat

import (
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/zeebo/blake3"
)

// An add user request accepted frame is sent from the offering device and includes their half of the signatures
// in an add user.  When this is received the requester user can create their half of the signatures and broadcast
// a complete add user.
type addUserRequestAccepted struct {
	OfferUser      []byte
	OfferSignature []byte // signature of the requester user by the offer user's device
}

func (aura *addUserRequestAccepted) getType() uint16 {
	return typeAddUserRequestAccepted
}

func (aura *addUserRequestAccepted) getPayload() []byte {
	bytes, err := msgpack.Marshal(aura)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal add user request accepted")
	}
	return bytes
}

func (b *Bounce) handleAddUserRequestAccepted(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	// Unmarshal the payload
	var aura addUserRequestAccepted
	err := msgpack.Unmarshal(payload, &aura)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling add user request accepted")
		return nil, false
	}

	// Unmarshal the offer user
	var offerUser user
	err = msgpack.Unmarshal(aura.OfferUser, &offerUser)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling offer user while handling add user request accepted")
		return nil, false
	}

	// Make sure this profile has a valid device group
	if !b.hasValidDeviceGroup(offerUser) {
		log.Error("add user request accepted contains offer user with invalid device group")
		return nil, false
	}

	// Re-generate our user that was originally sent in the request
	requesterUser, ok := b.currentUser()
	if !ok {
		log.Error("cannot handle add user request accepted when no profile exists")
		return nil, false
	}
	requesterBytes, err := msgpack.Marshal(requesterUser)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal requester user while handling add user request accepted")
	}
	requesterUserHash := blake3.Sum256(requesterBytes)

	// Make sure the offer signature is valid
	ok = b.network.VerifySignature(peer, requesterUserHash[:], aura.OfferSignature)
	if !ok {
		log.Warn("ignoring add user request accepted that contains invalid offer signature")
		return nil, false
	}

	// Create and marshal the new add user
	offerUserHash := blake3.Sum256(aura.OfferUser)
	newUser := addUser{
		ID:                 uuid.New(),
		Xor:                xor(requesterUser.ID, offerUser.ID),
		Timestamp:          time.Now().Unix(),
		OfferUser:          aura.OfferUser,
		RequesterUser:      requesterBytes,
		OfferDevice:        peer,
		RequesterDevice:    b.network.Address(),
		OfferSignature:     aura.OfferSignature,
		RequesterSignature: b.network.Sign(offerUserHash[:]),
	}
	newUserBytes, err := msgpack.Marshal(&newUser)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling add user")
		return nil, false
	}

	// Use the add user handler to save and broadcast
	au, _ := b.handleAddUser(b.network.Address(), newUserBytes, false)
	if au == nil {
		log.Error("failed to create add user frame from add user request accepted")
	} else {
		b.broadcast(au)
	}

	return nil, false
}
