package chat

import (
	"errors"
	"strings"
	"sync"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type consensusStore struct {
	sync.Mutex

	groups map[uuid.UUID]*canonicalStack
}

func (b *bounce) currentGroupStack(groupID uuid.UUID) (*canonicalStack, error) {
	b.consensusStore.Lock()
	stack, ok := b.consensusStore.groups[groupID]
	b.consensusStore.Unlock()
	if !ok {
		b.reloadGroupConsensus(groupID)
		b.consensusStore.Lock()
		stack, ok = b.consensusStore.groups[groupID]
		b.consensusStore.Unlock()
		if !ok {
			return &canonicalStack{}, errors.New("group consensus state doesn't exist after creation")
		}
	}
	return stack, nil
}

func (b *bounce) currentGroupState(groupID uuid.UUID) (groupState, error) {
	stack, err := b.currentGroupStack(groupID)
	if err != nil {
		return groupState{}, err
	}

	b.consensusStore.Lock()
	top, err := stack.top()
	b.consensusStore.Unlock()
	if err != nil {
		return groupState{}, err
	}

	return top, nil
}

func (b *bounce) reloadGroupConsensus(groupID uuid.UUID) {
	b.consensusStore.Lock()
	defer b.consensusStore.Unlock()

	// Create the initial group state from the group creation and use that to start history
	initialState, err := b.createInitialGroupState(groupID)
	if err != nil {
		log.WithFields(log.Fields{
			"group_id": groupID,
			"error":    err.Error(),
		}).Error("error creating initial state while updating group consensus")
		return
	}
	cs := newCanonicalStack(initialState, b.currentUserID())

	// Get all update groups for this group
	var ugs []updateGroup
	err = b.database.Preload(clause.Associations).Where("target = ?", groupID).Order("timestamp asc").Find(&ugs).Error
	if err != nil {
		log.WithFields(log.Fields{
			"group_id": groupID,
			"error":    err.Error(),
		}).Fatal("database error selecting all update groups")
	}

	// For each update group, attempt to add it to history
	for _, ug := range ugs {
		b.insertUpdateGroupIntoStack(cs, ug)
	}

	// Add this stack to the consensus store
	b.consensusStore.groups[groupID] = cs
}

func (b *bounce) reloadGroupConsensusSince(groupID uuid.UUID, ts int64) {
	b.consensusStore.Lock()
	defer b.consensusStore.Unlock()

	// Reload everything if timestamp is 0
	if ts == 0 {
		b.reloadGroupConsensus(groupID)
		return
	}

	// Find the stack, reload everything if we don't have a stack yet
	stack, ok := b.consensusStore.groups[groupID]
	if !ok {
		b.reloadGroupConsensus(groupID)
		return
	}

	// Remove updates that are at or older than timestamp from the stack
	untouchedState := []groupState{}
	for _, gs := range stack.history {
		if gs.ug.Timestamp < ts {
			untouchedState = append(untouchedState, gs)
		} else {
			break
		}
	}
	stack.history = untouchedState

	// Load all updates that are timestamp or newer from the database
	var ugs []updateGroup
	err := b.database.Preload(clause.Associations).Where("target = ? AND timestamp >= ?", groupID, ts).Order("timestamp asc").Find(&ugs).Error
	if err != nil {
		log.WithFields(log.Fields{
			"group_id": groupID,
			"error":    err.Error(),
		}).Fatal("database error selecting new update groups during partial reload")
	}

	// Add all updates from the database to the stack
	for _, ug := range ugs {
		b.insertUpdateGroupIntoStack(stack, ug)
	}
}

func (b *bounce) writeGroupConsensus(groupID uuid.UUID) error {
	b.consensusStore.Lock()
	defer b.consensusStore.Unlock()

	// Try to find the group, if there is no group, apply any blocking updates for that group
	var g group
	err := b.database.Preload(clause.Associations).Where("id = ?", groupID).First(&g).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// If the group doesn't exist we might still have unprocessed block frames to apply
			var ugs []updateGroup
			err = b.database.Preload(clause.Associations).Where(
				"target = ? AND type = ? AND actor = ? AND applied = ?",
				groupID,
				updateGroupTypeBlock,
				b.currentUserID(),
				false,
			).Order("timestamp asc").Find(&ugs).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error looking up update groups")
			}
			for _, ug := range ugs {
				b.addBlockedGroup(ug.Target)
				err = b.database.Model(&ug).Select("applied").Update("applied", true).Error
				if err != nil {
					log.WithFields(log.Fields{
						"id":    ug.ID,
						"error": err.Error(),
					}).Error("error setting applied on update group that blocks group")
				}
			}
			return nil
		} else {
			log.WithFields(log.Fields{
				"group_id": groupID,
				"error":    err.Error(),
			}).Fatal("database error looking up group")
		}
	}

	// Get the canonical history stack
	stack, ok := b.consensusStore.groups[groupID]
	if !ok {
		return errors.New("cannot write group consensus for group without a history stack")
	}

	// Get all update groups for this group
	var ugs []updateGroup
	err = b.database.Find(&ugs, "target = ?", groupID).Error
	if err != nil {
		log.WithFields(log.Fields{
			"group_id": groupID,
			"error":    err.Error(),
		}).Fatal("database error looking up update groups")
	}

	// Apply the group state to the database and UI, marking updates in the database as applied or not where needed
	return b.setRollbacksApplicationsAndGroupState(g, stack, ugs)
}

func (b *bounce) setRollbacksApplicationsAndGroupState(g group, cs *canonicalStack, ugs []updateGroup) error {
	finalState, err := cs.top()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("no final state available when updating group consensus state")
		return err
	}

	// Find any canonical update groups that have not been applied and make them as applied and inform the UI if needed
	canonical := make(map[uuid.UUID]bool)
	everInGroup := make(map[uuid.UUID]bool)
	for _, gs := range cs.history[1:] {
		canonical[gs.ug.ID] = true
		if !gs.ug.Applied {
			err := b.database.Model(&gs.ug).Select("applied").Update("applied", true).Error
			if err != nil {
				return err
			}

			if finalState.deletedBy == nil && finalState.isMember(b.currentUserID()) {
				// Defer is used to the the UI calls occur after the state has been set, for tests that trigger
				// checks based on when UI calls complete.
				defer b.informUIOfUpdateGroup(gs.ug)
			}

			if gs.ug.Type == updateGroupTypeAddUser {
				var newUser user
				err := msgpack.Unmarshal(gs.ug.Data, &newUser)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("update group add user container invalid user data")
					return err
				}
				if finalState.isMember(newUser.ID) {
					b.createNewUserIfNeeded(newUser)
				}
			}

			// If we were a member of the group for this update, and we are still in the group and it is not deleted, broadcast a confirmation
			if gs.isMember(b.currentUserID()) {
				b.updateLastGroupActivity(gs.ug.Target, gs.ug.Timestamp)
				if gs.ug.Actor != b.currentUserID() {
					if !(gs.ug.Type == updateGroupTypeBlock) {
						b.sendConfirmation(gs.ug)
					}
				}
			}
		}
		for _, member := range gs.users {
			everInGroup[member] = true
		}
	}

	// If we blocked this group, save that on user, custom scope our block update, and delete the group
	if finalState.blockedBy != nil {
		// Add this group to our list of blocked groups
		b.addBlockedGroup(g.ID)

		// Find our block update and custom scope it
		err = b.createCustomScopeFromGroup(g.ID)
		if err == nil {
			err = b.database.Model(&updateGroup{}).Where("id = ?", finalState.blockedBy).Select("custom_scope").Update("custom_scope", g.ID).Error
			if err != nil {
				log.WithFields(log.Fields{
					"update_group_id": finalState.blockedBy,
					"error":           err.Error(),
				}).Fatal("error updating custom scope on update group")
			}
		} else {
			log.WithFields(log.Fields{
				"update_group_id": finalState.blockedBy,
				"error":           err.Error(),
			}).Error("error creating custom scope for update group")
		}

		// Inform the UI
		b.userInterface.GroupDeleted(GroupDeleted{
			Group: g.ID,
			Actor: b.currentUserID(),
		})

		// Delete the group
		return b.database.Delete(&g).Error
	}

	// If the final state is that the group is deleted, delete the group
	if finalState.deletedBy != nil {
		// Attach a custom scope to this update group
		err = b.createCustomScopeFromGroup(g.ID)
		if err == nil {
			err = b.database.Model(&updateGroup{}).Where("id = ?", finalState.deletedBy.ID).Select("custom_scope").Update("custom_scope", g.ID).Error
			if err != nil {
				log.WithFields(log.Fields{
					"update_group_id": finalState.deletedBy.ID,
					"error":           err.Error(),
				}).Fatal("error updating custom scope on update group")
			}
		} else {
			log.WithFields(log.Fields{
				"update_group_id": finalState.deletedBy.ID,
				"error":           err.Error(),
			}).Error("error creating custom scope for update group")
		}

		// Determing if the actor who deleted this group was ever not an admin and collect any updates about their admin status
		alwaysAnAdmin := true
		ugsWithAdminStatusSideEffects := []updateGroup{}
		for _, gs := range cs.history {
			if !gs.isAdmin(finalState.deletedBy.Actor) {
				alwaysAnAdmin = false
			}

			if gs.ug.Type == updateGroupTypeAddUser || gs.ug.Type == updateGroupTypeRemoveUser || gs.ug.Type == updateGroupTypePromoteAdmin || gs.ug.Type == updateGroupTypeDemoteAdmin {
				ugsWithAdminStatusSideEffects = append(ugsWithAdminStatusSideEffects, gs.ug)
			}
		}

		// If this actor was ever not an admin, we need to preserve the history
		if !alwaysAnAdmin {
			// Find the custom scope we just created
			var cs customScope
			err = b.database.First(&cs, "id = ?", g.ID).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					log.WithFields(log.Fields{
						"id": g.ID,
					}).Warn("cannot find custom scope that was just created")
				} else {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Fatal("database error querying for custom scope")
				}
			} else {
				addrs := cs.addresses()

				for _, ug := range ugsWithAdminStatusSideEffects {
					allDelivered := true
					for _, addr := range addrs {
						if _, revoked := b.devicePool.revokedDevices[addr]; revoked {
							continue
						}
						if !b.isDeliveredTo(&ug, addr) {
							allDelivered = false
						}
					}

					if !allDelivered {
						err = b.database.Model(&ug).Select("custom_scope").Update("custom_scope", g.ID).Error
						if err != nil {
							log.WithFields(log.Fields{
								"update_group_id": ug.ID,
								"error":           err.Error(),
							}).Fatal("error updating custom scope on update group")
						}
					}
				}
			}
		}

		// Inform the UI
		b.userInterface.GroupDeleted(GroupDeleted{
			Group: g.ID,
			Actor: finalState.ug.Actor,
		})

		// Delete the group
		return b.database.Delete(&g).Error
	}

	// If the final state involves us being removed from the group, delete the group
	if finalState.removedBy != nil {
		// Attach a custom scope to this update group
		err = b.createCustomScopeFromGroup(g.ID) //ug.Target)
		if err == nil {
			err = b.database.Model(&updateGroup{}).Where("id = ?", finalState.removedBy.ID).Select("custom_scope").Update("custom_scope", g.ID).Error
			if err != nil {
				log.WithFields(log.Fields{
					"update_group_id": finalState.removedBy.ID,
					"error":           err.Error(),
				}).Fatal("error updating custom scope on update group")
			}
		} else {
			log.WithFields(log.Fields{
				"update_group_id": finalState.removedBy.ID,
				"error":           err.Error(),
			}).Error("error creating custom scope for update group")
		}

		// Inform the UI
		b.userInterface.RemovedFromGroup(RemovedFromGroup{
			Group: g.ID,
			Actor: finalState.removedBy.Actor,
		})

		// Delete the group
		return b.database.Delete(&g).Error
	}

	// Find any non-canonical update groups that have been applied and mark them as not applied roll them back in the UI
	for _, ug := range ugs {
		if _, ok := canonical[ug.ID]; !ok {
			if ug.Applied {
				err := b.database.Model(&ug).Select("applied").Update("applied", false).Error
				if err != nil {
					return err
				}
				b.userInterface.DeleteItem(ug.ID)
			}
		}
	}

	// Clear delivery records for users that were removed in the final state
	for userID, _ := range everInGroup {
		if !finalState.isMember(userID) {
			b.clearGroupDeliveryRecordsForUser(userID, g.ID)
		}
	}

	// If there was a failed attempt to delete the group, clear all delivery records once in order to restore the group
	// for any devices that applied the deletion
	var failedDelete updateGroup
	err = b.database.
		Select("id", "MAX(timestamp)").
		Where("target = ? AND type = ? AND applied = false", g.ID, updateGroupTypeDelete).
		Find(&failedDelete).
		Error
	if err == nil {
		b.clearDeliveryRecordsForFailedDelete(g.ID, failedDelete.ID)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"group_id": g.ID,
			"error":    err.Error(),
		}).Fatal("database error looking for unapplied update group delete")
	}

	return b.setGroupStateInDatabase(g, finalState)
}

func (b *bounce) setGroupStateInDatabase(g group, gs groupState) error {
	// Prune cleared messages
	b.pruneMessagesBeforeClear(gs.clearBefore, g.ID)

	// Set fields
	if g.Name != gs.name {
		err := b.database.Model(&g).Select("name").Update("name", gs.name).Error
		if err != nil {
			return err
		}
	}
	if g.Retention != gs.retention {
		err := b.database.Model(&g).Select("retention").Update("retention", gs.retention).Error
		if err != nil {
			return err
		}
	}
	if g.ClearBefore != gs.clearBefore {
		err := b.database.Model(&g).Select("clear_before").Update("clear_before", gs.clearBefore).Error
		if err != nil {
			return err
		}
	}
	if g.MutedUntil != gs.mutedUntil {
		err := b.database.Model(&g).Select("muted_until").Update("muted_until", gs.mutedUntil).Error
		if err != nil {
			return err
		}
	}
	if g.RestrictUserManagement != gs.userManagementRestricted {
		err := b.database.Model(&g).Select("restrict_user_management").Update("restrict_user_management", gs.userManagementRestricted).Error
		if err != nil {
			return err
		}
	}
	if g.RestrictGroupEdits != gs.editingRestricted {
		err := b.database.Model(&g).Select("restrict_group_edits").Update("restrict_group_edits", gs.editingRestricted).Error
		if err != nil {
			return err
		}
	}
	if g.RestrictPosting != gs.postingRestricted {
		err := b.database.Model(&g).Select("restrict_posting").Update("restrict_posting", gs.postingRestricted).Error
		if err != nil {
			return err
		}
	}

	// Set group members
	for _, u := range g.Users {
		if !gs.isMember(u.ID) {
			err := b.database.Exec("DELETE FROM group_users WHERE group_id = ? AND user_id = ?", g.ID, u.ID).Error
			if err != nil {
				return err
			}
		}
	}
	finalUsers := []User{}
	for _, userID := range gs.users {
		var u user
		err := b.database.Select("name").First(&u, "id = ?", userID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"error":   err.Error(),
					"user_id": userID,
				}).Error("group final state contains user not in database")
				return err
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error looking up user")
			}
		}
		finalUsers = append(finalUsers, User{ID: userID, Name: u.Name})

		if !b.userIsInGroup(g.ID, userID) { // TODO: check the groups struct that was passed?
			err = b.database.Exec("INSERT INTO group_users VALUES(?, ?)", g.ID, userID).Error
			if err != nil {
				if !errors.Is(err, gorm.ErrDuplicatedKey) {
					return err
				}
			}
		}
	}

	// Set group admins
	admins := []uuid.UUID{}
	for _, adminIDString := range strings.Split(g.Admins, ",") {
		adminID, err := uuid.Parse(adminIDString)
		if err != nil {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"group_id": g.ID,
				"admins":   g.Admins,
			}).Fatal("invalid UUID in group admin list")

		}
		admins = append(admins, adminID)
	}
	for _, adminID := range admins {
		if !gs.isAdmin(adminID) {
			b.removeGroupAdmin(g.ID, adminID)
		}
	}
	for _, adminID := range gs.admins {
		if !b.isGroupAdmin(g.ID, adminID) { // TODO: check the group struct that was passed in?
			b.addGroupAdmin(g.ID, adminID)
		}
	}

	// Set blocked users
	for _, blockedID := range gs.blockedUsers {
		if !b.isBlockedFromGroup(g.ID, blockedID) {
			b.blockUserFromGroup(g.ID, blockedID)
		}
	}

	// Set the read receipt and typing indicator overrides
	if g.ReadReceiptsOverridden != gs.readReceiptsOverridden {
		err := b.database.Model(&g).Select("read_receipts_overridden").Update("read_receipts_overridden", gs.readReceiptsOverridden).Error
		if err != nil {
			return err
		}
	}
	if g.ReadReceiptsEnabled != gs.readReceiptsEnabled {
		err := b.database.Model(&g).Select("read_receipts_enabled").Update("read_receipts_enabled", gs.readReceiptsEnabled).Error
		if err != nil {
			return err
		}
	}
	if g.TypingIndicatorsOverridden != gs.typingIndicatorsOverridden {
		err := b.database.Model(&g).Select("typing_indicators_overridden").Update("typing_indicators_overridden", gs.typingIndicatorsOverridden).Error
		if err != nil {
			return err
		}
	}
	if g.TypingIndicatorsEnabled != gs.typingIndicatorsEnabled {
		err := b.database.Model(&g).Select("typing_indicators_enabled").Update("typing_indicators_enabled", gs.typingIndicatorsEnabled).Error
		if err != nil {
			return err
		}
	}

	b.userInterface.SetGroupState(Group{
		ID:   g.ID,
		Name: g.Name,
		//Image: []byte{},
		Users:                          finalUsers,
		Admins:                         gs.admins,
		BlockedUsers:                   gs.blockedUsers,
		Retention:                      g.Retention,
		MutedUntil:                     g.MutedUntil,
		LastActivity:                   g.LastActivity,
		RestrictUserManagement:         g.RestrictUserManagement,
		RestrictGroupEdits:             g.RestrictGroupEdits,
		RestrictPosting:                g.RestrictPosting,
		OverrideReadReceiptSetting:     g.ReadReceiptsOverridden,
		ReadReceiptsEnabled:            g.ReadReceiptsEnabled,
		OverrideTypingIndicatorSetting: g.TypingIndicatorsOverridden,
		TypingIndicatorsEnabled:        g.TypingIndicatorsEnabled,
	})

	return nil
}

func (b *bounce) createNewUserIfNeeded(u user) {
	if u.ID != b.currentUserID() {
		// Ensure the user is valid
		if !b.hasValidDeviceGroup(u) {
			log.WithFields(log.Fields{
				"user_id": u.ID,
			}).Warn("refusing to save user with invalid device group")
			return
		}

		// Save the user and their devices if we don't have them
		for _, dev := range u.Devices {
			err := b.database.Clauses(clause.OnConflict{DoNothing: true}).Create(&dev).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("error saving device that belongs to a user being added to a group")
			}
		}
		err := b.database.Clauses(clause.OnConflict{DoNothing: true}).Create(&u).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error saving user that is being added to a group")
		}

		// Attempt to make a connection to the user
		b.userConnectionDesired(u.ID)
	}
}

func (b *bounce) clearGroupDeliveryRecordsForUser(userID, groupID uuid.UUID) {
	// Get the user
	var u user
	err := b.database.Preload(clause.Associations).Where("id = ?", userID).First(&u).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"group_id": groupID,
				"user_id":  userID,
			}).Error("user not found when attempting to delete delivery records for group user was removed from")
			return
		} else {
			log.WithFields(log.Fields{
				"error":   err.Error(),
				"user_id": userID,
			}).Fatal("database error looking up user")
		}
	}

	// Delete all delivery records for this user for items in this group, and send the removal directly
	for _, dev := range u.Devices {
		// Delete the delivery records for each group message
		gms := []groupMessage{}
		err = b.database.Select("id").Where("destination = ?", groupID).Find(&gms).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"group_id": groupID,
			}).Fatal("database error looking up all group messages for a group")
		}
		for _, gm := range gms {
			err = b.database.Exec("DELETE FROM delivery_records WHERE destination = ? AND frame_type = ? AND frame_id = ?", dev.Address, typeGroupMessage, gm.ID).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error":    err.Error(),
					"user_id":  userID,
					"group_id": groupID,
				}).Fatal("database error deleting delivery records")
			}
		}

		// Delete the delivery records for each update group
		ugs := []updateGroup{}
		err = b.database.Select("id").Where("target = ?", groupID).Find(&ugs).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"group_id": groupID,
			}).Fatal("database error looking up all updates for a group")
		}
		for _, ugToDelete := range ugs {
			// Find any confirmations for this update group
			var confirmations []confirmation
			err = b.database.Select("id").Where("update_group_id = ?", ugToDelete.ID).Find(&confirmations).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error":    err.Error(),
					"group_id": groupID,
				}).Fatal("database error looking up all confirmations for an update group")
			}
			for _, c := range confirmations {
				err = b.database.Exec("DELETE FROM delivery_records WHERE destination = ? AND frame_type = ? AND frame_id = ?", dev.Address, typeConfirmation, c.ID).Error
				if err != nil {
					log.WithFields(log.Fields{
						"error":    err.Error(),
						"user_id":  userID,
						"group_id": groupID,
					}).Fatal("database error deleting delivery records")
				}
			}

			// Delete the delivery records for the update group
			err = b.database.Exec("DELETE FROM delivery_records WHERE destination = ? AND frame_type = ? AND frame_id = ?", dev.Address, typeUpdateGroup, ugToDelete.ID).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error":    err.Error(),
					"user_id":  userID,
					"group_id": groupID,
				}).Fatal("database error deleting delivery records")
			}
		}

		// Delete the delivery records for the original group creation
		err = b.database.Exec("DELETE FROM delivery_records WHERE destination = ? AND frame_type = ? AND frame_id = ?", dev.Address, typeGroupCreation, groupID).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"user_id":  userID,
				"group_id": groupID,
			}).Fatal("database error deleting delivery records")
		}
	}
}

func (b *bounce) pruneMessagesBeforeClear(clearBefore int64, groupID uuid.UUID) {
	gms := []groupMessage{}
	err := b.database.Select("id").Where("written_at <= ? AND destination = ?", clearBefore, groupID).Find(&gms).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error selecting group messages to delete while clearing chat history")
	}
	for _, gm := range gms {
		err := b.database.Delete(&gm).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
				"id":    gm.ID,
			}).Fatal("error deleting group message while clearing chat history")
		}
		b.userInterface.DeleteItem(gm.ID)
	}
}

func (b *bounce) clearDeliveryRecordsForFailedDelete(groupID, updateGroupID uuid.UUID) {
	// Check if we've already cleared delivery records as a result of this update group
	var g group
	err := b.database.Select("delivery_records_cleared_for").Where("id = ?", groupID).Find(&g).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error":    err.Error(),
			"group_id": groupID,
		}).Error("error finding group while clearing delivery records after failed delete")
		return
	}

	if g.DeliveryRecordsClearedFor != updateGroupID {
		// Store on the group that we've cleared delivery records as a result of this update group
		err = b.database.Model(&group{}).Where("id = ?", groupID).Select("delivery_records_cleared_for").Update("delivery_records_cleared_for", updateGroupID).Error
		if err != nil {
			log.WithFields(log.Fields{
				"group_id": groupID,
			}).Error("error updating delivery records cleared for field on group")
			return
		}

		// Clear all delivery records for this group's group creation
		err = b.database.Exec("DELETE FROM delivery_records WHERE frame_type = ? AND frame_id = ?", typeGroupCreation, groupID).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"group_id": groupID,
			}).Fatal("database error deleting delivery records")
		}

		// Clear all delivery records for all update groups in this group
		var ugs []updateGroup
		err = b.database.Select("id").Where("target = ?", groupID).Find(&ugs).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"group_id": groupID,
			}).Fatal("database error getting all update groups")
		}
		for _, ug := range ugs {
			err = b.database.Exec("DELETE FROM delivery_records WHERE frame_type = ? AND frame_id = ?", typeUpdateGroup, ug.ID).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error":    err.Error(),
					"group_id": groupID,
				}).Fatal("database error deleting delivery records")

			}
		}

		// Clear all delivery records for all group messages in this group
		var gms []groupMessage
		err = b.database.Select("id").Where("destination = ?", groupID).Find(&gms).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"group_id": groupID,
			}).Fatal("database error getting all group messages")
		}
		for _, gm := range gms {
			err = b.database.Exec("DELETE FROM delivery_records WHERE frame_type = ? AND frame_id = ?", typeGroupMessage, gm.ID).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error":    err.Error(),
					"group_id": groupID,
				}).Fatal("database error deleting delivery records")

			}
		}

		b.referenceAllOnlineDevicesInGroup(groupID)
	}
}

func (b *bounce) referenceAllOnlineDevicesInGroup(groupID uuid.UUID) {
	// Get all the user IDs in this group
	//var userIDs []uuid.UUID
	//err := b.database.Table("group_users").
	//	Select("user_id").
	//	Where("group_id = ?", groupID).
	//	Find(&userIDs).
	//	Error
	//if err != nil {
	//	log.WithFields(log.Fields{
	//		"error": err.Error(),
	//	}).Fatal("database error getting user IDs in group")
	//}
	cs, ok := b.consensusStore.groups[groupID]
	if !ok {
		log.WithFields(log.Fields{
			"group_id": groupID,
		}).Error("cannot reference online devices of group without consensus stack")
		return
	}
	gs, err := cs.top()
	if err != nil {
		log.WithFields(log.Fields{
			"group_id": groupID,
			"error":    err.Error(),
		}).Error("cannot reference online devices of group without consensus stack")
		return
	}

	// Get all the addresses for all these users devices, excluding this device
	var addresses []string
	err = b.database.Table("devices").
		Select("address").
		Where("address != ? AND user_id IN (?)", b.network.Address(), gs.users). //userIDs).
		Find(&addresses).
		Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error getting user IDs in group")
	}

	// Send references to any online devices
	for _, addr := range addresses {
		rd := b.getRemoteDevice(addr)
		if rd.connectedSockets.Load() > 1 {
			go b.sendReferences(addr)
		}
	}
}
