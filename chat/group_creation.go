package chat

import (
	"errors"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// A group creation captures the original state of a group.  This is the original source of truth on the structure of a group, and all
// modifications from this point on are done with updateGroup frames.  The ID of a group is determined by a hash of the original
// marshalled group that is contained in this structure.  This prevents any modification to the group during broadcast, as future frames
// are referencing this group via a hash of it's orignal state.
type groupCreation struct {
	SignedFrame
	ID        ID `gorm:"primary_key;"`
	Nonce     nonce
	Timestamp int64
	SavedAt   int64  `msgpack:"-"`
	Data      []byte `gorm:"not null"` // Original group structure, msgpacked
}

func (gc *groupCreation) getNonce() nonce {
	return gc.Nonce
}

func (gc *groupCreation) generateID() ID {
	return newContentHasher(typeGroupCreation, gc.Nonce).
		int64(gc.Timestamp).
		bytes(gc.Data).
		generate()
}

func (gc *groupCreation) BeforeCreate(tx *gorm.DB) error {
	if !idMatchesContent(gc) {
		return errors.New("group creation ID is not generated from its content")
	}
	gc.SavedAt = time.Now().Unix()
	return nil
}

func (gc *groupCreation) AfterDelete(tx *gorm.DB) error {
	if gc.ID == NilID {
		return nil
	}
	return tx.Clauses(clause.Returning{}).Where("frame_id = ? AND frame_type = ?", gc.ID, typeGroupCreation).Delete(&deliveryRecord{}).Error
}

func (gc *groupCreation) getID() ID {
	return gc.ID
}

func (gc *groupCreation) getScope(_ ID) int {
	return scopeGroupWithInvites
}

func (gc *groupCreation) getDestination(_ ID) ID {
	return gc.ID
}

func (gc *groupCreation) getType() uint16 {
	return typeGroupCreation
}

func (gc *groupCreation) getPayload() []byte {
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

	return bytes
}

func (gc *groupCreation) getAuthor() ID {
	// Since group creations will never be broadcast globally, we don't
	// need to accurately report the author
	return NilID
}

func (gc *groupCreation) getTimestamp() int64 {
	return gc.Timestamp
}

func (gc *groupCreation) getSavedAt() int64 {
	return gc.SavedAt
}

func (b *Bounce) handleGroupCreation(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	groupMutex.Lock()
	defer groupMutex.Unlock()

	// Verify and unpack the signed container
	sc, err := b.unpackSignedContainer(typeGroupCreation, payload)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unpacking signed container for group creation")
		return nil, false
	}
	var gc groupCreation
	err = msgpack.Unmarshal(sc.Payload, &gc)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling group creation")
		return nil, false
	}
	gc.OriginalPayload = sc.Payload
	gc.Signature = sc.Signature
	gc.Signer = sc.Signer

	// Ignore group creations for blocked groups
	for _, blockedGroup := range b.blockedGroups() {
		if gc.ID == blockedGroup {
			return nil, false
		}
	}

	// A creation carries one user record, the creator's, so its size is bounded the same way an invite's is.  The
	// largest legitimate creation, a creator with the maximum number of devices and the longest names, is a little
	// over half of this.  Checked before the blob is decoded or hashed, so an oversized one costs nothing.
	if len(gc.Data) > maximumUserRecordBytes {
		log.WithFields(log.Fields{
			"peer": peer,
			"size": len(gc.Data),
		}).Warn("rejecting group creation that is too large")
		return nil, false
	}

	// Make sure the ID of this group creation is generated from its content
	if !idMatchesContent(&gc) {
		log.WithFields(log.Fields{
			"peer":       peer,
			"id":         gc.getID(),
			"content_id": gc.generateID(),
		}).Error("rejecting group creation with an ID not generated from its content")
		return nil, false
	}

	// Unmarshall the group
	var g group
	err = msgpack.Unmarshal(gc.Data, &g)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling group")
		return nil, false
	}

	// Assign the group ID as the group creation ID
	g.ID = gc.ID

	// Make sure the timestamps match
	if gc.Timestamp != g.CreatedAt {
		log.WithFields(log.Fields{
			"timestamp":        gc.Timestamp,
			"group_created_at": g.CreatedAt,
		}).Error("rejection group creation with timestamp mismatch")
		return nil, false
	}

	// Make sure this group has one user, who is also the admin and creator
	if len(g.Users) != 1 {
		log.Warn("ignoring group creation that contains more than one user")
		return nil, false
	}
	if g.Users[0].ID != g.CreatedBy {
		log.Warn("ignoring group creation that does not include the creator as the only user in the group")
		return nil, false
	}
	if g.Users[0].ID.String() != g.Admins {
		log.Warn("ignoring group creation that does not have the only user as the only admin")
		return nil, false
	}

	// Make sure that one of the devices in this group signed the creation of this group, and check if we're in the group
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
		return nil, false
	}

	// Check that the creator user has valid fields and a valid device group
	if err := validUserRecord(g.Users[0]); err != nil {
		log.WithFields(log.Fields{
			"peer":    peer,
			"user_id": g.Users[0].ID,
			"error":   err.Error(),
		}).Warn("ignoring group creation that contains invalid user record")
		return nil, false
	}
	if !b.hasValidDeviceGroup(g.Users[0]) {
		log.WithFields(log.Fields{
			"peer":    peer,
			"user_id": g.Users[0].ID,
		}).Warn("ignoring group that contains user with invalid device group")
		return nil, false
	}

	// If we already know about this group, ack it, mark as delivered, and return
	var existingGroupCreation groupCreation
	err = b.database.Where("id = ?", gc.ID).First(&existingGroupCreation).Error
	if err == nil {
		return &existingGroupCreation, false
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error looking up group creation")
	}

	// Save the group creation in the database
	err = b.database.Create(&gc).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error saving new group creation")
		return nil, false
	}

	// Ack and delivery track all of the devices included in this group creation
	for _, u := range g.Users {
		for _, dev := range u.Devices {
			go b.sendAck(peer, typeDevice, dev.ID)
			b.markDeliveredTo(&dev, peer)
		}
	}

	// Start tracking this group's state in the consensus store, and notify
	// the UI / update the database if this in real time
	b.reloadGroupConsensus(g.ID)

	// If we're being re-added to this group, clean up any custom scopes from our past removal
	err = b.rescopeIfReAddedToGroup(g.ID)
	if err != nil {
		log.WithFields(log.Fields{
			"group_id": g.ID,
			"error":    err.Error(),
		}).Error("error cleaning up custom scopes when re-added to group")
	}

	if !catchUp {
		b.writeGroupConsensus(g.ID)
	}

	return &gc, true
}

func (b *Bounce) groupCreationEmbeddedGroup(groupID ID) (group, error) {
	var gc groupCreation
	err := b.database.First(&gc, "id = ?", groupID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return group{}, err
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up group creation")
		}
	}

	var g group
	err = msgpack.Unmarshal(gc.Data, &g)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling group in group creation")
		return group{}, err
	}
	g.ID = groupID

	return g, nil
}
