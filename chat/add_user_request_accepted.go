package chat

import (
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"github.com/zeebo/blake3"
)

type addUserRequestAccepted struct {
	OfferUser      []byte
	OfferSignature []byte // signature of the requester user by the offer user's device
	payload        []byte
	payloadMutex   sync.Mutex
}

func (aura *addUserRequestAccepted) getID() uuid.UUID {
	return uuid.Nil
}

func (aura *addUserRequestAccepted) getScope(_ uuid.UUID) int {
	return scopeDevice
}

func (aura *addUserRequestAccepted) getDestination(_ uuid.UUID) uuid.UUID {
	return uuid.Nil
}

func (aura *addUserRequestAccepted) getType() uint16 {
	return typeAddUserRequestAccepted
}

func (aura *addUserRequestAccepted) getPayload() []byte {
	aura.payloadMutex.Lock()
	defer aura.payloadMutex.Unlock()

	if len(aura.payload) == 0 {
		bytes, err := msgpack.Marshal(aura)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal add user request accepted")
		}
		aura.payload = bytes
	}
	return aura.payload
}

func (b *bounce) handleAddUserRequestAccepted(peer string, payload []byte) {
	// Unmarshal the payload
	var aura addUserRequestAccepted
	err := msgpack.Unmarshal(payload, &aura)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling add user request accepted")
		return
	}

	// Make sure this profile has a valid device group
	//if !b.hasValidDeviceGroup(aura.Profile) {
	//	log.Error("aura contains profile with invalid device group")
	//	return
	//}
	requesterUser, ok := b.currentUser()
	if !ok {
		log.Error("cannot handle add user request accepted when no profile exists")
		return
	}
	requesterBytes, err := msgpack.Marshal(requesterUser)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal requester user while handling add user request accepted")
	}
	requesterUserHash := blake3.Sum256(requesterBytes)

	// TODO: make sure offer signature matches this marshal

	var offerUser user
	err = msgpack.Unmarshal(aura.OfferUser, &offerUser)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling offer user while handling add user request accepted")
		return
	}

	newUser := addUser{
		ID:                 uuid.New(),
		Xor:                xor(requesterUser.ID, offerUser.ID),
		Timestamp:          time.Now().Unix(),
		OfferUser:          aura.OfferUser,
		RequesterUser:      requesterBytes,
		OfferDevice:        peer,
		RequesterDevice:    b.network.Address(),
		OfferSignature:     aura.OfferSignature,
		RequesterSignature: b.network.Sign(requesterUserHash[:]), // TODO: wrong?
	}

	newUserBytes, err := msgpack.Marshal(&newUser)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling add user")
		return
	}
	b.handleAddUser(b.network.Address(), newUserBytes)
}
