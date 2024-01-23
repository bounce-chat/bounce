package chat

import (
	"errors"
	"sync"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
)

//
// A confirmation is a signature of an update group from a device which is broadcast to the entire group.
// This is used to establish which update groups are to be applied to a group in the case of a conflict
// and reduce the risk of a malicious former admin manipulating the update history.
//
type confirmation struct {
	ID            uuid.UUID `gorm:"type:uuid;primary_key;"`
	UpdateGroupID uuid.UUID
	Destination   uuid.UUID `msgpack:"-"`
	Author        uuid.UUID `msgpack:"-"`
	SigningDevice string
	Signature     []byte
	Timestamp     int64
	payload       []byte
	payloadMutex  sync.Mutex
}

func (c *confirmation) BeforeCreate(tx *gorm.DB) error {
	if c.ID == uuid.Nil {
		return errors.New("confirmation must have an ID assigned before creation")
	}
	return nil
}

func (c *confirmation) getID() uuid.UUID {
	return c.ID
}

func (c *confirmation) getScope(_ uuid.UUID) int {
	return scopeGroup
}

func (c *confirmation) getDestination(_ uuid.UUID) uuid.UUID {
	return c.Destination
}

func (c *confirmation) getType() uint16 {
	return typeConfirmation
}

func (c *confirmation) getPayload() []byte {
	c.payloadMutex.Lock()
	defer c.payloadMutex.Unlock()

	if len(c.payload) == 0 {
		bytes, err := msgpack.Marshal(c)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal confirmation")
		}
		c.payload = bytes
	}
	return c.payload
}

func (c *confirmation) getAuthor() uuid.UUID {
	return c.Author
}

func (c *confirmation) getTimestamp() int64 {
	return c.Timestamp
}

func (b *bounce) handleConfirmation(peer string, payload []byte) {

}
