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

	// TODO: make sure that everything is sorted?

	// Check if we're aware of the peer identity before processing this catch up.  If we don't know
	// who this device belongs to, we should be able to learn after handling all of the frames inside
	// it and we'll want to check to make sure that happened.
	_, deviceAlreadyExists := b.getDeviceFromAddress(peer)

	nopGroups := b.identifyNOPGroups(cu.Frames)

	// Keep track of update groups that are new, and which groups will need a consensus update
	ugs := []updateGroup{}
	groupsToUpdateConsensus := map[uuid.UUID]bool{}

	// Handle reach frame in the catch up using it's handler
	handlers := b.getHandlers()
	for _, fr := range cu.Frames {
		if fr.Type == typeCatchUp {
			log.Warn("refusing to processes recursive catch up")
			return
		}

		// Ignore frames for groups that we will not be a part of after this catch up
		if b.isNopGroupFrame(nopGroups, fr) {
			continue
		}

		// Update groups should not be applied in order during a catch up in case the user is removed
		// from and re-added to the same group within the catch up
		if fr.Type == typeUpdateGroup {
			newAndValid, ug := b.validateAndSaveUpdateGroup(peer, fr.Payload)
			if newAndValid {
				ugs = append(ugs, ug)
				groupsToUpdateConsensus[ug.Target] = true
			}
			continue
		}

		// Save confirmations without updating group consensus so that all consensus updates can be done after
		// the catch up is processed for efficiency reasons
		if fr.Type == typeConfirmation {
			targetGroupID, shouldUpdate := b.saveConfirmationWithoutConsensusUpdate(peer, fr.Payload)
			if shouldUpdate {
				groupsToUpdateConsensus[targetGroupID] = true
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
	for groupID, _ := range groupsToUpdateConsensus {
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

	// Ack it
	go b.sendAck(peer, typeUpdateGroup, ug.ID)

	// Mark that the peer that send this update already has it
	b.markDeliveredTo(&ug, peer)

	// Broadcast it
	b.broadcast(&ug)

	// If we removed a user we should also broadcast to that user, who is not longer in the group scope
	if ug.Type == updateGroupTypeRemoveUser {
		userID, err := uuid.FromBytes(ug.Data)
		if err != nil {
			log.WithFields(log.Fields{
				"error":   err.Error(),
				"actor":   ug.Actor,
				"user_id": ug.Data,
			}).Error("update group attempted to remove user with invalid UUID")
			return
		}

		if userID != b.currentUserID() {
			var u user
			err := b.database.Preload(clause.Associations).Where("id = ?", userID).First(&u).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					log.WithFields(log.Fields{
						"user_id": userID,
					}).Error("user not found for direct remove from group broadcast")
					return
				} else {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Fatal("error looking up user")
				}
			}

			for _, dev := range u.Devices {
				rd := b.getRemoteDevice(dev.Address)
				if rd.connectedSockets > 0 {
					go b.sendDirect(dev.Address, &ug)
				}
			}
		}
	}

	if ug.Applied {
		// Update group activity if we're still in the group
		if b.userIsInGroup(b.currentUserID(), ug.Target) {
			b.updateLastGroupActivity(ug.Target, ug.Timestamp)
		}
	}
}

func (b *bounce) saveConfirmationWithoutConsensusUpdate(peer string, payload []byte) (uuid.UUID, bool) {
	confirmationMutex.Lock()
	defer confirmationMutex.Unlock()

	// Unmarshal the confirmation
	var c confirmation
	err := msgpack.Unmarshal(payload, &c)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling confirmation")
		return uuid.Nil, false
	}

	// Check if we already have this confirmation
	var existingConfirmation confirmation
	err = b.database.Where("id = ?", c.ID).First(&existingConfirmation).Error
	if err == nil {
		b.markDeliveredTo(&existingConfirmation, peer)
		go b.sendAck(peer, typeConfirmation, c.ID)
		return uuid.Nil, false
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
		}).Warn("ignoring confirmation with invalid signautre")
		return uuid.Nil, false
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
			return uuid.Nil, false
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up update group")
		}
	}
	c.Destination = ug.Target

	// Look up and assign the user who signed this confirmation
	dev, ok := b.getDeviceFromAddress(c.SigningDevice)
	if !ok {
		log.WithFields(log.Fields{
			"update_group_id": c.UpdateGroupID,
			"confirmation_id": c.ID,
			"signing_device":  c.SigningDevice,
		}).Warn("ignoring confirmation from unknown device")
		return uuid.Nil, false
	}
	c.Author = dev.UserID

	// Make sure the user signing this confirmation was a member of the group for this update
	if !b.isMemberOfGroupForUpdate(c.Author, ug.Target, ug.ID) {
		log.WithFields(log.Fields{
			"update_group_id": c.UpdateGroupID,
			"confirmation_id": c.ID,
			"author":          c.Author,
		}).Warn("ignoring confirmation signed by user who was not a member of the group during the update")
		return uuid.Nil, false
	}

	// Save it
	err = b.database.Create(&c).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving confirmation")
	}

	// Ack it
	go b.sendAck(peer, typeConfirmation, c.ID)

	// Mark as delivered to this peer
	b.markDeliveredTo(&c, peer)

	// Broadcast it
	b.broadcast(&c)

	return ug.Target, true
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
