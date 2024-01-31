package chat

import (
	"errors"
	"sort"
	"sync"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
)

//
// A frame contains the ID, type, and marshalled payload of any other frame
//
type frame struct {
	ID      uuid.UUID
	Type    uint16
	Payload []byte
}

//
// A catch up is a frame that is used to transport a set of frames that are chronologically ordered
//
type catchUp struct {
	Frames         []frame
	broadcastables sortableBroadcastables
	payload        []byte
	payloadMutex   sync.Mutex
}

func (cu *catchUp) getType() uint16 {
	return typeCatchUp
}

func (cu *catchUp) getPayload() []byte {
	cu.payloadMutex.Lock()
	defer cu.payloadMutex.Unlock()

	if len(cu.payload) == 0 {
		sort.Sort(cu.broadcastables)
		for _, br := range cu.broadcastables {
			cu.Frames = append(cu.Frames, frame{ID: br.getID(), Type: br.getType(), Payload: br.getPayload()})
		}

		bytes, err := msgpack.Marshal(cu)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal catch up")
		}
		cu.payload = bytes
	}
	return cu.payload
}

func (b *bounce) handleCatchUp(peer string, payload []byte) {
	// Unmarshal the catch up
	var cu catchUp
	err := msgpack.Unmarshal(payload, &cu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling catch up")
		return
	}

	// Send references for these frames into the reference engine so that we know we've received them
	// and no longer need to request them from any peers
	references := []frameReference{}
	for _, f := range cu.Frames {
		references = append(references, frameReference{FrameID: f.ID, Type: f.Type})
	}
	b.loadCatchUp(peer, references)

	// Check if we're aware of the peer identity before processing this catch up.  If we don't know
	// who this device belongs to, we should be able to learn after handling all of the frames inside
	// it and we'll want to check to make sure that happened.
	_, deviceAlreadyExists := b.getDeviceFromAddress(peer)

	// Keep track of update groups that are new
	ugs := []updateGroup{}

	// Handle reach frame in the catch up using it's handler
	handlers := b.getHandlers()
	for _, fr := range cu.Frames {
		if fr.Type == typeCatchUp {
			log.Warn("refusing to processes recursive catch up")
			return
		}

		// Update groups should not be applied in order during a catch up in case the user is removed
		// from and re-added to the same group within the catch up
		if fr.Type == typeUpdateGroup {
			newAndValid, ug := b.validateAndSaveUpdateGroup(peer, fr.Payload)
			if newAndValid {
				ugs = append(ugs, ug)
			}
			continue
		}

		handler, ok := handlers[fr.Type]
		if !ok {
			log.WithFields(log.Fields{
				"peer": peer,
				"type": fr.Type,
			}).Warn("peer sent a catch up frame type that doesn't have a handler")
			continue
		}
		handler(peer, fr.Payload)
	}

	// Update all group consensus states for groups that had an update group in this catch up
	ugGroups := map[uuid.UUID]bool{}
	for _, ug := range ugs {
		ugGroups[ug.Target] = true
	}
	for groupID, _ := range ugGroups {
		b.updateGroupConsensus(groupID)
	}
	for _, ug := range ugs {
		b.handleUpdateGroupIfApplied(peer, ug)
	}

	// We might have learned about new devices from this catch up, so we should see if there's anyone else we
	// now want to dial
	go b.auditPeers()

	// If we didn't know who this device belonged to at first, check to make sure we know who it belongs to
	// after handling all of the frames
	if !deviceAlreadyExists {
		if _, deviceNowExists := b.getDeviceFromAddress(peer); deviceNowExists {
			// An unknown device sent a catch up that included frames that prove we should add the device,
			// since we didn't initially offer references to this device we should now do so now that we
			// have context on what this device is
			go b.sendReferences(peer)
		} else {
			log.WithFields(log.Fields{
				"peer":        peer,
				"frame_count": len(cu.Frames),
			}).Warn("catch up from unknown device did not result in learning device identity")
		}
	}

	// Detect and warn about any groups that were added during this catch up that we didn't learn we're a part of
	var orphanedGroups []group
	err = b.database.
		Model(&group{}).
		Distinct().
		Select("groups.*").
		Joins("JOIN group_users ON groups.id = group_users.group_id").
		Where("? NOT IN (SELECT group_users.user_id FROM group_users)", b.currentUserID()).
		Find(&orphanedGroups).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error attempting to discover orphaned groups")
	}

	for _, og := range orphanedGroups {
		log.WithFields(log.Fields{
			"peer":     peer,
			"group_id": og.ID,
			"users":    og.Users,
		}).Warn("catch up created orphaned group")
	}
}

func (b *bounce) validateAndSaveUpdateGroup(peer string, payload []byte) (bool, updateGroup) {
	updateGroupMutex.Lock()
	defer updateGroupMutex.Unlock()

	// Unpack the signed container
	sc, err := b.unpackSignedContainer(payload)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unpacking signed container for update group")
		return false, updateGroup{}
	}
	var ug updateGroup
	err = msgpack.Unmarshal(sc.Payload, &ug)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error unmarshalling update group")
	}
	ug.OriginalPayload = sc.Payload
	ug.Signature = sc.Signature
	ug.Signer = sc.Signer

	// Make sure that the user that created this signed container is the actor
	if !b.signedByUser(sc, ug.Actor) {
		log.WithFields(log.Fields{
			"peer":           peer,
			"actor":          ug.Actor,
			"signing_device": sc.Signer,
			"group":          ug.Target,
		}).Warn("ignoring group update that was not signed by the supposed actor")
		return false, updateGroup{}
	}

	// If we already have this update, we just mark that this peer has it too and return
	var existingUG updateGroup
	err = b.database.Where("id = ?", ug.ID).First(&existingUG).Error
	if err == nil {
		b.markDeliveredTo(&existingUG, peer)
		go b.sendAck(peer, typeUpdateGroup, ug.ID)
		return false, updateGroup{}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up update group")
	}

	// Save this update
	err = b.database.Create(&ug).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving update group")
	}

	return true, ug
}

func (b *bounce) handleUpdateGroupIfApplied(peer string, ug updateGroup) {
	err := b.database.Select("applied").First(&ug, "id = ?", ug.ID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"update_group_id": ug.ID,
				"error":           err.Error(),
			}).Error("update group not found after catch up")
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up update group")
		}
	}
	if ug.Applied {
		// Update group activity
		b.updateLastGroupActivity(ug.Target, ug.Timestamp)

		// Ack it
		go b.sendAck(peer, typeUpdateGroup, ug.ID)

		// Mark that the peer that send this update already has it
		b.markDeliveredTo(&ug, peer)

		// Broadcast it
		b.broadcast(&ug)
	}
}
