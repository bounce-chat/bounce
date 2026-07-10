package chat

import (
	"sort"
	"sync"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

var catchUpMutex sync.Mutex

var allowedCatchUpFrames = map[uint16]bool{
	typeDirectMessage:       true,
	typeGroupMessage:        true,
	typeUpdateDM:            true,
	typeDevice:              true,
	typeAddUser:             true,
	typeGroupCreation:       true,
	typeUpdateGroup:         true,
	typeConfirmation:        true,
	typeUpdateUser:          true,
	typeUpdateDevice:        true,
	typeReadReceipt:         true,
	typeUpdateSettings:      true,
	typeFile:                true,
	typeChunkOffer:          true,
	typeDraft:               true,
	typeEncryptedChunkOffer: true,
}

// A frame contains the ID, type, and marshalled payload of any other frame
type frame struct {
	ID      uuid.UUID
	Type    uint16
	Payload []byte
}

// A catch up is a frame that is used to transport a set of frames that are chronologically ordered
type catchUp struct {
	Frames    []frame
	sendables sortableCatchUpAbles
}

func (cu *catchUp) getType() uint16 {
	return typeCatchUp
}

func (cu *catchUp) getPayload() []byte {
	sort.Sort(cu.sendables)
	for _, br := range cu.sendables {
		cu.Frames = append(cu.Frames, frame{ID: br.getID(), Type: br.getType(), Payload: br.getPayload()})
	}

	bytes, err := msgpack.Marshal(cu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal catch up")
	}
	return bytes
}

var initialSyncMutex sync.Mutex

func (b *Bounce) handleCatchUp(peer string, payload []byte, _ bool) (broadcastable, bool) {
	if waitingForInitialSyncFrom == peer {
		b.ui.InitialSyncStarting()

		initialSyncMutex.Lock()
		defer initialSyncMutex.Unlock()
	}

	// Unmarshal the catch up
	var cu catchUp
	err := msgpack.Unmarshal(payload, &cu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling catch up")
		return nil, false
	}

	if b.encrypted {
		for _, fr := range cu.Frames {
			b.handleEncryptedFrame(peer, fr.Payload, true)
		}
		return nil, false
	}

	// Check if we're aware of the peer identity before processing this catch up.  If we don't know
	// who this device belongs to, we should be able to learn after handling all of the frames inside
	// it and we'll want to check to make sure that happened.
	dev, deviceExists := b.getDeviceFromAddress(peer)
	encryptedDeviceCacheMutex.Lock()
	_, encrypted := encryptedDeviceCache[peer]
	encryptedDeviceCacheMutex.Unlock()

	// Do not allow catchups from blocked users
	if blockedUser(dev.UserID) {
		return nil, false
	}

	// Keep track of things that are impacted by this catch up
	groupsToUpdateConsensus := map[uuid.UUID]bool{}
	usersToUpdate := map[uuid.UUID]bool{}
	dmsToUpdate := map[uuid.UUID]bool{}
	devicesToReference := map[string]bool{}
	settingsUpdated := false
	draftsUpdated := true
	devicesToUpdate := map[uuid.UUID]bool{}

	// Keep track of frames that will be shared with the UI in a BulkUpdate after everything else is processed
	gmsToDisplay := []*groupMessage{}
	dmsToDisplay := []*directMessage{}
	rrsToDisplay := []*readReceipt{}

	// Collect all the processed frames into a single ack
	a := &ack{}

	// Handle reach frame in the catch up using it's handler
	handlers := b.getHandlers(b.encrypted)
	catchUpMutex.Lock()
	frameCount := len(cu.Frames)
	progressMod := 1
	if frameCount > 1000 {
		progressMod = int(frameCount / 1000)
	}
	for i, fr := range cu.Frames {
		// Check if this type of frame is allowed in a catch up
		if _, present := allowedCatchUpFrames[fr.Type]; !present {
			log.WithFields(log.Fields{
				"frame_type": fr.Type,
				"peer":       peer,
			}).Warn("refusing to process catch up that contains frame not allowed in catch ups")
			return nil, false
		}

		handler, ok := handlers[fr.Type]
		if !ok {
			log.WithFields(log.Fields{
				"peer": peer,
				"type": fr.Type,
			}).Warn("peer sent a catch up frame type that doesn't have a handler")
			continue
		}
		br, firstTimeSeeing := handler(peer, fr.Payload, true)
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

		// Keep track of any device we would broadcast this to so that we can reference them
		destinations := b.getBroadcastScope(br, true)
		for _, dst := range destinations {
			devicesToReference[dst] = true
		}

		// Track any follow up actions we need to take on these frames after the catchup compeltes
		switch fr.Type {
		case typeUpdateGroup:
			groupsToUpdateConsensus[br.getDestination(b.currentUserID())] = true
		case typeConfirmation:
			groupsToUpdateConsensus[br.getDestination(b.currentUserID())] = true
		case typeGroupCreation:
			groupsToUpdateConsensus[br.getDestination(b.currentUserID())] = true
		case typeUpdateUser:
			usersToUpdate[br.getAuthor()] = true
		case typeUpdateDM:
			dmsToUpdate[br.getDestination(b.currentUserID())] = true

			// If this is an update to anyone's block status, do the update immediately
			// so that the change applies to handling other frames in the catch up
			ud, ok := br.(*updateDM)
			if !ok {
				log.WithFields(log.Fields{
					"id": br.getID(),
				}).Warn("cannot cast update DM broadcastable")
				continue
			}
			if ud.Type == updateDMTypeSetBlocked {
				u, ok := b.currentUser()
				if !ok {
					continue
				}
				b.updateDMState(ud.getDestination(u.ID))
			}
		case typeUpdateSettings:
			settingsUpdated = true
		case typeUpdateDevice:
			devicesToUpdate[br.getDestination(b.currentUserID())] = true
		case typeGroupMessage:
			if firstTimeSeeing {
				gm, ok := br.(*groupMessage)
				if !ok {
					log.WithFields(log.Fields{
						"id": br.getID(),
					}).Warn("group message handler returned broadcastable that is not group message")
					continue
				}
				gmsToDisplay = append(gmsToDisplay, gm)
			}
		case typeDirectMessage:
			if firstTimeSeeing {
				dm, ok := br.(*directMessage)
				if !ok {
					log.WithFields(log.Fields{
						"id": br.getID(),
					}).Warn("direct message handler returned broadcastable that is not direct message")
					continue
				}
				dmsToDisplay = append(dmsToDisplay, dm)
			}
		case typeReadReceipt:
			if firstTimeSeeing {
				rr, ok := br.(*readReceipt)
				if !ok {
					log.WithFields(log.Fields{
						"id": br.getID(),
					}).Warn("read receipt handler returned broadcastable that is not read receipt")
					continue
				}
				rrsToDisplay = append(rrsToDisplay, rr)
			}
		case typeDraft:
			draftsUpdated = true
		}

		if waitingForInitialSyncFrom == peer {
			if i%progressMod == 0 {
				b.ui.InitialSyncProgress(float64(i) / float64(frameCount))
			}
		}
	}

	if waitingForInitialSyncFrom == peer {
		b.ui.InitialSyncPreparing()
	}

	// Inform the reference engine of all the frames we handled
	b.loadCatchUp(peer, a.References)

	// Unlock the mutex for catchup handling
	catchUpMutex.Unlock()

	// Ack all of the handled frames
	go b.sendDirect(peer, a)

	for deviceID, _ := range devicesToUpdate {
		b.updateDeviceState(deviceID)
	}

	for userID, _ := range usersToUpdate {
		b.updateUserState(userID)
	}

	for userID, _ := range dmsToUpdate {
		b.updateDMState(userID)
	}

	if settingsUpdated {
		b.updateSettingsState()
	}

	if draftsUpdated {
		draftMutex.Lock()
		for _, d := range draftCache {
			b.ui.UpdateDraft(Draft{Thread: d.Thread, Text: d.Text})
		}
		draftMutex.Unlock()
		b.syncDrafts()
	}

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
		b.writeGroupConsensus(groupID)

		// Get the user IDs for this group again, send references to any new users
		// TODO: or any removed users?
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

	// If we didn't know who this device belonged to at first, check to make sure we know who it belongs to
	// after handling all of the frames
	if !encrypted && !deviceExists {
		if _, deviceExists = b.getDeviceFromAddress(peer); deviceExists {
			// An unknown device sent a catch up that included frames that prove we should add the device,
			// since we didn't initially offer references to this device we should now do so now that we
			// have context on what this device is
			go b.sendReferences(peer)
		} else {
			encryptedDeviceCacheMutex.Lock()
			_, encrypted := encryptedDeviceCache[peer]
			encryptedDeviceCacheMutex.Unlock()
			if !encrypted {
				log.WithFields(log.Fields{
					"peer":        peer,
					"frame_count": len(cu.Frames),
				}).Warn("catch up from unknown device did not result in learning device identity")
			}
		}
	}

	// Display the group messages we received for the first time in this catch up, as long as we are still in the groups
	bulkUpdate := BulkUpdate{
		Source:         uuid.Nil,
		Seen:           make([]uuid.UUID, 0),
		GroupMessages:  make(map[uuid.UUID][]GroupMessage),
		DirectMessages: make(map[uuid.UUID][]DirectMessage),
		ReadReceipts:   make(map[uuid.UUID][]ReadReceipt),
	}
	if deviceExists {
		bulkUpdate.Source = dev.UserID
	}

	var mostRecentUnseenGMs = map[uuid.UUID]*groupMessage{}
	for i, gm := range gmsToDisplay {
		groupID := gm.getDestination(b.currentUserID())
		gs, err := b.currentGroupState(groupID)
		if err != nil {
			log.WithFields(log.Fields{
				"group_id": groupID,
				"error":    err.Error(),
			}).Error("error getting group state while checking if group message should be displayed")
			continue
		}
		if !gs.isMember(b.currentUserID()) || gs.isBlocked(b.currentUserID()) || gs.deletedBy != nil {
			// Do not display group messages for groups that we are no longer displaying
			continue
		}

		uiImageAttachments := []ImageAttachment{}
		for _, ia := range gm.ImageAttachments {
			uiImageAttachments = append(uiImageAttachments, ImageAttachment{
				ID:       ia.FileID,
				Name:     ia.Name,
				Size:     ia.Size,
				Width:    ia.Width,
				Height:   ia.Height,
				BlurHash: ia.BlurHash,
			})
		}
		uiFileAttachments := []FileAttachment{}
		for _, fa := range gm.FileAttachments {
			uiFileAttachments = append(uiFileAttachments, FileAttachment{
				ID:   fa.FileID,
				Name: fa.Name,
				Size: fa.Size,
			})
		}

		// Reload the seen value in case it was updated during group consensus
		var seenReload groupMessage
		err = b.database.Select("seen").First(&seenReload, "id = ?", gm.ID).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":           err.Error(),
				"update_group_id": gm.ID,
			}).Error("error reloading seen value for update group")
		}
		gm.Seen = seenReload.Seen

		// Inform the UI about the new message
		bulkUpdate.GroupMessages[gm.Destination] = append(bulkUpdate.GroupMessages[gm.Destination], GroupMessage{
			ID:               gm.ID,
			Author:           gm.Author,
			Thread:           gm.Destination,
			WrittenAt:        gm.WrittenAt,
			SavedAt:          gm.SavedAt,
			ExpiresAt:        gm.DeleteAt,
			Seen:             gm.Seen,
			Text:             gm.Text,
			ImageAttachments: uiImageAttachments,
			FileAttachments:  uiFileAttachments,
		})

		// Find any read receipts for this message that came early, add missing data, broadcast and send to the UI
		seen, rrs := b.processEarlyReadReceipts(gm.ID, typeGroupMessage, false)
		if seen {
			bulkUpdate.Seen = append(bulkUpdate.Seen, gm.ID)
		} else {
			mostRecentUnseenGMs[gm.Destination] = gmsToDisplay[i]
		}
		bulkUpdate.ReadReceipts[gm.ID] = append(bulkUpdate.ReadReceipts[gm.ID], rrs...)

		// Update last activity time if it is the latest
		b.updateLastGroupActivity(gm.Destination, gm.SavedAt)
	}

	var mostRecentUnseenDMs = map[uuid.UUID]*directMessage{}
	for i, dm := range dmsToDisplay {
		uiImageAttachments := []ImageAttachment{}
		for _, ia := range dm.ImageAttachments {
			uiImageAttachments = append(uiImageAttachments, ImageAttachment{
				ID:       ia.FileID,
				Name:     ia.Name,
				Size:     ia.Size,
				Width:    ia.Width,
				Height:   ia.Height,
				BlurHash: ia.BlurHash,
			})
		}
		uiFileAttachments := []FileAttachment{}
		for _, fa := range dm.FileAttachments {
			uiFileAttachments = append(uiFileAttachments, FileAttachment{
				ID:   fa.FileID,
				Name: fa.Name,
				Size: fa.Size,
			})
		}

		// Inform the UI about the new message
		destination := dm.getDestination(b.currentUserID())
		bulkUpdate.DirectMessages[destination] = append(bulkUpdate.DirectMessages[destination], DirectMessage{
			ID:               dm.ID,
			Author:           dm.Author,
			Thread:           destination,
			WrittenAt:        dm.WrittenAt,
			SavedAt:          dm.SavedAt,
			ExpiresAt:        dm.DeleteAt,
			Seen:             dm.Seen,
			Text:             dm.Text,
			ImageAttachments: uiImageAttachments,
			FileAttachments:  uiFileAttachments,
		})

		// Find any read receipts for this message that came early, add missing data, broadcast and send to the UI
		seen, rrs := b.processEarlyReadReceipts(dm.ID, typeDirectMessage, false)
		if seen {
			bulkUpdate.Seen = append(bulkUpdate.Seen, dm.ID)
		} else {
			mostRecentUnseenDMs[dm.getDestination(b.currentUserID())] = dmsToDisplay[i]
		}
		bulkUpdate.ReadReceipts[dm.ID] = append(bulkUpdate.ReadReceipts[dm.ID], rrs...)
	}

	for _, rr := range rrsToDisplay {
		targetTypeString, ok := readReceiptTargetTypeString[rr.TargetType]
		if !ok {
			log.WithFields(log.Fields{
				"error": errUnknownReadReceiptTargetType.Error(),
			}).Error("error parsing read receipt")
			return nil, false
		}
		_, author, _, err := b.getReadReceiptDestinationAuthorAndScope(rr.Target, targetTypeString)
		if err != nil {
			log.WithFields(log.Fields{
				"id":    rr.ID,
				"error": err.Error(),
			}).Warn("error processing read receipt for bulk update in catch up")
			continue
		}
		if rr.Actor == b.currentUserID() {
			bulkUpdate.Seen = append(bulkUpdate.Seen, rr.Target)
		} else if author == b.currentUserID() {
			if rr.Scope != scopeSync {
				bulkUpdate.ReadReceipts[rr.Target] = append(
					bulkUpdate.ReadReceipts[rr.Target],
					ReadReceipt{
						ID:     rr.ID,
						Actor:  rr.Actor,
						Target: rr.Target,
					},
				)
			}
		}
	}
	b.ui.CatchUpMessages(bulkUpdate, waitingForInitialSyncFrom == peer)

	for _, gm := range gmsToDisplay {
		b.ui.MessageDelivered(gm.ID, dev.UserID)
	}
	for _, dm := range dmsToDisplay {
		b.ui.MessageDelivered(dm.ID, dev.UserID)
	}

	for _, dm := range mostRecentUnseenDMs {
		if b.postNotification != nil && b.shouldNotifyDM(dm) {
			title, content, err := b.getDMNotificationContent(dm)
			if err == nil {
				b.postNotification(
					dm.ID.String(),
					title,
					content,
					dm.getDestination(b.currentUserID()).String(),
					b.getNotificationIcon(dm.getDestination(b.currentUserID()).String()),
				)
			}
		}
	}
	for _, gm := range mostRecentUnseenGMs {
		if b.postNotification != nil && b.shouldNotifyGM(gm) {
			title, content, err := b.getGMNotificationContent(gm)
			if err == nil {
				b.postNotification(
					gm.ID.String(),
					title,
					content,
					gm.Destination.String(),
					b.getNotificationIcon(gm.Destination.String()),
				)
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

	if waitingForInitialSyncFrom == peer {
		waitingForInitialSyncFrom = ""
		b.ui.InitialSyncComplete()
	}

	// If this is an encryped device (and we aren't), check if we manage it, and if so check if we need to re-key it
	if encrypted && !b.encrypted {
		b.getManagementKeyHash(peer)
	}

	return nil, false
}
