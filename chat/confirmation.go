package chat

import (
	"errors"
	"sync"
	"time"

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
	CustomScope   uuid.UUID `msgpack:"-"`
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

func (c *confirmation) AfterDelete(tx *gorm.DB) error {
	return tx.Where("frame_id = ? AND frame_type = ?", c.ID, typeConfirmation).Delete(&deliveryRecord{}).Error
}

func (c *confirmation) getID() uuid.UUID {
	return c.ID
}

func (c *confirmation) getScope(_ uuid.UUID) int {
	if c.CustomScope != uuid.Nil {
		return scopeCustom
	}

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

func (b *bounce) handleConfirmation(peer string, payload []byte, catchUp bool) broadcastable {
	groupMutex.Lock()
	defer groupMutex.Unlock()

	// Unmarshal the confirmation
	var c confirmation
	err := msgpack.Unmarshal(payload, &c)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling confirmation")
		return nil
	}

	// Check if we already have this confirmation
	var existingConfirmation confirmation
	err = b.database.Where("id = ?", c.ID).First(&existingConfirmation).Error
	if err == nil {
		return &existingConfirmation
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up confirmation")
	}

	// Validate the signature
	valid := b.network.VerifySignature(c.SigningDevice, c.UpdateGroupID[:], c.Signature)
	if !valid {
		log.WithFields(log.Fields{
			"update_group_id": c.UpdateGroupID,
			"confirmation_id": c.ID,
			"signing_device":  c.SigningDevice,
		}).Warn("ignoring confirmation with invalid signature")
		return nil
	}

	// Look up the update group that is being signed
	var ug updateGroup
	err = b.database.Where("id = ?", c.UpdateGroupID).First(&ug).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"update_group_id": c.UpdateGroupID,
				"confirmation_id": c.ID,
			}).Warn("ignoring confirmation for unknown update group")
			return nil
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up update group")
		}
	}
	c.Destination = ug.Target
	c.CustomScope = ug.CustomScope

	// Look up and assign the user who signed this confirmation
	dev, ok := b.getDeviceFromAddress(c.SigningDevice)
	if !ok {
		log.WithFields(log.Fields{
			"update_group_id": c.UpdateGroupID,
			"confirmation_id": c.ID,
			"signing_device":  c.SigningDevice,
		}).Warn("ignoring confirmation from unknown device")
		return nil
	}
	c.Author = dev.UserID

	// Make sure the user signing this confirmation was a member of the group for this update, unless the update group in question
	// has a custom scope, in which case this update removed us from the group
	if ug.CustomScope == uuid.Nil && !b.isMemberOfGroupForUpdate(c.Author, ug.Target, ug.ID) {
		log.WithFields(log.Fields{
			"update_group_id": c.UpdateGroupID,
			"confirmation_id": c.ID,
			"author":          c.Author,
		}).Warn("ignoring confirmation signed by user who was not a member of the group during the update")
		return nil
	}

	// Save it
	err = b.database.Create(&c).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving confirmation")
	}

	// Custom scoped updates remove us from a group, no need to update consensus there
	if ug.CustomScope == uuid.Nil {
		// Update the group state without commiting to the database or sending to the UI
		b.reloadGroupConsensusSince(ug.Target, ug.Timestamp)

		if !catchUp {
			// Update the group state in the database and UI
			b.writeGroupConsensus(ug.Target)
		}

	}
	return &c
}

func (b *bounce) sendConfirmation(ug updateGroup) {
	// Check if we already confirmed this update
	var existingConfirmation confirmation
	err := b.database.Where("signing_device = ? AND update_group_id = ?", b.network.Address(), ug.ID).First(&existingConfirmation).Error
	if err == nil {
		// We already signed this update group
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up confirmation")
	}

	// Create a confirmation
	c := confirmation{
		ID:            uuid.New(),
		UpdateGroupID: ug.ID,
		Destination:   ug.Target,
		Author:        b.currentUserID(),
		SigningDevice: b.network.Address(),
		Signature:     b.network.Sign(ug.ID[:]),
		Timestamp:     time.Now().Unix(),
	}

	// Save it
	err = b.database.Create(&c).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving confirmation")
	}

	// Broadcast it
	b.broadcast(&c)
}
