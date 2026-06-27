package chat

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type consensusStore struct {
	sync.Mutex

	groups map[uuid.UUID]*canonicalStack
}

func (b *Bounce) currentGroupStack(groupID uuid.UUID) (*canonicalStack, error) {
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

func (b *Bounce) currentGroupState(groupID uuid.UUID) (groupState, error) {
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

func (b *Bounce) reloadGroupConsensus(groupID uuid.UUID) error {
	b.consensusStore.Lock()
	defer b.consensusStore.Unlock()

	// Create the initial group state from the group creation and use that to start history
	initialState, addressMap, revokedMap, err := b.createInitialGroupState(groupID)
	if err != nil {
		log.WithFields(log.Fields{
			"group_id": groupID,
			"error":    err.Error(),
		}).Debug("error creating initial state while updating group consensus")
		return err
	}
	cs := newCanonicalStack(initialState, addressMap, revokedMap, b.currentUserID())

	// Get all update groups for this group
	var ugs []updateGroup
	err = b.database.Preload(clause.Associations).Where("target = ? AND custom_scope = ?", groupID, uuid.Nil).Order("timestamp asc").Find(&ugs).Order("id").Find(&ugs).Error
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

	return nil
}

func (b *Bounce) reloadGroupConsensusSince(groupID uuid.UUID, ts int64) error {
	b.consensusStore.Lock()

	// Reload everything if timestamp is 0
	if ts == 0 {
		b.consensusStore.Unlock()
		return b.reloadGroupConsensus(groupID)
	}

	// Find the stack, reload everything if we don't have a stack yet
	stack, ok := b.consensusStore.groups[groupID]
	if !ok {
		b.consensusStore.Unlock()
		return b.reloadGroupConsensus(groupID)
	}
	defer b.consensusStore.Unlock()

	// Remove updates that are at or newer than timestamp from the stack
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
	err := b.database.Preload(clause.Associations).Where("target = ? AND timestamp >= ? AND custom_scope = ?", groupID, ts, uuid.Nil).Order("timestamp asc").Find(&ugs).Order("id").Find(&ugs).Error
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

	return nil
}

func (b *Bounce) writeGroupConsensus(groupID uuid.UUID) error {
	b.consensusStore.Lock()
	defer b.consensusStore.Unlock()

	// Get the canonical history stack
	stack, ok := b.consensusStore.groups[groupID]
	if !ok {
		return errors.New("cannot write group consensus for group without a history stack")
	}

	// Get all update groups for this group
	var ugs []updateGroup
	err := b.database.Find(&ugs, "target = ?", groupID).Error
	if err != nil {
		log.WithFields(log.Fields{
			"group_id": groupID,
			"error":    err.Error(),
		}).Fatal("database error looking up update groups")
	}

	// Apply the group state to the database and UI, marking updates in the database as applied or not where needed
	return b.setRollbacksApplicationsAndGroupState(groupID, stack, ugs)
}

func (b *Bounce) setRollbacksApplicationsAndGroupState(groupID uuid.UUID, cs *canonicalStack, ugs []updateGroup) error {
	finalState, err := cs.top()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("no final state available when updating group consensus state")
		return err
	}

	applied := map[uuid.UUID]bool{}
	for _, ug := range ugs {
		if ug.Applied {
			applied[ug.ID] = true
		}
	}

	notified := map[uuid.UUID]bool{}
	for _, ug := range ugs {
		if ug.Notified {
			notified[ug.ID] = true
		}
	}

	// Create a list of all of the users included in this group, starting with the user who created it
	initialGroup, err := b.groupCreationEmbeddedGroup(groupID)
	if err != nil {
		// If we're trying to update consensus on a group that we do not have a group creation for,
		// it is possible we are handling frames we used to block the group.  In that case, make sure
		// the group is blocked.
		var ugs []updateGroup
		ugErr := b.database.Preload(clause.Associations).Where(
			"target = ? AND type = ? AND actor = ?",
			groupID,
			updateGroupTypeBlock,
			b.currentUserID(),
		).Order("timestamp asc").Find(&ugs).Error
		if ugErr != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up update groups")
		}

		if len(ugs) == 0 {
			// There were no blocking update groups for this group and we don't have the group creation, this is unexpected
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("no group creations or blocking updates associated with group consensus update target")
			return err
		} else {
			for _, ug := range ugs {
				if !ug.Applied {
					b.addBlockedGroup(ug.Target)
					err = b.database.Model(&ug).Select("applied").Update("applied", true).Error
					if err != nil {
						log.WithFields(log.Fields{
							"id":    ug.ID,
							"error": err.Error(),
						}).Error("error setting applied on update group that blocks group")
					}
				}
			}
			return nil
		}
	}
	allUsers := []user{initialGroup.Users[0]}

	// Find any canonical update groups that have not been applied and make them as applied and inform the UI if needed
	canonical := make(map[uuid.UUID]bool)
	everInGroup := make(map[uuid.UUID]bool)
	ugsToNotify := []updateGroup{}
	for i, gs := range cs.history[1:] {
		canonical[gs.ug.ID] = true
		if _, ok := applied[gs.ug.ID]; !ok {
			err := b.database.Model(&gs.ug).Select("applied").Update("applied", true).Error
			if err != nil {
				return err
			}

			if gs.ug.Type == updateGroupTypeInviteUser {
				var newUser user
				err := msgpack.Unmarshal(gs.ug.Data, &newUser)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("update group add user container invalid user data")
					return err
				}
				if finalState.isMember(newUser.ID) || finalState.isInvited(newUser.ID) {
					allUsers = append(allUsers, newUser)
				}
			}

			// If we were a member of the group for this update, and we are still in the group and it is not deleted, broadcast a confirmation
			if gs.isMember(b.currentUserID()) {
				if gs.ug.Actor != b.currentUserID() && gs.ug.Type != updateGroupTypeBlock && gs.ug.Type != updateGroupTypeRespondToInvite {
					// We only want confirmations to be send to devices that are in the group in the final group state, so we
					// defer the call to sending confirmations so that it happens after the database has been updated
					if finalState.deletedBy == nil && !finalState.isBlocked(b.currentUserID()) && finalState.isMember(b.currentUserID()) {
						b.sendConfirmation(gs.ug)
					}
				}
			}
		}

		if _, ok := notified[gs.ug.ID]; !ok {
			if finalState.deletedBy == nil && finalState.isMember(b.currentUserID()) {
				ugsToNotify = append(ugsToNotify, cs.history[i+1].ug)

				err := b.database.Model(&gs.ug).Select("notified").Update("notified", true).Error
				if err != nil {
					return err
				}
			}
		}

		for _, member := range gs.users {
			everInGroup[member] = true
		}
		for _, member := range gs.invites {
			everInGroup[member] = true
		}
	}

	// We can only be invited to groups by people we were aware of before they invited us to a group, this prevents a user who knows our details
	// from being able to bootstrap themselves into our contacts without being introduced by someone we already know.  If we didn't create the
	// group, we check to make sure that whoever invited us already existed in our database.
	if initialGroup.Users[0].ID != b.currentUserID() {
		var invitedByUser user
		err = b.database.Select("id").First(&invitedByUser, "id = ?", finalState.invitedBy).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				err = b.database.Exec("DELETE FROM group_creations WHERE id = ?", groupID).Error
				if err != nil {
					log.WithFields(log.Fields{
						"group_id": groupID,
						"error":    err.Error(),
					}).Error("error removing group creation from unauthorized group")
				}

				err = b.database.Exec("DELETE FROM update_groups WHERE target = ?", groupID).Error
				if err != nil {
					log.WithFields(log.Fields{
						"group_id": groupID,
						"error":    err.Error(),
					}).Error("error removing update groups from unauthorized group")
				}

				return nil
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error looking up user")
			}
		}
	}

	// If we blocked this group, save that on user, custom scope our block update, and delete the group
	if finalState.blockedBy != nil {
		// Add this group to our list of blocked groups
		b.addBlockedGroup(groupID)

		// Find our block update and custom scope it
		err = b.createCustomScopeFromGroup(groupID)
		if err == nil {
			err = b.database.Model(&updateGroup{}).Where("id = ?", finalState.blockedBy.ID).Select("custom_scope").Update("custom_scope", groupID).Error
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
		go b.ui.GroupDeleted(GroupDeleted{
			Group: groupID,
			Actor: b.currentUserID(),
		})

		// Delete the group
		delete(b.consensusStore.groups, groupID)
		return b.database.Delete(&initialGroup).Error
	}

	// If the final state is that the group is deleted, delete the group
	if finalState.deletedBy != nil {
		// Attach a custom scope to this update group
		err = b.createCustomScopeFromGroup(groupID)
		if err == nil {
			err = b.database.Model(&updateGroup{}).Where("id = ?", finalState.deletedBy.ID).Select("custom_scope").Update("custom_scope", groupID).Error
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

			if gs.ug.Type == updateGroupTypeInviteUser || gs.ug.Type == updateGroupTypeRemoveUser || gs.ug.Type == updateGroupTypePromoteAdmin || gs.ug.Type == updateGroupTypeDemoteAdmin {
				ugsWithAdminStatusSideEffects = append(ugsWithAdminStatusSideEffects, gs.ug)
			}
		}

		// If this actor was ever not an admin, we need to preserve the history
		if !alwaysAnAdmin {
			// Find the custom scope we just created
			var cs customScope
			err = b.database.First(&cs, "id = ?", groupID).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					log.WithFields(log.Fields{
						"id": groupID,
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
						if b.devicePool.isRevoked(addr) {
							continue
						}
						if !b.isDeliveredTo(&ug, addr) {
							allDelivered = false
						}
					}

					if !allDelivered {
						err = b.database.Model(&ug).Select("custom_scope").Update("custom_scope", groupID).Error
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
		go b.ui.GroupDeleted(GroupDeleted{
			Group: groupID,
			Actor: finalState.ug.Actor,
		})

		// Delete the group
		delete(b.consensusStore.groups, groupID)
		return b.database.Delete(&initialGroup).Error
	}

	// If the final state involves us being removed from the group, delete the group
	if finalState.removedBy != nil {
		if finalState.removedBy.CustomScope == uuid.Nil {
			// Attach a custom scope to this update group
			err = b.createCustomScopeFromGroup(groupID)
			if err == nil {
				err = b.database.Model(&updateGroup{}).Where("id = ?", finalState.removedBy.ID).Select("custom_scope").Update("custom_scope", groupID).Error
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
		}

		// TODO: we only need to do these things if the UI is already aware of the group
		// Inform the UI
		go b.ui.RemovedFromGroup(RemovedFromGroup{
			Group: groupID,
			Actor: finalState.removedBy.Actor,
		})

		// Delete the group
		delete(b.consensusStore.groups, groupID)
		return b.database.Delete(&initialGroup).Error
	}

	if !finalState.isMember(b.currentUserID()) && !finalState.isInvited(b.currentUserID()) {
		// We are not a member of this group nor have we been invited.  We also haven't blocked it, been removed from it, or deleted it.
		// This indicates that our previous invitation to the group has been rolled back by a consensus operation.  We should remove
		// everything about the group from this device, aside from custom sync scoping the updates, and remove it from the UI.
		b.cleanupRolledBackInvite(groupID)
		b.ui.RollbackGroup(groupID)
	}

	// Find any non-canonical update groups that have been applied and mark them as not applied roll them back in the UI
	for _, ug := range ugs {
		if _, ok := canonical[ug.ID]; !ok {
			if ug.Applied {
				err := b.database.Model(&ug).Select("applied").Update("applied", false).Error
				if err != nil {
					return err
				}
				go b.ui.DeleteItem(ug.ID)
			}
		}
	}

	// Clear delivery records for users that were removed in the final state
	for userID, _ := range everInGroup {
		if !finalState.isMember(userID) && !finalState.isInvited(userID) {
			b.clearGroupDeliveryRecordsForUser(userID, groupID)
		}
	}

	// If there was a failed attempt to delete the group, clear all delivery records once in order to restore the group
	// for any devices that applied the deletion
	var failedDelete updateGroup
	err = b.database.
		Select("id", "MAX(timestamp)").
		Where("target = ? AND type = ? AND applied = false", groupID, updateGroupTypeDelete).
		Find(&failedDelete).
		Error
	if err == nil {
		b.clearDeliveryRecordsForFailedDelete(groupID, failedDelete.ID)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"group_id": groupID,
			"error":    err.Error(),
		}).Fatal("database error looking for unapplied update group delete")
	}

	// If this group contains a blocked user, leave the group if we're in it, or reject it if we're invited
	hasBlockedUser := false
	for _, userID := range finalState.users {
		if blockedUser(userID) {
			hasBlockedUser = true
			break
		}
	}
	for _, userID := range finalState.invites {
		if blockedUser(userID) {
			hasBlockedUser = true
			break
		}
	}
	if hasBlockedUser {
		// Create an update to block or leave the group depending on our status
		var injectedUpdate updateGroup
		if finalState.isMember(b.currentUserID()) {
			currentUserID := b.currentUserID()
			injectedUpdate = updateGroup{
				ID:        uuid.New(),
				Actor:     b.currentUserID(),
				Target:    groupID,
				Timestamp: time.Now().Unix(),
				Type:      updateGroupTypeRemoveUser,
				Data:      currentUserID[:],
			}
		} else {
			injectedUpdate = updateGroup{
				ID:        uuid.New(),
				Actor:     b.currentUserID(),
				Target:    groupID,
				Timestamp: time.Now().Unix(),
				Type:      updateGroupTypeRespondToInvite,
				Data:      []byte{rejectInvite},
			}
		}
		// Create a custom scope since we are not going to keep this group on disk
		scope := customScope{
			ID:        groupID,
			Addresses: strings.Join(finalState.scope(), ","),
		}
		err = b.database.Create(&scope).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error saving custom scope")
		}
		injectedUpdate.CustomScope = scope.ID

		// Sign it
		injectedUpdate.OriginalPayload, err = msgpack.Marshal(injectedUpdate)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error marshalling group update")
		}
		sc := b.createSignedContainer(injectedUpdate.OriginalPayload)
		injectedUpdate.Signature = sc.Signature
		injectedUpdate.Signer = sc.Signer

		// Save the update
		err = b.database.Create(&injectedUpdate).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error saving update group")
		}

		// Push it onto the canonical stack
		err = cs.push(injectedUpdate)
		if err != nil {
			log.Error("error pushing group invite auto-injected update into canonical stack")
		} else {
			// Broadcast it and recursively set the state
			b.broadcast(&injectedUpdate)
			return b.setRollbacksApplicationsAndGroupState(groupID, cs, append(ugs, injectedUpdate))
		}

	}

	// If we're invited, check our policy for automatically accepting group invites, and act on it if needed
	if finalState.isInvited(b.currentUserID()) {
		// Make sure that if we've set this setting before, we're only using the setting if it was set before this invite.
		// This prevents changing the setting from retroactively causing any invites to be accepted.
		if ts := b.lastAutoJoinGroupSettingChange(); ts == 0 || ts < finalState.invitedAt {
			u, ok := b.currentUser()
			if !ok {
				log.Error("current user doesn't exist when updating group state")
				return errUserNotFound
			}

			var usersCreatedByGroup []user
			err = b.database.Where("introduction_metadata = ?", groupID).Find(&usersCreatedByGroup).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error looking up users created by group")
			}
			allUsersCreated := true
			for _, userID := range finalState.users {
				var test user
				err := b.database.Select("id").Take(&test, "id = ?", userID).Error
				if err != nil {
					allUsersCreated = false
				}
			}
			for _, userID := range finalState.invites {
				var test user
				err := b.database.Select("id").Take(&test, "id = ?", userID).Error
				if err != nil {
					allUsersCreated = false
				}
			}
			noNewUsers := len(usersCreatedByGroup) == 0 && allUsersCreated

			if u.ProfileSettings.AutoJoinGroups == AlwaysAutoJoinGroups || (u.ProfileSettings.AutoJoinGroups == OnlyAutoJoinGroupsWithNoNewUsers && noNewUsers) {
				// Create an update group to auto-accept
				accept := updateGroup{
					ID:        uuid.New(),
					Actor:     b.currentUserID(),
					Target:    groupID,
					Timestamp: time.Now().Unix(),
					Type:      updateGroupTypeRespondToInvite,
					Data:      []byte{acceptInvite},
				}
				accept.OriginalPayload, err = msgpack.Marshal(accept)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Fatal("error marshalling group update")
				}
				sc := b.createSignedContainer(accept.OriginalPayload)
				accept.Signature = sc.Signature
				accept.Signer = sc.Signer

				err = b.database.Create(&accept).Error
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Fatal("database error saving update group")
				}

				// Push it onto the canonical stack
				err = cs.push(accept)
				if err != nil {
					log.Error("error pushing group invite auto-accept into canonical stack")
				} else {
					// Broadcast it and recursively set the state
					// TODO: doesn't work because of the return below
					return b.setRollbacksApplicationsAndGroupState(groupID, cs, append(ugs, accept))
				}
			}
		}
	}

	err = b.setGroupStateInDatabase(initialGroup, allUsers, finalState, ugsToNotify)
	if err != nil {
		if err != nil {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"group_id": groupID,
			}).Error("error updating group consensus")
		}

	}
	return err
}

func (b *Bounce) setGroupStateInDatabase(initialGroup group, allUsers []user, gs groupState, ugsToNotify []updateGroup) error {
	newUsers := make(map[uuid.UUID]bool)
	for _, u := range allUsers {
		newUsers[u.ID] = b.createNewUserIfNeeded(u, initialGroup.ID)
	}
	var g group
	err := b.database.Preload(clause.Associations).Where("id = ?", initialGroup.ID).First(&g).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			g = initialGroup
			err = b.database.Create(&initialGroup).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error":    err.Error(),
					"group_id": initialGroup.ID,
				}).Fatal("database error creating group")
			}
			b.GroupConnectionDesired(g.ID)

			if gs.isMember(b.currentUserID()) {
				if b.postNotification != nil {
					deferToAnotherDevice := !uiIsInForeground.Load() && anotherDeviceIsActive.Load()
					if !deferToAnotherDevice {
						b.postNotification(initialGroup.Name, "You have been added to a group")
					}
				}
			} else {
				if b.postNotification != nil {
					deferToAnotherDevice := !uiIsInForeground.Load() && anotherDeviceIsActive.Load()
					if !deferToAnotherDevice {
						b.postNotification(initialGroup.Name, "You have been invited to a group")
					}
				}
			}
		} else {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"group_id": initialGroup.ID,
			}).Fatal("database error looking up group")
		}
	}
	b.updateLastGroupActivity(initialGroup.ID, gs.ug.Timestamp)

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
	if g.InvitedBy != gs.invitedBy {
		err := b.database.Model(&g).Select("invited_by").Update("invited_by", gs.invitedBy).Error
		if err != nil {
			return err
		}
	}
	if g.InvitedAt != gs.invitedAt {
		err := b.database.Model(&g).Select("invited_at").Update("invited_at", gs.invitedAt).Error
		if err != nil {
			return err
		}
	}
	if g.AcceptedAt != gs.acceptedAt {
		err := b.database.Model(&g).Select("accepted_at").Update("accepted_at", gs.acceptedAt).Error
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
		err := b.database.First(&u, "id = ?", userID).Error
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
		finalUsers = append(finalUsers, User{
			ID:               u.ID,
			Name:             u.Name,
			Alias:            u.Alias,
			Notes:            u.Notes,
			Blocked:          u.Blocked,
			Images:           u.images(),
			IntroductionTime: u.IntroductionTime,
		})

		if !b.userIsInGroup(g.ID, userID) {
			err = b.database.Exec("INSERT INTO group_users VALUES(?, ?)", g.ID, userID).Error
			if err != nil {
				if !errors.Is(err, gorm.ErrDuplicatedKey) {
					return err
				}
			}
		}
	}

	// Set the image history
	imageIDStrings := []string{}
	for _, imageID := range gs.images {
		imageIDStrings = append(imageIDStrings, imageID.String())
	}
	err = b.database.Model(&g).Select("images").Update("images", strings.Join(imageIDStrings, ",")).Error
	if err != nil {
		return err
	}

	// Set group admins
	admins := []uuid.UUID{}
	if len(g.Admins) > 0 {
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
	}
	for _, adminID := range admins {
		if !gs.isAdmin(adminID) {
			b.removeGroupAdmin(g.ID, adminID)
		}
	}
	for _, adminID := range gs.admins {
		if !b.isGroupAdmin(g.ID, adminID) {
			b.addGroupAdmin(g.ID, adminID)
		}
	}

	// Set the invited users
	invited := []uuid.UUID{}
	if len(g.Invites) > 0 {
		for _, invitedIDString := range strings.Split(g.Invites, ",") {
			invitedID, err := uuid.Parse(invitedIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":    err.Error(),
					"group_id": g.ID,
					"invited":  g.Invites,
				}).Fatal("invalid UUID in group invited list")

			}
			invited = append(invited, invitedID)
		}
	}
	for _, invitedID := range invited {
		if !gs.isInvited(invitedID) {
			b.removeGroupInvite(g.ID, invitedID)
		}
	}
	finalInvites := []User{}
	for _, invitedID := range gs.invites {
		if !b.isInvited(g.ID, invitedID) {
			b.addGroupInvite(g.ID, invitedID)

			// Add this user as a recipient for the group creation and updates for any encrypted devices
			go b.addInvitedUserAsEncryptedRecipient(invitedID, g.ID)
		}

		var u user
		err := b.database.First(&u, "id = ?", invitedID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"error":   err.Error(),
					"user_id": invitedID,
				}).Error("group final state contains invited user not in database")
				return err
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error looking up user")
			}
		}
		finalInvites = append(finalInvites, User{
			ID:               u.ID,
			Name:             u.Name,
			Alias:            u.Alias,
			Notes:            u.Notes,
			Blocked:          u.Blocked,
			Images:           u.images(),
			IntroductionTime: u.IntroductionTime,
		})
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

	// Mark every update group and group message that occured before the last time we accepted the invite as having already been seen
	if gs.acceptedAt != 0 {
		err := b.database.Exec("UPDATE update_groups SET seen = true WHERE target = ? AND timestamp < ?", g.ID, gs.acceptedAt).Error
		if err != nil {
			log.WithFields(log.Fields{
				"group_id": g.ID,
				"error":    err.Error(),
			}).Error("error marking update groups that occured before accepting the group as seen")
		}
		err = b.database.Exec("UPDATE group_messages SET seen = true WHERE destination = ? AND written_at < ?", g.ID, gs.acceptedAt).Error
		if err != nil {
			log.WithFields(log.Fields{
				"group_id": g.ID,
				"error":    err.Error(),
			}).Error("error marking group messages that occured before accepting the group as seen")
		}
	}

	go func() {
		b.ui.SetGroupState(Group{
			ID:                             g.ID,
			Name:                           g.Name,
			Images:                         gs.images,
			Users:                          finalUsers,
			Admins:                         gs.admins,
			Invites:                        finalInvites,
			InvitedBy:                      gs.invitedBy,
			InvitedAt:                      gs.invitedAt,
			AcceptedAt:                     gs.acceptedAt,
			BlockedUsers:                   gs.blockedUsers,
			Retention:                      g.Retention,
			MutedUntil:                     g.MutedUntil,
			LastActivity:                   g.LastActivity,
			CreatedBy:                      g.CreatedBy,
			CreatedAt:                      g.CreatedAt,
			RestrictUserManagement:         g.RestrictUserManagement,
			RestrictGroupEdits:             g.RestrictGroupEdits,
			RestrictPosting:                g.RestrictPosting,
			OverrideReadReceiptSetting:     g.ReadReceiptsOverridden,
			ReadReceiptsEnabled:            g.ReadReceiptsEnabled,
			OverrideTypingIndicatorSetting: g.TypingIndicatorsOverridden,
			TypingIndicatorsEnabled:        g.TypingIndicatorsEnabled,
		})

		for _, ug := range ugsToNotify {
			if gs.acceptedAt != 0 && ug.Timestamp < gs.acceptedAt {
				ug.Seen = true
			}
			b.informUIOfUpdateGroup(ug)
		}

		for id, created := range newUsers {
			if created {
				// For users that were just created, start the process of
				// figuring out a shared DM rentention setting
				b.updateDMState(id)
			}
		}
	}()

	b.referenceAllOnlineDevicesInGroup(g.ID)

	return nil
}

func (b *Bounce) createNewUserIfNeeded(u user, groupID uuid.UUID) bool {
	if u.ID == b.currentUserID() {
		return false
	}

	// Ensure the user is valid
	if !b.hasValidDeviceGroup(u) {
		log.WithFields(log.Fields{
			"user_id": u.ID,
		}).Warn("refusing to save user with invalid device group")
		return false
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
	u.IntroductionMethod = userIntroductionGroup
	u.IntroductionTime = time.Now().Unix()
	u.IntroductionMetadata = groupID
	res := b.database.Clauses(clause.OnConflict{DoNothing: true}).Create(&u)
	if res.Error != nil {
		log.WithFields(log.Fields{
			"error": res.Error.Error(),
		}).Fatal("error saving user that is being added to a group")
	}

	// Attempt to make a connection to the user
	b.UserConnectionDesired(u.ID)

	return res.RowsAffected == 1
}

func (b *Bounce) clearGroupDeliveryRecordsForUser(userID, groupID uuid.UUID) {
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

	// Get the images in the group
	var g group
	err = b.database.Select("images").Where("id = ?", groupID).Find(&g).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"group_id": groupID,
				"user_id":  userID,
			}).Error("group not found when attempting to delete delivery records for group user was removed from")
			return
		} else {
			log.WithFields(log.Fields{
				"error":   err.Error(),
				"user_id": userID,
			}).Fatal("database error looking up group")
		}
	}
	imageHistory := []uuid.UUID{}
	if len(g.Images) > 0 {
		for _, imageIDString := range strings.Split(g.Images, ",") {
			imageID, err := uuid.Parse(imageIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":  err.Error(),
					"images": g.Images,
				}).Fatal("invalid UUID in group images list")
			}
			imageHistory = append(imageHistory, imageID)
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

			// Clear delivery records for any files associated with this message
			fas := []fileAttachment{}
			err = b.database.Select("file_id").Where("message_id = ?", gm.ID).Find(&fas).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error looking up all file attachments for a message")
			}
			for _, fa := range fas {
				offers := []chunkOffer{}
				err := b.database.Select("id").Where("file_id = ?", fa.FileID).Find(&offers).Error
				if err != nil {
					log.WithFields(log.Fields{
						"error":    err.Error(),
						"user_id":  userID,
						"group_id": groupID,
					}).Fatal("database error deleting delivery records")
				}
				for _, offer := range offers {
					err = b.database.Exec("DELETE FROM delivery_records WHERE destination = ? AND frame_type = ? AND frame_id = ?", dev.Address, typeChunkOffer, offer.ID).Error
					if err != nil {
						log.WithFields(log.Fields{
							"error":    err.Error(),
							"user_id":  userID,
							"group_id": groupID,
						}).Fatal("database error deleting delivery records")
					}
				}

				err = b.database.Exec("DELETE FROM delivery_records WHERE destination = ? AND frame_type = ? AND frame_id = ?", dev.Address, typeFile, fa.FileID).Error
				if err != nil {
					log.WithFields(log.Fields{
						"error":    err.Error(),
						"user_id":  userID,
						"group_id": groupID,
					}).Fatal("database error deleting delivery records")
				}
			}

			ias := []imageAttachment{}
			err = b.database.Select("file_id").Where("message_id = ?", gm.ID).Find(&ias).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error looking up all image attachments for a message")
			}
			for _, ia := range ias {
				offers := []chunkOffer{}
				err := b.database.Select("id").Where("file_id = ?", ia.FileID).Find(&offers).Error
				if err != nil {
					log.WithFields(log.Fields{
						"error":    err.Error(),
						"user_id":  userID,
						"group_id": groupID,
					}).Fatal("database error deleting delivery records")
				}
				for _, offer := range offers {
					err = b.database.Exec("DELETE FROM delivery_records WHERE destination = ? AND frame_type = ? AND frame_id = ?", dev.Address, typeChunkOffer, offer.ID).Error
					if err != nil {
						log.WithFields(log.Fields{
							"error":    err.Error(),
							"user_id":  userID,
							"group_id": groupID,
						}).Fatal("database error deleting delivery records")
					}
				}

				err = b.database.Exec("DELETE FROM delivery_records WHERE destination = ? AND frame_type = ? AND frame_id = ?", dev.Address, typeFile, ia.FileID).Error
				if err != nil {
					log.WithFields(log.Fields{
						"error":    err.Error(),
						"user_id":  userID,
						"group_id": groupID,
					}).Fatal("database error deleting delivery records")
				}
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

		// Clear delivery records for any files or file related frames for this group
		err = b.database.Exec("DELETE FROM delivery_records WHERE destination = ? AND frame_type = ? AND frame_id IN (?)", dev.Address, typeFile, imageHistory).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"user_id":  userID,
				"group_id": groupID,
			}).Fatal("database error deleting delivery records")
		}

		for _, img := range imageHistory {
			offers := []chunkOffer{}
			err := b.database.Select("id").Where("file_id = ?", img).Find(&offers).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error":    err.Error(),
					"user_id":  userID,
					"group_id": groupID,
				}).Fatal("database error deleting delivery records")
			}
			for _, offer := range offers {
				err = b.database.Exec("DELETE FROM delivery_records WHERE destination = ? AND frame_type = ? AND frame_id = ?", dev.Address, typeChunkOffer, offer.ID).Error
				if err != nil {
					log.WithFields(log.Fields{
						"error":    err.Error(),
						"user_id":  userID,
						"group_id": groupID,
					}).Fatal("database error deleting delivery records")
				}
			}
		}
	}
}

func (b *Bounce) pruneMessagesBeforeClear(clearBefore int64, groupID uuid.UUID) {
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
		go b.ui.DeleteItem(gm.ID)
	}
}

func (b *Bounce) clearDeliveryRecordsForFailedDelete(groupID, updateGroupID uuid.UUID) {
	// Check if we've already cleared delivery records as a result of this update group
	var g group
	err := b.database.Select("images", "delivery_records_cleared_for").Where("id = ?", groupID).Find(&g).Error
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

			// Clear delivery records for any files associated with this message
			fas := []fileAttachment{}
			err = b.database.Select("file_id").Where("message_id = ?", gm.ID).Find(&fas).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error looking up all file attachments for a message")
			}
			for _, fa := range fas {
				err = b.database.Exec("DELETE FROM delivery_records WHERE frame_type = ? AND frame_id = ?", typeFile, fa.FileID).Error
				if err != nil {
					log.WithFields(log.Fields{
						"error":    err.Error(),
						"group_id": groupID,
					}).Fatal("database error deleting delivery records")
				}
			}

			ias := []imageAttachment{}
			err = b.database.Select("file_id").Where("message_id = ?", gm.ID).Find(&ias).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error looking up all image attachments for a message")
			}
			for _, ia := range ias {
				err = b.database.Exec("DELETE FROM delivery_records WHERE frame_type = ? AND frame_id = ?", typeFile, ia.FileID).Error
				if err != nil {
					log.WithFields(log.Fields{
						"error":    err.Error(),
						"group_id": groupID,
					}).Fatal("database error deleting delivery records")
				}
			}
		}

		// Clear delivery records for any group images
		imageHistory := []uuid.UUID{}
		if len(g.Images) > 0 {
			for _, imageIDString := range strings.Split(g.Images, ",") {
				imageID, err := uuid.Parse(imageIDString)
				if err != nil {
					log.WithFields(log.Fields{
						"error":  err.Error(),
						"images": g.Images,
					}).Fatal("invalid UUID in group images list")
				}
				imageHistory = append(imageHistory, imageID)
			}
		}
		err = b.database.Exec("DELETE FROM delivery_records WHERE frame_type = ? AND frame_id IN (?)", typeFile, imageHistory).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"group_id": groupID,
			}).Fatal("database error deleting delivery records")
		}

		b.referenceAllOnlineDevicesInGroup(groupID)
	}
}

func (b *Bounce) referenceAllOnlineDevicesInGroup(groupID uuid.UUID) {
	// Get the current state of the group
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
		Where("address != ? AND user_id IN (?)", b.network.Address(), append(gs.users, gs.invites...)).
		Find(&addresses).
		Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error getting user IDs in group")
	}

	// Send references to any online devices
	for _, addr := range addresses {
		// TODO: check if blocked / revoked?
		rd := b.getRemoteDevice(addr)
		if rd.connectedSockets.Load() >= 1 {
			go b.sendReferences(addr)
		}
	}

	for _, addr := range b.onlineEncryptedDevicesInGroup(groupID) {
		go b.sendReferences(addr)
	}
}

func (b *Bounce) cleanupRolledBackInvite(groupID uuid.UUID) {
	id := b.createCustomScopeForSync()
	err := b.database.Model(&updateGroup{}).Where("target = ?", groupID).Select("custom_scope").Update("custom_scope", id).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error updating custom scope on update groups")
	}

	var g group
	err = b.database.First(&g, "id = ?", groupID).Error
	if err == nil {
		err = b.database.Delete(&g).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"group_id": groupID,
			}).Error("error deleting rollback group")
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error":    err.Error(),
			"group_id": groupID,
		}).Fatal("database error looking up group")
	}
}

func (b *Bounce) addInvitedUserAsEncryptedRecipient(userID, groupID uuid.UUID) {
	for _, address := range b.encryptedDevicesInGroup(groupID) {
		err := b.database.Create(&appendRecipient{
			ID:        uuid.New(),
			GroupID:   groupID,
			UserID:    userID,
			Timestamp: time.Now().Unix(),
			Address:   address,
		}).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error saving new appendRecipient")
			return
		}
	}

	b.addRecipientsIfNeeded()
}

func (b *Bounce) onlineEncryptedDevicesInGroup(groupID uuid.UUID) []string {
	addrs := []string{}

	var g group
	err := b.database.Preload(clause.Associations).Take(&g, "id = ?", groupID).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error getting group when collecting encrypted devices")
		return []string{}
	}
	userIDs := []uuid.UUID{}
	for _, u := range g.Users {
		userIDs = append(userIDs, u.ID)
	}
	if len(g.Invites) > 0 {
		for _, invitedIDString := range strings.Split(g.Invites, ",") {
			invitedID, err := uuid.Parse(invitedIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":    err.Error(),
					"group_id": g.ID,
					"invites":  g.Invites,
				}).Fatal("invalid UUID in group invite list")
			}
			userIDs = append(userIDs, invitedID)
		}
	}
	for _, userID := range userIDs {
		var u user
		err = b.database.Select("encrypted_devices").Take(&u, "id = ?", userID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"user_id": userID,
				}).Error("user not found in group")
				continue
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error getting encrypted devices from user")
			}
		}
		if len(u.EncryptedDevices) > 0 {
			for _, addr := range strings.Split(u.EncryptedDevices, ",") {
				rd := b.getRemoteDevice(addr)
				if rd.connectedSockets.Load() >= 1 {
					addrs = append(addrs, addr)
				}
			}
		}
	}

	return addrs
}
