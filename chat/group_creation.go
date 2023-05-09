package chat

import (
	"errors"
	"sync"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"github.com/zeebo/blake3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

//
// A group creation captures the original state of a group.  This is the original source of truth on the structure of a group, and all
// modifications from this point on are done with updateGroup frames.  The ID of a group is determined by a hash of the original
// marshalled group that is contained in this structure.  This prevents any modification to the group during broadcast, as future frames
// are referencing this group via a hash of it's orignal state.
//
type groupCreation struct {
	ID              uuid.UUID `gorm:"type:uuid;primary_key;"`
	Timestamp       int64
	Data            []byte `gorm:"not null"` // Original group structure, msgpacked
	Signer          string `msgpack:"-" gorm:"not null"`
	OriginalPayload []byte `msgpack:"-" gorm:"not null"`
	Signature       []byte `msgpack:"-" gorm:"not null"`
	payload         []byte
	payloadMutex    sync.Mutex
}

func (gc *groupCreation) BeforeCreate(tx *gorm.DB) error {
	if gc.ID == uuid.Nil {
		return errors.New("group creation must have an ID assigned before creation")
	}
	return nil
}

func (gc *groupCreation) AfterDelete(tx *gorm.DB) error {
	return tx.Where("frame_id = ? AND frame_type = ?", gc.ID, typeGroupCreation).Delete(&deliveryRecord{}).Error
}

func (gc *groupCreation) getID() uuid.UUID {
	return gc.ID
}

func (gc *groupCreation) getScope(_ uuid.UUID) int {
	return scopeGroup
}

func (gc *groupCreation) getDestination(_ uuid.UUID) uuid.UUID {
	return gc.ID
}

func (gc *groupCreation) getType() uint16 {
	return typeGroupCreation
}

func (gc *groupCreation) getPayload() []byte {
	gc.payloadMutex.Lock()
	defer gc.payloadMutex.Unlock()

	if len(gc.payload) == 0 {
		bytes, err := msgpack.Marshal(signedContainer{
			Payload:   gc.OriginalPayload,
			Signature: gc.Signature,
			Signer:    gc.Signer,
		})
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error marshalling group creation's signed container")
		}
		gc.payload = bytes
	}
	return gc.payload
}

func (gc *groupCreation) getAuthor() uuid.UUID {
	// Since group creations will never be broadcast globally, we don't
	// need to accurately report the author
	return uuid.Nil
}

func (gc *groupCreation) getTimestamp() int64 {
	return gc.Timestamp
}

func (b *bounce) handleGroupCreation(peer string, payload []byte) {
	groupMutex.Lock()
	defer groupMutex.Unlock()

	// Verify and unpack the signed container
	sc, err := b.unpackSignedContainer(payload)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unpacking signed container for group creation")
		return
	}
	var gc groupCreation
	err = msgpack.Unmarshal(sc.Payload, &gc)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error unmarshalling group creation")
	}
	gc.OriginalPayload = sc.Payload
	gc.Signature = sc.Signature
	gc.Signer = sc.Signer

	// Make sure the ID of this group creation matches the hash of the group data
	hasher := blake3.New()
	written, _ := hasher.Write(gc.Data)
	if written != len(gc.Data) {
		log.WithFields(log.Fields{
			"length":  len(gc.Data),
			"written": written,
		}).Fatal("failed to write all group data into hasher")
	}
	digest := hasher.Digest()
	groupHash := make([]byte, 16)
	digest.Read(groupHash)
	groupID, err := uuid.FromBytes(groupHash)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot create UUID from hash of group data")
	}
	if gc.ID != groupID {
		log.WithFields(log.Fields{
			"group_creation_id": gc.ID,
			"group_hash_id":     groupID,
		}).Error("rejecting group creation with ID that does not match hash of group data")
	}

	// Unmarshall the group
	var g group
	err = msgpack.Unmarshal(gc.Data, &g)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling group")
		return
	}

	// Assign the group ID as the group creation ID
	g.ID = gc.ID

	// Make sure the timestamps match
	if gc.Timestamp != g.CreatedAt {
		log.WithFields(log.Fields{
			"timestamp":        gc.Timestamp,
			"group_created_at": g.CreatedAt,
		}).Error("rejection group creation with timestamp mismatch")
		return
	}

	// Make sure that one of the devices in this group signed the creation of this group
	signingDeviceInGroup := false
	for _, u := range g.Users {
		for _, dev := range u.Devices {
			if dev.Address == gc.Signer {
				signingDeviceInGroup = true
			}
		}
	}
	if !signingDeviceInGroup {
		log.WithFields(log.Fields{
			"signing_device": gc.Signer,
		}).Warn("rejecting group creation not signed by any of the original devices")
		return
	}

	// Check that each user has a valid device group
	for _, u := range g.Users {
		if !b.hasValidDeviceGroup(u) {
			log.WithFields(log.Fields{
				"peer":    peer,
				"user_id": u.ID,
			}).Warn("ignoring group that contains user with invalid device group")
			return
		}
	}

	// If we already know about this group, ack it, mark as delivered, and return
	var existingGroupCreation groupCreation
	err = b.database.Where("id = ?", gc.ID).First(&existingGroupCreation).Error
	if err == nil {
		go b.sendAck(peer, typeGroupCreation, gc.ID)
		b.markDeliveredTo(&gc, peer)
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error looking up group creation")
	}

	// Save all of the structures in this group, creating any new users or devices as needed
	userIDs := []uuid.UUID{}
	err = b.database.Transaction(func(tx *gorm.DB) error {
		for _, u := range g.Users {
			for _, dev := range u.Devices {
				err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&dev).Error
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error saving device that is part of a group")
					return err
				}
			}
			err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&u).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error saving user that is part of a group")
				return err
			}
			userIDs = append(userIDs, u.ID)
		}
		err := tx.Create(&gc).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error saving new group creation")
			return err
		}
		err = tx.Create(&g).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error saving new group")
			return err
		}

		return nil
	})
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error in transaction for saving a new group")
	}

	// Ack the group
	go b.sendAck(peer, typeGroupCreation, gc.ID)

	// Mark this as delivered to this peer
	b.markDeliveredTo(&gc, peer)

	// Broadcast it
	b.broadcast(&gc)

	// We might be learning about the creation of a group that originally didn't include us, but that we were later added to.
	// In that case it doesn't make sense to inform the UI about this group until we're added to it.
	if b.userIsInGroup(b.currentUserID(), g.ID) {
		b.userInterface.NewGroupChat(Group{
			ID:      g.ID,
			Name:    g.Name,
			UserIDs: userIDs,
		})
	}

	// Notify the peering engine that we want to be connected to this group right now
	b.groupConnectionDesired(g.ID)
}
