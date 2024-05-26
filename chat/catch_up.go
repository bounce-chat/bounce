package chat

import (
	"sort"
	"sync"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
)

var catchUpMutex sync.Mutex

var allowedCatchUpFrames = map[uint16]bool{
	typeDirectMessage: true,
	typeGroupMessage:  true,
	typeUpdateDM:      true,
	typeDevice:        true,
	typeAddUser:       true,
	typeGroupCreation: true,
	typeUpdateGroup:   true,
	typeConfirmation:  true,
}

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

func (b *bounce) handleCatchUp(peer string, payload []byte, _ bool) broadcastable {
	// Unmarshal the catch up
	var cu catchUp
	err := msgpack.Unmarshal(payload, &cu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling catch up")
		return nil
	}

	// Check if we're aware of the peer identity before processing this catch up.  If we don't know
	// who this device belongs to, we should be able to learn after handling all of the frames inside
	// it and we'll want to check to make sure that happened.
	_, deviceAlreadyExists := b.getDeviceFromAddress(peer)

	// Keep track of which groups will need a consensus update
	groupsToUpdateConsensus := map[uuid.UUID]bool{}

	// Keep track of devices we need to reference after this
	devicesToReference := map[string]bool{}

	// Collect all the processed frames into a single ack
	a := &ack{}

	// Handle reach frame in the catch up using it's handler
	handlers := b.getHandlers()
	lastTimestamp := int64(0)
	catchUpMutex.Lock()
	for _, fr := range cu.Frames {
		// Check if this type of frame is allowed in a catch up
		if _, present := allowedCatchUpFrames[fr.Type]; !present {
			log.WithFields(log.Fields{
				"frame_type": fr.Type,
				"peer":       peer,
			}).Warn("refusing to process catch up that contains frame not allowed in catch ups")
			return nil
		}

		handler, ok := handlers[fr.Type]
		if !ok {
			log.WithFields(log.Fields{
				"peer": peer,
				"type": fr.Type,
			}).Warn("peer sent a catch up frame type that doesn't have a handler")
			continue
		}
		br := handler(peer, fr.Payload, true)
		if br == nil {
			log.WithFields(log.Fields{
				"id":   fr.ID,
				"type": fr.Type,
			}).Debug("handler for frame in catch up did not return broadcastable")
			continue
		}
		b.markDeliveredTo(br, peer)
		a.References = append(a.References, frameReference{FrameID: br.getID(), Type: br.getType()})
		log.WithFields(log.Fields{
			"id":   br.getID(),
			"type": br.getType(),
			"peer": peer,
		}).Debug("handling frame inside a catch up")

		// Make sure this catch up is in order
		if br.getTimestamp() < lastTimestamp {
			log.WithFields(log.Fields{
				"last_timestamp":    lastTimestamp,
				"current_timestamp": br.getTimestamp(),
				"type":              br.getType(),
				"id":                br.getID(),
			}).Error("refusing to process out-of-order catch up")
			return nil
		}
		lastTimestamp = br.getTimestamp()

		// Keep track of any device we would broadcast this to so that we can reference them
		destinations := b.getBroadcastScope(br)
		for _, dev := range destinations {
			devicesToReference[dev] = true
		}

		// If this frame impacts group consensus, take note of the group for a consensus check after the catch up
		if fr.Type == typeUpdateGroup || fr.Type == typeGroupCreation || fr.Type == typeConfirmation {
			groupsToUpdateConsensus[br.getDestination(b.currentUserID())] = true
		}
	}
	// Inform the reference engine of all the frames we handled
	b.loadCatchUp(peer, a.References)

	// Unlock the mutex for catchup handling
	catchUpMutex.Unlock()

	// Ack all of the handled frames
	go b.sendDirect(peer, a)

	// Update all group consensus states for groups that had an update group in this catch up
	for groupID, _ := range groupsToUpdateConsensus {
		// Get the user IDs for this group
		originalIDmap := make(map[uuid.UUID]bool)
		var originalUserIDs []uuid.UUID
		err := b.database.Table("group_users").
			Select("user_id").
			Where("group_id = ?", groupID).
			Find(&originalUserIDs).
			Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error getting user IDs in group")
		}
		for _, id := range originalUserIDs {
			originalIDmap[id] = true
		}

		// Update the group consensis
		b.updateGroupConsensus(groupID)

		// Get the user IDs for this group again, send references to any new users
		var updatedUserIDs []uuid.UUID
		err = b.database.Table("group_users").
			Select("user_id").
			Where("group_id = ?", groupID).
			Find(&updatedUserIDs).
			Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error getting user IDs in group")
		}
		for _, id := range updatedUserIDs {
			if _, present := originalIDmap[id]; !present {
				if id == b.currentUserID() {
					continue
				}
				var addresses []string
				err := b.database.Table("devices").
					Select("address").
					Where("user_id = ?", id).
					Find(&addresses).
					Error
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Fatal("database error getting addresses from devices")
				}
				for _, address := range addresses {
					go b.sendReferences(address)
				}
			}
		}
	}

	// Send references to any device we would have broadcast to
	for address, _ := range devicesToReference {
		go b.sendReferences(address)
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

	return nil
}
