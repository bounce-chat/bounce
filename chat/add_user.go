package chat

import (
	"sync"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
)

//
// An addUser structure contains proof that two users became friends
//
type addUser struct {
	ID                 uuid.UUID
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

func (au *addUser) getDestination(_ uuid.UUID) uuid.UUID {
	// TODO: compute counterparty
	return uuid.Nil
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

func (b *bounce) handleAddUser(peer string, payload []byte) {
	// TODO: handle it by validating everything, figuring out which user isn't us, then saving the other user, sending references
}

func (b *bounce) applyAddUser(au addUser) error {
	return nil
}

func (b *bounce) validFriendAddition(u user) bool {
	return false
}
