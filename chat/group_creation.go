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

type groupCreation struct {
	ID           uuid.UUID `gorm:"type:uuid;primary_key;"`
	Timestamp    int64
	Data         []byte // Original group structure, msgpacked
	Signer       string `msgpack:"-"`
	Payload      []byte `msgpack:"-"` // TODO: rename to marshalled or something
	Signature    []byte `msgpack:"-"`
	payload      []byte
	payloadMutex sync.Mutex
}

func (gc *groupCreation) BeforeCreate(tx *gorm.DB) error {
	// TODO: enforce that the group contained in this structure has the same UUID as the gc, and it isn't nil

	return nil
}

// TODO: after delete, cascade delete of all group updates and messages
// could also use db.Select(clause.Associations).Delete(&group)
// https://gorm.io/docs/associations.html#Delete-with-Select

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
			Payload:   gc.Payload,
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
	gc.Payload = sc.Payload
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

	// If we already know about this group, ack it and return
	var existingGroupCreation groupCreation
	err = b.database.Where("id = ?", gc.ID).First(&existingGroupCreation).Error // TODO: use count or only select ID or something?
	if err == nil {
		// We already know about this group, just mark it as delivered to the peer
		// TODO: ack
		b.markDeliveredTo(&gc, peer)
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
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

		// Save all of the structures in this group, creating any new users or devices as needed
		userIDs := []uuid.UUID{}
		err = b.database.Transaction(func(tx *gorm.DB) error {
			for _, u := range g.Users {
				// TODO: if the users are new, shouldn't we ack them as well?
				// TODO: can I just save the users and have their devices auto-save without conflict?
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

		// TODO: notify the device pool to connect right away.  groupConnectionDesired()?

		go b.broadcast(&gc)

		// We might be learning about the creation of a group that originally didn't include us, but that we were later added to.
		// In that case it doesn't make sense to inform the UI about this group until we're added to it.
		if b.userIsInGroup(b.currentUserID(), g.ID) {
			b.userInterface.NewGroupChat(Group{
				ID:      g.ID,
				Name:    g.Name,
				UserIDs: userIDs,
			})
		}
	} else {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error looking up group")
	}

	// Ack the group
	go b.sendDirectAck(peer, &ack{
		GroupCreations: gc.ID.String(),
	})
}
