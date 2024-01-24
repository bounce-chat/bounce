package chat

import (
	"encoding/binary"
	"errors"
	"strings"
	"sync"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var groupConsensusMutex sync.Mutex

var errStackEmpty = errors.New("stack is empty")

type groupState struct {
	name                     string
	users                    []uuid.UUID
	admins                   []uuid.UUID
	mutedUntil               int64
	retention                int64
	clearBefore              int64
	postingRestricted        bool
	editingRestricted        bool
	userManagementRestricted bool
	ug                       updateGroup
}

func (gs groupState) equals(otherState groupState) bool {
	if gs.name != otherState.name {
		return false
	}

	if gs.mutedUntil != otherState.mutedUntil {
		return false
	}

	if gs.retention != otherState.retention {
		return false
	}

	if gs.clearBefore != otherState.clearBefore {
		return false
	}

	if gs.postingRestricted != otherState.postingRestricted {
		return false
	}

	if gs.editingRestricted != otherState.editingRestricted {
		return false
	}

	if gs.userManagementRestricted != otherState.userManagementRestricted {
		return false
	}

	if len(gs.users) != len(otherState.users) {
		return false
	}

	for _, userID := range gs.users {
		if !otherState.isMember(userID) {
			return false
		}
	}

	for _, userID := range otherState.users {
		if !gs.isMember(userID) {
			return false
		}
	}

	if len(gs.admins) != len(otherState.admins) {
		return false
	}

	for _, userID := range gs.admins {
		if !otherState.isAdmin(userID) {
			return false
		}
	}

	for _, userID := range otherState.admins {
		if !gs.isAdmin(userID) {
			return false
		}
	}

	return true
}

func (gs groupState) isAdmin(userID uuid.UUID) bool {
	for _, adminID := range gs.admins {
		if adminID == userID {
			return true
		}
	}
	return false
}

func (gs groupState) isMember(userID uuid.UUID) bool {
	for _, memberID := range gs.users {
		if memberID == userID {
			return true
		}
	}
	return false
}

type canonicalStack struct {
	myID         uuid.UUID
	history      []groupState
	historyStash []groupState
}

func newCanonicalStack(initialState groupState, myID uuid.UUID) *canonicalStack {
	return &canonicalStack{
		myID:    myID,
		history: []groupState{initialState},
	}
}

func (cs *canonicalStack) push(ug updateGroup) error {
	top, err := cs.top()
	if err != nil {
		return err
	}

	updatedGroupState, err := applyUpdateGroupToState(top, ug)
	if err != nil {
		log.WithFields(log.Fields{
			"update_group_id": ug.ID,
			"type":            ug.Type,
			"error":           err.Error(),
		}).Error("cannot push update group onto history stack")
		return err
	}

	cs.history = append(cs.history, updatedGroupState)

	return nil
}

func (cs *canonicalStack) pop() (updateGroup, error) {
	if cs.empty() {
		return updateGroup{}, errStackEmpty
	}

	lastItem := cs.history[len(cs.history)-1]
	cs.history = cs.history[:len(cs.history)-1]
	return lastItem.ug, nil
}

func (cs *canonicalStack) top() (groupState, error) {
	if cs.empty() {
		return groupState{}, errStackEmpty
	}
	return cs.history[len(cs.history)-1], nil
}

func (cs *canonicalStack) empty() bool {
	return len(cs.history) == 0
}

func (cs *canonicalStack) stash() {
	cs.historyStash = cs.history
}

func (cs *canonicalStack) restore() {
	cs.history = cs.historyStash
}

//
// Given an update group, add it into the history stack if it should be applied, detecting and removing any conflicts in the process
//
func (cs *canonicalStack) insertUpdateGroupIntoStack(ug updateGroup) {
	// Get the current state of history
	lastState, err := cs.top()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("cannot insert update group into history")
		return
	}

	// Enforce time ordering on everything after the group creation
	if len(cs.history) > 1 {
		if ug.Timestamp < lastState.ug.Timestamp {
			log.WithFields(log.Fields{
				"previous": lastState.ug.ID,
				"current":  ug.ID,
			}).Fatal("out of order update group inserted into canonical history")
		}
	}

	// Ignore this update if it changes nothing
	if changeIsNOP(lastState, ug) {
		return
	}

	// Check if this user has permission to perform this change
	if err = stateChangeAllowed(lastState, ug, cs.myID); err == nil {
		// If it is allowed, apply it
		cs.push(ug)
	} else {
		// If this change is not allowed, check if it is confirmed
		confirmed := (float64(ug.confirmingUsers(cs.myID)) / float64(len(lastState.users))) > 0.5

		if confirmed {
			// If this change is not allowed and is confirmed, pop through history until the conflicting change is identified
			recheck := []updateGroup{}
			cs.stash()
			for {
				// Remove the lastest change and add it to a slice
				removed, err := cs.pop()
				if err != nil {
					// This shouldn't be possible
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Fatal("error popping group state history stack")
				}
				recheck = append([]updateGroup{removed}, recheck...)

				// If the history is now empty, then this change was never allowed, so we ignore it even though it's confirmed and reset the stack
				if cs.empty() {
					cs.restore()
					return
				}

				// Now that one item has been removed, check if that makes this change allowed
				newTop, err := cs.top()
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Fatal("error getting top of group state history stack")

				}
				if err = stateChangeAllowed(newTop, ug, cs.myID); err == nil {
					// If this chnage is now allwed, then the conflict was the last thing we removed from the stack
					conflict := recheck[0]

					// Check if this conflict was confirmed
					conflictConfirmed := (float64(conflict.confirmingUsers(cs.myID)) / float64(len(newTop.users))) > 0.5
					if conflictConfirmed {
						// If the conflict was confirmed as well, then the conflict wins because it's older, and we ignore this change
						cs.restore()
						break
					} else {
						// If the conflict is not confirmed then we exclude it, and attempt to re-add everything that happened since the conflict was removed
						for _, rc := range recheck[1:] {
							cs.insertUpdateGroupIntoStack(rc)
						}
						break
					}
				}
			}
		} else {
			// If this change is not allowed and not confirmed, do not add it to history
			return
		}
	}
}

func (b *bounce) buildCanonicalHistoryStack(groupID uuid.UUID) (*canonicalStack, []updateGroup) {
	// Create the initial group state from the group creation and use that to start history
	initialState, err := b.createInitialGroupState(groupID)
	if err != nil {
		log.WithFields(log.Fields{
			"group_id": groupID,
			"error":    err.Error(),
		}).Error("error creating initial state while updating group consensus")
		return &canonicalStack{}, []updateGroup{}
	}
	cs := newCanonicalStack(initialState, b.currentUserID())

	// Get all update groups for this group
	var ugs []updateGroup
	err = b.database.Preload(clause.Associations).Where("group_id = ?", groupID).Order("timestamp asc").Find(&ugs).Error
	if err != nil {
		log.WithFields(log.Fields{
			"group_id": groupID,
			"error":    err.Error(),
		}).Fatal("database error selecting all update groups")
	}

	// For each update group, attempt to add it to history
	for _, ug := range ugs {
		cs.insertUpdateGroupIntoStack(ug)
	}

	return cs, ugs
}

func (b *bounce) updateGroupConsensus(groupID uuid.UUID) {
	groupConsensusMutex.Lock()
	defer groupConsensusMutex.Unlock()

	cs, ugs := b.buildCanonicalHistoryStack(groupID)

	// Track what has been applied and rolled back, inform the UI, and set the group state in the database
	b.setRollbacksApplicationsAndGroupState(groupID, cs, ugs)
}

func (b *bounce) isMemberOfGroupForUpdate(userID, groupID, ugID uuid.UUID) bool {
	cs, _ := b.buildCanonicalHistoryStack(groupID)

	for _, gs := range cs.history {
		if gs.ug.ID == ugID {
			return gs.isMember(userID)
		}
	}

	return false
}

func (b *bounce) createInitialGroupState(groupID uuid.UUID) (groupState, error) {
	// Create the group state
	gs := groupState{
		users:                    []uuid.UUID{},
		admins:                   []uuid.UUID{},
		postingRestricted:        true,
		editingRestricted:        true,
		userManagementRestricted: true,
	}

	// Look up the group creation and unpack the original group
	var gc groupCreation
	err := b.database.Where("id = ?", groupID).First(&gc).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"group_id": groupID,
			}).Error("group creation not found for consensus calculation")
			return gs, err
		} else {
			log.WithFields(log.Fields{
				"group_id": groupID,
				"error":    err.Error(),
			}).Fatal("database error looking up group creation for consensus calculation")
		}
	}

	var g group
	err = msgpack.Unmarshal(gc.Data, &g)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling group")
		return gs, err
	}

	// Set group properties
	gs.name = g.Name
	gs.mutedUntil = g.MutedUntil
	gs.retention = g.Retention
	gs.clearBefore = g.ClearBefore
	gs.postingRestricted = g.RestrictPosting
	gs.editingRestricted = g.RestrictGroupEdits
	gs.userManagementRestricted = g.RestrictUserManagement

	// Set the values for the original members
	for _, u := range g.Users {
		gs.users = append(gs.users, u.ID)
	}

	// Set the values for the original admins
	if len(g.Admins) != 0 { // TODO: still allowed?
		for _, adminIDString := range strings.Split(g.Admins, ",") {
			adminID, err := uuid.Parse(adminIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":    err.Error(),
					"group_id": groupID,
					"admins":   g.Admins,
				}).Fatal("invalid UUID in group admin list")
			}

			gs.admins = append(gs.admins, adminID)
		}
	}

	// Return the state
	return gs, nil

	// TODO: alternatively, the last valid set state works here
}

func changeIsNOP(lastState groupState, ug updateGroup) bool {
	updatedGroupState, err := applyUpdateGroupToState(lastState, ug)
	if err != nil {
		return true
	}

	return updatedGroupState.equals(lastState)
}

func stateChangeAllowed(gs groupState, ug updateGroup, myID uuid.UUID) error {
	if !gs.isMember(ug.Actor) {
		return errUserNotInGroup
	}

	switch ug.Type {
	case updateGroupTypeChangeName:
		if gs.editingRestricted {
			if gs.isAdmin(ug.Actor) {
				return nil
			} else {
				return errNoPermissionToEditGroup
			}
		} else {
			return nil
		}
	case updateGroupTypeAddUser:
		if gs.userManagementRestricted {
			if gs.isAdmin(ug.Actor) {
				return nil
			} else {
				return errNoPermissionToManageUsers
			}
		} else {
			return nil
		}
	case updateGroupTypeRemoveUser:
		if gs.userManagementRestricted {
			if gs.isAdmin(ug.Actor) {
				return nil
			} else {
				return errNoPermissionToManageUsers
			}
		} else {
			return nil
		}
	case updateGroupTypeChangeMutedUntil:
		if myID == ug.Actor {
			return nil
		} else {
			return errMutedUntilOnlyMutableBySelf
		}
	case updateGroupTypeChangeRetention:
		if gs.editingRestricted {
			if gs.isAdmin(ug.Actor) {
				return nil
			} else {
				return errNoPermissionToEditGroup
			}
		} else {
			return nil
		}
	case updateGroupTypeSetClearBefore:
		if gs.editingRestricted {
			if gs.isAdmin(ug.Actor) {
				return nil
			} else {
				return errNoPermissionToEditGroup
			}
		} else {
			return nil
		}
	case updateGroupTypePromoteAdmin:
		if gs.isAdmin(ug.Actor) {
			userID, err := uuid.FromBytes(ug.Data)
			if err != nil {
				log.WithFields(log.Fields{
					"error":   err.Error(),
					"actor":   ug.Actor,
					"user_id": ug.Data,
				}).Error("update group attempted to promote admin with invalid UUID")
				return err
			}
			if gs.isMember(userID) {
				return nil
			} else {
				return errCannotPromoteAdminNotInGroup
			}
		} else {
			return errAdminRequired
		}
	case updateGroupTypeDemoteAdmin:
		if gs.isAdmin(ug.Actor) {
			return nil
		} else {
			return errAdminRequired
		}
	case updateGroupTypeChangeUserManagementPermission:
		if gs.isAdmin(ug.Actor) {
			return nil
		} else {
			return errAdminRequired
		}
	case updateGroupTypeChangeGroupEditsPermission:
		if gs.isAdmin(ug.Actor) {
			return nil
		} else {
			return errAdminRequired
		}
	case updateGroupTypeChangePostingPermission:
		if gs.isAdmin(ug.Actor) {
			return nil
		} else {
			return errAdminRequired
		}
	default:
		log.WithFields(log.Fields{
			"type": ug.Type,
		}).Warn("cannot apply update group with unknown type")
		return errUpdateGroupWithUnknownType
	}

	return errUpdateGroupWithUnknownType
}

func applyUpdateGroupToState(gs groupState, ug updateGroup) (groupState, error) {
	gs.ug = ug

	switch ug.Type {
	case updateGroupTypeChangeName:
		return applyUpdateGroupChangeNameToState(gs, ug)
	case updateGroupTypeAddUser:
		return applyUpdateGroupAddUserToState(gs, ug)
	case updateGroupTypeRemoveUser:
		return applyUpdateGroupRemoveUserToState(gs, ug)
	case updateGroupTypeChangeMutedUntil:
		return applyUpdateGroupChangeMutedUntilToState(gs, ug)
	case updateGroupTypeChangeRetention:
		return applyUpdateGroupChangeRetentionToState(gs, ug)
	case updateGroupTypeSetClearBefore:
		return applyUpdateGroupSetClearBeforeToState(gs, ug)
	case updateGroupTypePromoteAdmin:
		return applyUpdateGroupPromoteAdminToState(gs, ug)
	case updateGroupTypeDemoteAdmin:
		return applyUpdateGroupDemoteAdminToState(gs, ug)
	case updateGroupTypeChangeUserManagementPermission:
		return applyUpdateGroupChangeUserManagementPermissionToState(gs, ug)
	case updateGroupTypeChangeGroupEditsPermission:
		return applyUpdateGroupChangeGroupEditsPermissionToState(gs, ug)
	case updateGroupTypeChangePostingPermission:
		return applyUpdateGroupChangePostingPermissionToState(gs, ug)
	default:
		log.WithFields(log.Fields{
			"type": ug.Type,
		}).Warn("cannot apply update group with unknown type")
		return gs, errUpdateGroupWithUnknownType
	}

	return gs, nil
}

func applyUpdateGroupChangeNameToState(gs groupState, ug updateGroup) (groupState, error) {
	newName := string(ug.Data)
	if !validGroupName(newName) {
		log.WithFields(log.Fields{
			"name": newName,
		}).Error("cannot apply update group with invalid name")
		return gs, errInvalidGroupName
	}
	gs.name = newName

	return gs, nil
}

func applyUpdateGroupAddUserToState(gs groupState, ug updateGroup) (groupState, error) {
	var u user
	err := msgpack.Unmarshal(ug.Data, &u)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling user in update group")
		return gs, err
	}

	if !gs.isMember(u.ID) {
		gs.users = append(gs.users, u.ID)
	}

	return gs, nil
}

func applyUpdateGroupRemoveUserToState(gs groupState, ug updateGroup) (groupState, error) {
	userID, err := uuid.FromBytes(ug.Data)
	if err != nil {
		log.WithFields(log.Fields{
			"error":   err.Error(),
			"actor":   ug.Actor,
			"user_id": ug.Data,
		}).Error("update group attempted to remove user with invalid UUID")
		return gs, err
	}

	membersWithoutUser := []uuid.UUID{}
	for _, id := range gs.users {
		if id != userID {
			membersWithoutUser = append(membersWithoutUser, id)
		}
	}

	adminsWithoutUser := []uuid.UUID{}
	for _, id := range gs.admins {
		if id != userID {
			adminsWithoutUser = append(adminsWithoutUser, id)
		}
	}

	gs.users = membersWithoutUser
	gs.admins = adminsWithoutUser

	return gs, nil
}

func applyUpdateGroupChangeMutedUntilToState(gs groupState, ug updateGroup) (groupState, error) {
	gs.mutedUntil = int64(binary.LittleEndian.Uint64(ug.Data))

	return gs, nil
}

func applyUpdateGroupChangeRetentionToState(gs groupState, ug updateGroup) (groupState, error) {
	gs.retention = int64(binary.LittleEndian.Uint64(ug.Data))

	return gs, nil
}

func applyUpdateGroupSetClearBeforeToState(gs groupState, ug updateGroup) (groupState, error) {
	gs.clearBefore = int64(binary.LittleEndian.Uint64(ug.Data))

	return gs, nil
}

func applyUpdateGroupPromoteAdminToState(gs groupState, ug updateGroup) (groupState, error) {
	userID, err := uuid.FromBytes(ug.Data)
	if err != nil {
		log.WithFields(log.Fields{
			"error":   err.Error(),
			"actor":   ug.Actor,
			"user_id": ug.Data,
		}).Error("update group attempted to promote admin with invalid UUID")
		return gs, err
	}

	if !gs.isMember(userID) {
		return gs, errCannotPromoteAdminNotInGroup
	}

	if !gs.isAdmin(userID) {
		gs.admins = append(gs.admins, userID)
	}

	return gs, nil
}

func applyUpdateGroupDemoteAdminToState(gs groupState, ug updateGroup) (groupState, error) {
	userID, err := uuid.FromBytes(ug.Data)
	if err != nil {
		log.WithFields(log.Fields{
			"error":   err.Error(),
			"actor":   ug.Actor,
			"user_id": ug.Data,
		}).Error("update group attempted to demote admin with invalid UUID")
		return gs, err
	}

	adminsWithoutUser := []uuid.UUID{}
	for _, id := range gs.admins {
		if id != userID {
			adminsWithoutUser = append(adminsWithoutUser, id)
		}
	}
	gs.admins = adminsWithoutUser

	return gs, nil
}

func applyUpdateGroupChangeUserManagementPermissionToState(gs groupState, ug updateGroup) (groupState, error) {
	restricted, err := ug.permissionPayloadIsRestricted()
	if err != nil {
		return gs, err
	}

	gs.userManagementRestricted = restricted

	return gs, nil
}

func applyUpdateGroupChangeGroupEditsPermissionToState(gs groupState, ug updateGroup) (groupState, error) {
	restricted, err := ug.permissionPayloadIsRestricted()
	if err != nil {
		return gs, err
	}

	gs.editingRestricted = restricted

	return gs, nil
}

func applyUpdateGroupChangePostingPermissionToState(gs groupState, ug updateGroup) (groupState, error) {
	restricted, err := ug.permissionPayloadIsRestricted()
	if err != nil {
		return gs, err
	}

	gs.postingRestricted = restricted

	return gs, nil
}

func (b *bounce) setRollbacksApplicationsAndGroupState(groupID uuid.UUID, cs *canonicalStack, ugs []updateGroup) {
	err := b.database.Transaction(func(tx *gorm.DB) error {
		// Find any canonical update groups that have not been applied and make them as applied and inform them UI
		canonical := make(map[uuid.UUID]bool)
		for _, gs := range cs.history[1:] {
			canonical[gs.ug.ID] = true
			if !gs.ug.Applied {
				err := tx.Model(&gs.ug).Update("applied", true).Error
				if err != nil {
					return err
				}

				b.applyUpdateGroupInUI(gs.ug)
				err = b.handleUpdateGroupSideEffects(tx, gs.ug)
				if err != nil {
					return err
				}
				if gs.isMember(b.currentUserID()) && gs.ug.Actor != b.currentUserID() {
					b.sendConfirmation(gs.ug)
				}
			}
		}

		// Find any non-canonical update groups that have been applied and mark them as not applied roll them back in the UI
		for _, ug := range ugs {
			if _, ok := canonical[ug.ID]; !ok {
				if ug.Applied {
					err := tx.Model(&ug).Update("applied", false).Error
					if err != nil {
						return err
					}
					b.rollbackUpdateGroupInUI(ug)
				}
			}
		}

		// Set the final state in the database
		finalState, err := cs.top()
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("no final state available when updating group consensus state")
		}

		// Find the group
		var g group
		err = tx.Preload(clause.Associations).Where("id = ?", groupID).First(&g).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"group_id": groupID,
				}).Error("group not found while setting state")
				return err
			} else {
				log.WithFields(log.Fields{
					"group_id": groupID,
					"error":    err.Error(),
				}).Fatal("database error looking up group")
			}
		}

		// If the final state involves us being removed from the group, delete the group
		if !finalState.isMember(b.currentUserID()) {
			// Find the most recent update group that removed us
			removalActor := uuid.UUID{}
			for i := len(cs.history) - 1; i >= 0; i-- {
				ug := cs.history[i].ug
				if ug.Type == updateGroupTypeRemoveUser {
					targetUser, err := uuid.FromBytes(ug.Data)
					if err != nil {
						log.WithFields(log.Fields{
							"error":           err.Error(),
							"update_group_id": ug.ID,
						}).Error("update group attempts to remove user with invalid UUID")
						continue
					}
					if targetUser == b.currentUserID() {
						removalActor = ug.Actor
						break
					}
				}
			}

			// Inform the UI
			b.userInterface.RemovedFromGroup(RemovedFromGroup{
				Group: g.ID,
				Actor: removalActor,
			})

			// Delete the group
			return tx.Delete(&g).Error
		}

		// Set fields
		if g.Name != finalState.name {
			err := tx.Model(&g).Update("name", finalState.name).Error
			if err != nil {
				return err
			}
		}
		if g.Retention != finalState.retention {
			err := tx.Model(&g).Update("retention", finalState.retention).Error
			if err != nil {
				return err
			}
		}
		if g.ClearBefore != finalState.clearBefore {
			err := tx.Model(&g).Update("clear_before", finalState.clearBefore).Error
			if err != nil {
				return err
			}
		}
		if g.MutedUntil != finalState.mutedUntil {
			err := tx.Model(&g).Update("muted_until", finalState.mutedUntil).Error
			if err != nil {
				return err
			}
		}
		if g.RestrictUserManagement != finalState.userManagementRestricted {
			err := tx.Model(&g).Update("restrict_user_management", finalState.userManagementRestricted).Error
			if err != nil {
				return err
			}
		}
		if g.RestrictGroupEdits != finalState.editingRestricted {
			err := tx.Model(&g).Update("restrict_group_edits", finalState.editingRestricted).Error
			if err != nil {
				return err
			}
		}
		if g.RestrictPosting != finalState.postingRestricted {
			err := tx.Model(&g).Update("restrict_posting", finalState.postingRestricted).Error
			if err != nil {
				return err
			}
		}

		// Set group members
		for _, u := range g.Users {
			if !finalState.isMember(u.ID) {
				err = b.database.Exec("DELETE FROM group_users WHERE group_id = ? AND user_id = ?", g.ID, u.ID).Error
				if err != nil {
					return err
				}
			}
		}
		for _, userID := range finalState.users {
			if !b.userIsInGroup(userID, g.ID) {
				err = tx.Exec("INSERT INTO group_users VALUES(?, ?)", g.ID, userID).Error
				if err != nil {
					if !errors.Is(err, gorm.ErrDuplicatedKey) {
						return err
					}
				}
			}
		}

		// Set group admins
		admins := []uuid.UUID{}
		if len(g.Admins) > 0 {
			for _, adminIDString := range strings.Split(g.Admins, ",") {
				adminID, err := uuid.Parse(adminIDString)
				if err != nil {
					log.WithFields(log.Fields{
						"error":    err.Error(),
						"group_id": groupID,
						"admins":   g.Admins,
					}).Fatal("invalid UUID in group admin list")

				}
				admins = append(admins, adminID)
			}
		}
		for _, adminID := range admins {
			if !finalState.isAdmin(adminID) {
				b.removeGroupAdmin(g.ID, adminID) // TODO: not in transaction
			}
		}
		for _, adminID := range finalState.admins {
			if !b.isGroupAdmin(g.ID, adminID) {
				b.addGroupAdmin(g.ID, adminID) // TODO: not in transaction
			}
		}

		return nil
	}).Error

	if err != nil {
		log.WithFields(log.Fields{
			"group_id": groupID,
			"error":    err(),
		}).Error("error setting group state")
	}
}

func (b *bounce) applyUpdateGroupInUI(ug updateGroup) {
	switch ug.Type {
	case updateGroupTypeChangeName:
		b.informUIUpdateGroupChangeName(ug)
	case updateGroupTypeAddUser:
		b.informUIUpdateGroupAddUser(ug)
	case updateGroupTypeRemoveUser:
		b.informUIUpdateGroupRemoveUser(ug)
	case updateGroupTypeChangeMutedUntil:
		b.informUIUpdateGroupChangeMutedUntil(ug)
	case updateGroupTypeChangeRetention:
		b.informUIUpdateGroupChangeRetention(ug)
	case updateGroupTypeSetClearBefore:
		b.informUIUpdateGroupSetClearBefore(ug)
	case updateGroupTypePromoteAdmin:
		b.informUIUpdateGroupPromoteAdmin(ug)
	case updateGroupTypeDemoteAdmin:
		b.informUIUpdateGroupDemoteAdmin(ug)
	case updateGroupTypeChangeUserManagementPermission:
		b.informUIUpdateGroupChangeUserManagementPermission(ug)
	case updateGroupTypeChangeGroupEditsPermission:
		b.informUIUpdateGroupChangeGroupEditsPermission(ug)
	case updateGroupTypeChangePostingPermission:
		b.informUIUpdateGroupChangePostingPermission(ug)
	default:
		log.WithFields(log.Fields{
			"type": ug.Type,
		}).Warn("cannot inform UI of update group with unknown type")
	}
}

func (b *bounce) informUIUpdateGroupChangeName(ug updateGroup) {
	newName := string(ug.Data)
	if !validGroupName(newName) {
		log.WithFields(log.Fields{
			"name": newName,
		}).Error("cannot inform UI of update group with invalid name")
		return
	}

	b.userInterface.RenameGroup(UpdateGroupName{
		ID:        ug.ID,
		Thread:    ug.Target,
		Actor:     ug.Actor,
		Timestamp: ug.Timestamp,
		Name:      newName,
	})
}

func (b *bounce) informUIUpdateGroupAddUser(ug updateGroup) {
	// Unmarshall the new user
	var u user
	err := msgpack.Unmarshal(ug.Data, &u)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling user")
		return
	}

	b.userInterface.AddUser(
		UpdateGroupAddUser{
			ID:        ug.ID,
			Thread:    ug.Target,
			Actor:     ug.Actor,
			Timestamp: ug.Timestamp,
			User: User{
				ID:   u.ID,
				Name: u.Name,
			},
		})
}

func (b *bounce) informUIUpdateGroupRemoveUser(ug updateGroup) {
	// Parse the user ID
	userID, err := uuid.FromBytes(ug.Data)
	if err != nil {
		log.WithFields(log.Fields{
			"error":   err.Error(),
			"actor":   ug.Actor,
			"user_id": ug.Data,
		}).Error("update group attempted to remove user with invalid UUID")
		return
	}

	// Notify the UI
	b.userInterface.RemoveUser(
		UpdateGroupRemoveUser{
			ID:        ug.ID,
			Thread:    ug.Target,
			Actor:     ug.Actor,
			Timestamp: ug.Timestamp,
			User:      userID,
		})
}

func (b *bounce) informUIUpdateGroupChangeMutedUntil(ug updateGroup) {
	mutedUntil := int64(binary.LittleEndian.Uint64(ug.Data))
	b.userInterface.GroupMutedUntilChanged(ug.Target, mutedUntil)
}

func (b *bounce) informUIUpdateGroupChangeRetention(ug updateGroup) {
	b.userInterface.GroupRetentionChanged(UpdateGroupRetention{
		ID:        ug.ID,
		Thread:    ug.Target,
		Actor:     ug.Actor,
		Timestamp: ug.Timestamp,
		Retention: int64(binary.LittleEndian.Uint64(ug.Data)),
	})
}

func (b *bounce) informUIUpdateGroupSetClearBefore(ug updateGroup) {
	b.userInterface.GroupChatHistoryCleared(UpdateGroupClearHistory{
		ID:        ug.ID,
		Thread:    ug.Target,
		Actor:     ug.Actor,
		Timestamp: ug.Timestamp,
		ClearTime: int64(binary.LittleEndian.Uint64(ug.Data)),
	})
}

func (b *bounce) informUIUpdateGroupPromoteAdmin(ug updateGroup) {
	// Parse the UUID
	userID, err := uuid.FromBytes(ug.Data)
	if err != nil {
		log.WithFields(log.Fields{
			"error":   err.Error(),
			"actor":   ug.Actor,
			"user_id": ug.Data,
		}).Error("update group attempted to promote admin with invalid UUID")
		return
	}

	// Notify the UI
	b.userInterface.AdminPromoted(UpdateGroupAdminPromoted{
		ID:        ug.ID,
		Thread:    ug.Target,
		Actor:     ug.Actor,
		Timestamp: ug.Timestamp,
		UserID:    userID,
	})
}

func (b *bounce) informUIUpdateGroupDemoteAdmin(ug updateGroup) {
	// Parse the UUID
	userID, err := uuid.FromBytes(ug.Data)
	if err != nil {
		log.WithFields(log.Fields{
			"error":   err.Error(),
			"actor":   ug.Actor,
			"user_id": ug.Data,
		}).Error("update group attempted to promote admin with invalid UUID")
		return
	}

	// Notify the UI
	b.userInterface.AdminDemoted(UpdateGroupAdminDemoted{
		ID:        ug.ID,
		Thread:    ug.Target,
		Actor:     ug.Actor,
		Timestamp: ug.Timestamp,
		UserID:    userID,
	})
}

func (b *bounce) informUIUpdateGroupChangeUserManagementPermission(ug updateGroup) {
	restricted, err := ug.permissionPayloadIsRestricted()
	if err != nil {
		return
	}
	if restricted {
		b.userInterface.UserManagementRestricted(UpdateGroupUserManagementRestricted{
			ID:        ug.ID,
			Thread:    ug.Target,
			Actor:     ug.Actor,
			Timestamp: ug.Timestamp,
		})
	} else {
		b.userInterface.UserManagementUnrestricted(UpdateGroupUserManagementUnrestricted{
			ID:        ug.ID,
			Thread:    ug.Target,
			Actor:     ug.Actor,
			Timestamp: ug.Timestamp,
		})
	}
}

func (b *bounce) informUIUpdateGroupChangeGroupEditsPermission(ug updateGroup) {
	restricted, err := ug.permissionPayloadIsRestricted()
	if err != nil {
		return
	}
	if restricted {
		b.userInterface.GroupEditsRestricted(UpdateGroupEditsRestricted{
			ID:        ug.ID,
			Thread:    ug.Target,
			Actor:     ug.Actor,
			Timestamp: ug.Timestamp,
		})
	} else {
		b.userInterface.GroupEditsUnrestricted(UpdateGroupEditsUnrestricted{
			ID:        ug.ID,
			Thread:    ug.Target,
			Actor:     ug.Actor,
			Timestamp: ug.Timestamp,
		})
	}
}

func (b *bounce) informUIUpdateGroupChangePostingPermission(ug updateGroup) {
	restricted, err := ug.permissionPayloadIsRestricted()
	if err != nil {
		return
	}
	if restricted {
		b.userInterface.PostingRestricted(UpdateGroupPostingRestricted{
			ID:        ug.ID,
			Thread:    ug.Target,
			Actor:     ug.Actor,
			Timestamp: ug.Timestamp,
		})
	} else {
		b.userInterface.PostingUnrestricted(UpdateGroupPostingUnrestricted{
			ID:        ug.ID,
			Thread:    ug.Target,
			Actor:     ug.Actor,
			Timestamp: ug.Timestamp,
		})
	}
}

func (b *bounce) rollbackUpdateGroupInUI(ug updateGroup) {
	// TODO: tell the UI to delete this update
}

func (b *bounce) handleUpdateGroupSideEffects(tx *gorm.DB, ug updateGroup) error {
	switch ug.Type {
	case updateGroupTypeAddUser:
		return b.createNewUserIfNeeded(tx, ug)
	case updateGroupTypeRemoveUser:
		return b.clearDeliveryRecordsForRemovedUser(tx, ug)
	case updateGroupTypeSetClearBefore:
		return b.pruneMessagesBeforeClear(tx, ug)
	}

	return nil
}

func (b *bounce) createNewUserIfNeeded(tx *gorm.DB, ug updateGroup) error {
	// Unmarshall the new user
	var u user
	err := msgpack.Unmarshal(ug.Data, &u)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling user")
		return err
	}

	if u.ID != b.currentUserID() {
		// Ensure the user is valid
		if !b.hasValidDeviceGroup(u) {
			return errUserHasInvalidDeviceGroup
		}

		// Save the user and their devices if we don't have them
		for _, dev := range u.Devices {
			err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&dev).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error saving device that belongs to a user being added to a group")
				return err
			}
		}
		err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&u).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error saving user that is being added to a group")
			return err
		}

		// Attempt to make a connection to the user
		b.userConnectionDesired(u.ID)
	}

	return nil
}

func (b *bounce) clearDeliveryRecordsForRemovedUser(tx *gorm.DB, ug updateGroup) error {
	// Parse the user ID
	userID, err := uuid.FromBytes(ug.Data)
	if err != nil {
		log.WithFields(log.Fields{
			"error":   err.Error(),
			"actor":   ug.Actor,
			"user_id": ug.Data,
		}).Error("update group attempted to remove user with invalid UUID")
		return err
	}

	// Get the user
	var u user
	err = b.database.Preload(clause.Associations).Where("id = ?", userID).First(&u).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"group_id": ug.Target,
				"user_id":  userID,
			}).Error("user not found when attempting to remove user from group")
			return err
		} else {
			log.WithFields(log.Fields{
				"error":   err.Error(),
				"user_id": userID,
			}).Fatal("database error looking up user")
		}
	}

	// Delete all delivery records for this user for items in this group
	for _, dev := range u.Devices {
		// Delete the delivery records for each group message
		gms := []groupMessage{}
		err = tx.Where("destination = ?", ug.Target).Find(&gms).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"group_id": ug.Target,
			}).Fatal("database error looking up all group messages for a group")
		}
		for _, gm := range gms {
			err = tx.Exec("DELETE FROM delivery_records WHERE destination = ? AND frame_type = ? AND frame_id = ?", dev.Address, typeGroupMessage, gm.ID).Error
			if err != nil {
				return err
			}
		}

		// Delete the delivery records for each update group
		ugs := []updateGroup{}
		err = tx.Where("target = ?", ug.Target).Find(&ugs).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"group_id": ug.Target,
			}).Fatal("database error looking up all updates for a group")
		}
		for _, ugToDelete := range ugs {
			err = tx.Exec("DELETE FROM delivery_records WHERE destination = ? AND frame_type = ? AND frame_id = ?", dev.Address, typeUpdateGroup, ugToDelete.ID).Error
			if err != nil {
				return err
			}
		}

		// Delete the delivery records for the original group creation
		err = tx.Exec("DELETE FROM delivery_records WHERE destination = ? AND frame_type = ? AND frame_id = ?", dev.Address, typeGroupCreation, ug.Target).Error
		if err != nil {
			return err
		}
	}

	return nil
}

func (b *bounce) pruneMessagesBeforeClear(tx *gorm.DB, ug updateGroup) error {
	clearBefore := int64(binary.LittleEndian.Uint64(ug.Data))

	gms := []groupMessage{}
	err := tx.Select("id").Where("written_at <= ? AND destination = ?", clearBefore, ug.Target).Find(&gms).Error
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
		b.userInterface.DeleteMessage(gm.ID)
	}

	return nil
}
