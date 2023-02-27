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
	// unmarshal both users
	// check signatures
	// make sure the Xor matches
	// figure out which one is us
	// see if we can learn anything about new sync devices that belong to us from this?
	// 	would those actually be replicated during the reference flow anyway?
	// if this didn't come from a sync device, or one of the devices owned by the new user, report something weird
	// validate the new user (device group, doesn't conflict with existing devices)
	// save and apply (make saving part of applying?)
	// send references to the peer
	// audit peers
	// broadcast
}

func (b *bounce) applyAddUser(au addUser) error {
	return nil
}

func (b *bounce) validFriendAddition(u user) bool {
	return false
}
