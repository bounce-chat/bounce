package chat

import (
	"errors"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// A confirmation is a signature of an update group from a device which is broadcast to the entire group.
// This is used to establish which update groups are to be applied to a group in the case of a conflict
// and reduce the risk of a malicious former admin manipulating the update history.
type confirmation struct {
	cachedEncoding
	ID            uuid.UUID `gorm:"type:uuid;primary_key;"`
	UpdateGroupID uuid.UUID
	Destination   uuid.UUID `msgpack:"-"`
	Author        uuid.UUID `msgpack:"-"`
	CustomScope   uuid.UUID `msgpack:"-"`
	SigningDevice string
	Signature     []byte
	Timestamp     int64
	SavedAt       int64 `msgpack:"-"`
}

func (c *confirmation) BeforeCreate(tx *gorm.DB) error {
	if c.ID == uuid.Nil {
		return errors.New("confirmation must have an ID assigned before creation")
	}
	c.SavedAt = time.Now().Unix()
	return nil
}

func (c *confirmation) AfterDelete(tx *gorm.DB) error {
	if c.ID == uuid.Nil {
		return nil
	}
	return tx.Clauses(clause.Returning{}).Where("frame_id = ? AND frame_type = ?", c.ID, typeConfirmation).Delete(&deliveryRecord{}).Error
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

func (c *confirmation) getSavedAt() int64 {
	return c.SavedAt
}

func (b *Bounce) handleConfirmation(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	groupMutex.Lock()
	defer groupMutex.Unlock()

	// Unmarshal the confirmation
	var c confirmation
	err := msgpack.Unmarshal(payload, &c)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling confirmation")
		return nil, false
	}

	// Ignore anything from a blocked user
	if blockedUser(c.Author) {
		log.WithFields(log.Fields{
			"id":     c.ID,
			"author": c.getAuthor(),
		}).Warn("ignoring confirmation from blocked user")

		if peerDev, ok := b.getDeviceFromAddress(peer); ok {
			if !blockedUser(peerDev.UserID) {
				go b.sendAck(peer, typeConfirmation, c.ID)
			}
		}
		return nil, false
	}

	// Check if we already have this confirmation
	var existingConfirmation confirmation
	err = b.database.Where("id = ?", c.ID).First(&existingConfirmation).Error
	if err == nil {
		return &existingConfirmation, false
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
		return nil, false
	}

	// Look up and assign the user who signed this confirmation
	dev, ok := b.getDeviceFromAddress(c.SigningDevice)
	if !ok {
		log.WithFields(log.Fields{
			"update_group_id": c.UpdateGroupID,
			"confirmation_id": c.ID,
			"signing_device":  c.SigningDevice,
		}).Warn("ignoring confirmation from unknown device")
		return nil, false
	}
	c.Author = dev.UserID

	// Look up the update group that is being signed
	var ug updateGroup
	err = b.database.Where("id = ?", c.UpdateGroupID).First(&ug).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// We have a confirmation for an update group that we don't have yet, save the confirmation
			// so that it can be taken into account whenever the update group shows up
			err = b.database.Create(&c).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error saving confirmation")
			}
			// We can't broadcast it yet without a destination, but we do manually ack to the peer that send it
			b.markDeliveredTo(&c, peer)
			go b.sendAck(peer, typeConfirmation, c.ID)
			return nil, false
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up update group")
		}
	}
	c.Destination = ug.Target
	c.CustomScope = ug.CustomScope

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
	return &c, true
}

func (b *Bounce) sendConfirmation(ug updateGroup) {
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
