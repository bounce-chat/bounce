package chat

import (
	"errors"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

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

	// Determine if we're going to learn about any groups that we aren't a member of, so we can ignore
	// frames for any of those groups
	nopGroups := b.identifyNOPGroups(cu.Frames)

	// Keep track of which groups will need a consensus update
	groupsToUpdateConsensus := map[uuid.UUID]bool{}

	// Keep track of devices we need to reference after this
	devicesToReference := map[string]bool{}

	// Handle reach frame in the catch up using it's handler
	handlers := b.getHandlers()
	lastTimestamp := int64(0)
	for _, fr := range cu.Frames {
		// Check if this type of frame is allowed in a catch up
		if _, present := allowedCatchUpFrames[fr.Type]; !present {
			log.WithFields(log.Fields{
				"frame_type": fr.Type,
				"peer":       peer,
			}).Warn("refusing to process catch up that contains frame not allowed in catch ups")
			return nil
		}

		// Ignore frames for groups that we will not be a part of after this catch up
		if b.isNopGroupFrame(nopGroups, fr) {
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
		br := handler(peer, fr.Payload, true)
		if br == nil {
			log.WithFields(log.Fields{
				"id":   fr.ID,
				"type": fr.Type,
			}).Debug("handler for frame in catch up did not return broadcastable")
			continue
		}
		b.markDeliveredTo(br, peer)
		go b.sendAck(peer, br.getType(), br.getID())

		// Make sure this catch up is in order
		if br.getTimestamp() < lastTimestamp {
			log.WithFields(log.Fields{
				"last_timestamp":    lastTimestamp,
				"current_timestamp": br.getTimestamp(),
				"type":              br.getType(),
				"id":                br.getID(),
			}).Error("refusing to process out-of-order catch up")
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

	// Update all group consensus states for groups that had an update group in this catch up
	for groupID, _ := range groupsToUpdateConsensus {
		b.updateGroupConsensus(groupID)
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

func (b *bounce) identifyNOPGroups(frames []frame) map[uuid.UUID]bool {
	nopGroups := map[uuid.UUID]bool{}
	stacks := map[uuid.UUID]*canonicalStack{}
	ugs := map[uuid.UUID][]updateGroup{}

	// Create canonical history stacks for each group creation in this set of frames, and collect the update groups by group ID
	for _, fr := range frames {
		if fr.Type == typeGroupCreation {
			sc, err := b.unpackSignedContainer(fr.Payload)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error unpacking signed container for group creation")
				continue
			}
			var gc groupCreation
			err = msgpack.Unmarshal(sc.Payload, &gc)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error unmarshalling group creation")
				continue
			}
			var g group
			err = msgpack.Unmarshal(gc.Data, &g)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error unmarshalling group")
				continue
			}

			if _, present := stacks[gc.ID]; present {
				log.WithFields(log.Fields{
					"group_id": gc.ID,
				}).Warn("catch up contains multiple group creations for same group")
				continue
			}

			initialState := groupState{
				name:                     g.Name,
				mutedUntil:               g.MutedUntil,
				retention:                g.Retention,
				clearBefore:              g.ClearBefore,
				users:                    []uuid.UUID{},
				admins:                   []uuid.UUID{},
				postingRestricted:        g.RestrictPosting,
				editingRestricted:        g.RestrictGroupEdits,
				userManagementRestricted: g.RestrictUserManagement,
			}

			for _, u := range g.Users {
				initialState.users = append(initialState.users, u.ID)
			}

			if len(g.Admins) != 0 { // TODO: still allowed?
				for _, adminIDString := range strings.Split(g.Admins, ",") {
					adminID, err := uuid.Parse(adminIDString)
					if err != nil {
						log.WithFields(log.Fields{
							"error":    err.Error(),
							"group_id": gc.ID,
							"admins":   g.Admins,
						}).Fatal("invalid UUID in group admin list")
					}

					initialState.admins = append(initialState.admins, adminID)
				}
			}

			stacks[gc.ID] = newCanonicalStack(initialState, b.currentUserID())
		}
	}

	for _, fr := range frames {
		if fr.Type == typeUpdateGroup {
			sc, err := b.unpackSignedContainer(fr.Payload)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error unpacking signed container for update group")
				continue
			}
			var ug updateGroup
			err = msgpack.Unmarshal(sc.Payload, &ug)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error unmarshalling update group")
				continue
			}
			ugs[ug.Target] = append(ugs[ug.Target], ug)

			if _, present := stacks[ug.Target]; !present {
				var gc groupCreation
				err = b.database.Where("id = ?", ug.Target).First(&gc).Error
				if errors.Is(err, gorm.ErrRecordNotFound) {
					nopGroups[ug.Target] = true
				}
			}
		}
	}

	// Insert the update groups from this set of frames, as well as upadte groups from the database, into the history stack and
	// determine if we are a member of this group in the end or if this is a NOP group
	for groupID, cs := range stacks {
		offeredIDs := map[uuid.UUID]bool{}
		allUgs, ok := ugs[groupID]
		if !ok {
			continue
		}
		for _, ug := range allUgs {
			offeredIDs[ug.ID] = true
		}

		databaseUgs := []updateGroup{}
		err := b.database.Preload(clause.Associations).Where("target = ?", groupID).Order("timestamp asc").Find(&databaseUgs).Error
		if err != nil {
			log.WithFields(log.Fields{
				"group_id": groupID,
				"error":    err.Error(),
			}).Fatal("database error selecting all update groups")
		}
		for _, ug := range databaseUgs {
			if _, present := offeredIDs[ug.ID]; !present {
				allUgs = append(allUgs, ug)
			}
		}

		sort.Slice(allUgs, func(i, j int) bool {
			return allUgs[i].Timestamp < allUgs[j].Timestamp
		})
		for _, ug := range allUgs {
			cs.insertUpdateGroupIntoStack(ug)
		}
		finalState, err := cs.top()
		if !finalState.isMember(b.currentUserID()) {
			nopGroups[groupID] = true
		}
	}

	return nopGroups
}

func (b *bounce) isNopGroupFrame(nopGroups map[uuid.UUID]bool, fr frame) bool {
	if fr.Type == typeGroupCreation {
		sc, err := b.unpackSignedContainer(fr.Payload)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error unpacking signed container for group creation")
			return false
		}
		var gc groupCreation
		err = msgpack.Unmarshal(sc.Payload, &gc)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error unmarshalling group creation")
			return false
		}
		_, present := nopGroups[gc.ID]
		return present
	}

	if fr.Type == typeUpdateGroup {
		sc, err := b.unpackSignedContainer(fr.Payload)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error unpacking signed container for update group")
			return false
		}
		var ug updateGroup
		err = msgpack.Unmarshal(sc.Payload, &ug)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error unmarshalling update group")
			return false
		}
		_, present := nopGroups[ug.Target]
		return present

	}

	if fr.Type == typeGroupMessage {
		sc, err := b.unpackSignedContainer(fr.Payload)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error unpacking signed container for group message")
			return false
		}
		var gm groupMessage
		err = msgpack.Unmarshal(sc.Payload, &gm)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error unmarshalling group message")
			return false
		}
		_, present := nopGroups[gm.Destination]
		return present

	}

	return false
}
