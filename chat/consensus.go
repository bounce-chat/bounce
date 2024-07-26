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
	blockedUsers             []uuid.UUID
	mutedUntil               int64
	retention                int64
	clearBefore              int64
	postingRestricted        bool
	editingRestricted        bool
	userManagementRestricted bool
	deletedBy                uuid.UUID
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

	if gs.deletedBy != otherState.deletedBy {
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

	if len(gs.blockedUsers) != len(otherState.blockedUsers) {
		return false
	}

	for _, userID := range gs.blockedUsers {
		if !otherState.isBlocked(userID) {
			return false
		}
	}

	for _, userID := range otherState.blockedUsers {
		if !gs.isBlocked(userID) {
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

func (gs groupState) isBlocked(userID uuid.UUID) bool {
	for _, memberID := range gs.blockedUsers {
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
	// Make sure the payload of this update is valid for its type
	if !ug.validPayloadFormat() {
		log.WithFields(log.Fields{
			"id": ug.ID,
		}).Error("ignoring update group with invalid data")
	}

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
			}).Error("out of order update group inserted into canonical history")
			return
		}
	}

	// Stop allowing any new changes once a group has been blocked
	if lastState.isBlocked(cs.myID) {
		return
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
	err = b.database.Preload(clause.Associations).Where("target = ?", groupID).Order("timestamp asc").Find(&ugs).Error
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

	//
	// Try to look up the group.  If we can't find the group, check if we have any update groups anyway.  These
	// update groups might exist because they apply to a group that was deleted, blocked, or that we left before
	// this device ever learned about the groups existance.  In that case, we want to apply the block, and keep
	// these updates around for any other sync devices that need them.
	//
	var g group
	err := b.database.Preload(clause.Associations).Where("id = ?", groupID).First(&g).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			b.applyUpdateGroupsForNonexistentGroup(groupID)
			return
		} else {
			log.WithFields(log.Fields{
				"group_id": groupID,
				"error":    err.Error(),
			}).Fatal("database error looking up group")
		}
	}

	cs, ugs := b.buildCanonicalHistoryStack(groupID)

	// Track what has been applied and rolled back, inform the UI, and set the group state in the database
	err = b.setRollbacksApplicationsAndGroupState(g, cs, ugs)
	if err != nil {
		log.WithFields(log.Fields{
			"group_id": groupID,
			"error":    err.Error(),
		}).Error("error setting group state")
	}
}

func (b *bounce) applyUpdateGroupsForNonexistentGroup(groupID uuid.UUID) {
	// Find all update groups for this group
	var ugs []updateGroup
	err := b.database.Preload(clause.Associations).Where("target = ?", groupID).Order("timestamp asc").Find(&ugs).Error
	if err != nil {
		log.WithFields(log.Fields{
			"group_id": groupID,
			"error":    err.Error(),
		}).Fatal("database error selecting all update groups")
	}

	for _, ug := range ugs {
		// Even if we have never seen a group before, if we blocked it on another device, add it to the block list on this device
		if ug.Type == updateGroupTypeBlock && ug.Actor == b.currentUserID() {
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
	}
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
		blockedUsers:             []uuid.UUID{},
		postingRestricted:        true,
		editingRestricted:        true,
		userManagementRestricted: true,
		deletedBy:                uuid.Nil,
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

	// Return the state
	return gs, nil
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
		userID, err := uuid.FromBytes(ug.Data)
		if err != nil {
			log.WithFields(log.Fields{
				"error":   err.Error(),
				"actor":   ug.Actor,
				"user_id": ug.Data,
			}).Error("update group attempted to remove user with invalid UUID")
			return err
		}
		if len(gs.admins) == 1 && gs.admins[0] == userID {
			return errCannotRemoveLastAdmin
		}
		if ug.Actor == userID {
			return nil
		}
		if gs.deletedBy == userID {
			return errCannotDemoteAdminWhoDeletedGroup
		}
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
		userID, err := uuid.FromBytes(ug.Data)
		if err != nil {
			log.WithFields(log.Fields{
				"error":   err.Error(),
				"actor":   ug.Actor,
				"user_id": ug.Data,
			}).Error("update group attempted to demote admin with invalid UUID")
			return err
		}
		if gs.deletedBy == userID {
			return errCannotDemoteAdminWhoDeletedGroup
		}
		if gs.isAdmin(ug.Actor) {
			if len(gs.admins) == 1 && gs.admins[0] == userID {
				return errCannotRemoveLastAdmin
			}
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
	case updateGroupTypeDelete:
		if gs.deletedBy != uuid.Nil {
			return errAlreadyDeleted
		}
		if gs.isAdmin(ug.Actor) {
			return nil
		} else {
			return errAdminRequired
		}
	case updateGroupTypeBlock:
		if gs.isAdmin(ug.Actor) {
			if len(gs.admins) == 1 && gs.admins[0] == ug.Actor {
				return errCannotRemoveLastAdmin
			}
		}
		return nil
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
	case updateGroupTypeDelete:
		return applyUpdateGroupDeleteToState(gs, ug)
	case updateGroupTypeBlock:
		return applyUpdateGroupBlockToState(gs, ug)
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

	if len(adminsWithoutUser) == 0 {
		return gs, errCannotRemoveLastAdmin
	}

	gs.users = membersWithoutUser
	gs.admins = adminsWithoutUser

	if gs.deletedBy == userID {
		gs.deletedBy = uuid.Nil
	}

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

	if len(adminsWithoutUser) == 0 {
		return gs, errCannotRemoveLastAdmin
	}

	gs.admins = adminsWithoutUser

	if gs.deletedBy == userID {
		gs.deletedBy = uuid.Nil
	}

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

func applyUpdateGroupDeleteToState(gs groupState, ug updateGroup) (groupState, error) {
	gs.deletedBy = ug.Actor

	return gs, nil
}

func applyUpdateGroupBlockToState(gs groupState, ug updateGroup) (groupState, error) {
	if !gs.isBlocked(ug.Actor) {
		gs.blockedUsers = append(gs.blockedUsers, ug.Actor)
	}

	membersWithoutUser := []uuid.UUID{}
	for _, id := range gs.users {
		if id != ug.Actor {
			membersWithoutUser = append(membersWithoutUser, id)
		}
	}

	adminsWithoutUser := []uuid.UUID{}
	for _, id := range gs.admins {
		if id != ug.Actor {
			adminsWithoutUser = append(adminsWithoutUser, id)
		}
	}

	if len(adminsWithoutUser) == 0 {
		return gs, errCannotRemoveLastAdmin
	}

	gs.users = membersWithoutUser
	gs.admins = adminsWithoutUser

	return gs, nil
}

func (b *bounce) setRollbacksApplicationsAndGroupState(g group, cs *canonicalStack, ugs []updateGroup) error {
	if len(cs.history) == 0 {
		log.WithFields(log.Fields{
			"group_id": g.ID,
		}).Error("cannot update group state with empty history stack")
		return errStackEmpty
	}

	finalState, err := cs.top()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("no final state available when updating group consensus state")
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

			if finalState.deletedBy == uuid.Nil && finalState.isMember(b.currentUserID()) {
				b.applyUpdateGroupInUI(gs.ug)
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
					if gs.isMember(b.currentUserID()) {
						if !(gs.ug.Type == updateGroupTypeBlock) {
							b.sendConfirmation(gs.ug)
						}
					}
				}
			}
		}
		for _, member := range gs.users {
			everInGroup[member] = true
		}
	}

	// If the final state is that the group is deleted, delete the group
	if finalState.deletedBy != uuid.Nil {
		// Find the update group that deleted this group, or use gs.deletedWithi
		deleteUgID := uuid.Nil
		for i := len(cs.history) - 1; i >= 0; i-- {
			ug := cs.history[i].ug
			if ug.Type == updateGroupTypeDelete {
				deleteUgID = ug.ID
				break
			}
		}
		if deleteUgID == uuid.Nil {
			log.WithFields(log.Fields{
				"deleted_by": finalState.deletedBy,
			}).Error("error locating deletion update group in canonical stack")
			return errors.New("unable to find delete update in canonical history")
		}

		// Attach a custom scope to this update group
		err = b.createCustomScopeFromGroup(g.ID)
		if err == nil {
			err = b.database.Model(&updateGroup{}).Where("id = ?", deleteUgID).Select("custom_scope").Update("custom_scope", g.ID).Error
			if err != nil {
				log.WithFields(log.Fields{
					"update_group_id": deleteUgID,
					"error":           err.Error(),
				}).Fatal("error updating custom scope on update group")
			}
		} else {
			log.WithFields(log.Fields{
				"update_group_id": deleteUgID,
				"error":           err.Error(),
			}).Error("error creating custom scope for update group")
		}

		// Determing if the actor who deleted this group was ever not an admin and collect any updates about their admin status
		alwaysAnAdmin := true
		ugsWithAdminStatusSideEffects := []updateGroup{}
		for _, gs := range cs.history {
			if !gs.isAdmin(finalState.deletedBy) {
				alwaysAnAdmin = false
			}

			if gs.ug.Type == updateGroupTypeAddUser || gs.ug.Type == updateGroupTypeRemoveUser || gs.ug.Type == updateGroupTypePromoteAdmin || gs.ug.Type == updateGroupTypeDemoteAdmin {
				ugsWithAdminStatusSideEffects = append(ugsWithAdminStatusSideEffects, gs.ug)
			}
		}

		// If this actor was ever not an admin, we need to preserve the history
		if !alwaysAnAdmin {
			// Find the custom scope we just cleared
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

	// If we blocked this group, save that on user, custom scope our block update, and delete the group
	if finalState.isBlocked(b.currentUserID()) {
		// Add this group to our list of blocked groups
		b.addBlockedGroup(g.ID)

		// Find our block update and custom scope it
		for i := len(cs.history) - 1; i >= 0; i-- {
			ug := cs.history[i].ug
			if ug.Type == updateGroupTypeBlock && ug.Actor == b.currentUserID() {
				err = b.createCustomScopeFromGroup(ug.Target)
				if err == nil {
					err = b.database.Model(&ug).Select("custom_scope").Update("custom_scope", ug.Target).Error
					if err != nil {
						log.WithFields(log.Fields{
							"update_group_id": ug.ID,
							"error":           err.Error(),
						}).Fatal("error updating custom scope on update group")
					}
				} else {
					log.WithFields(log.Fields{
						"update_group_id": ug.ID,
						"error":           err.Error(),
					}).Error("error creating custom scope for update group")
				}

				break
			}
		}

		// Inform the UI
		b.userInterface.GroupDeleted(GroupDeleted{
			Group: g.ID,
			Actor: b.currentUserID(),
		})

		// Delete the group
		return b.database.Delete(&g).Error
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
					// Set the actor who removed us from the group
					removalActor = ug.Actor

					// Attach a custom scope to this update group
					err = b.createCustomScopeFromGroup(ug.Target)
					if err == nil {
						err = b.database.Model(&ug).Select("custom_scope").Update("custom_scope", ug.Target).Error
						if err != nil {
							log.WithFields(log.Fields{
								"update_group_id": ug.ID,
								"error":           err.Error(),
							}).Fatal("error updating custom scope on update group")
						}
					} else {
						log.WithFields(log.Fields{
							"update_group_id": ug.ID,
							"error":           err.Error(),
						}).Error("error creating custom scope for update group")
					}

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

		if !b.userIsInGroup(g.ID, userID) {
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
		if !b.isGroupAdmin(g.ID, adminID) {
			b.addGroupAdmin(g.ID, adminID)
		}
	}

	// Set blocked users
	for _, blockedID := range gs.blockedUsers {
		if !b.isBlockedFromGroup(g.ID, blockedID) {
			b.blockUserFromGroup(g.ID, blockedID)
		}
	}

	b.userInterface.SetGroupState(Group{
		ID:   g.ID,
		Name: g.Name,
		//Image: []byte{},
		Users:                  finalUsers,
		Admins:                 gs.admins,
		BlockedUsers:           gs.blockedUsers,
		Retention:              g.Retention,
		MutedUntil:             g.MutedUntil,
		LastActivity:           g.LastActivity,
		RestrictUserManagement: g.RestrictUserManagement,
		RestrictGroupEdits:     g.RestrictGroupEdits,
		RestrictPosting:        g.RestrictPosting,
	})

	return nil
}

func (b *bounce) applyUpdateGroupInUI(ug updateGroup) {
	switch ug.Type {
	case updateGroupTypeChangeName:
		b.informUIUpdateGroupChangeName(ug)
	case updateGroupTypeAddUser:
		b.informUIUpdateGroupAddUser(ug)
	case updateGroupTypeRemoveUser:
		b.informUIUpdateGroupRemoveUser(ug)
	case updateGroupTypeChangeRetention:
		b.informUIUpdateGroupChangeRetention(ug)
	case updateGroupTypeChangeMutedUntil:
		return
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
	case updateGroupTypeDelete:
		return
	case updateGroupTypeBlock:
		b.informUIUpdateGroupBlock(ug)
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

	// Notify the UI
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

func (b *bounce) informUIUpdateGroupBlock(ug updateGroup) {
	b.userInterface.UserBlockedGroup(UserBlockedGroup{
		ID:        ug.ID,
		Thread:    ug.Target,
		Actor:     ug.Actor,
		Timestamp: ug.Timestamp,
	})
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
	var userIDs []uuid.UUID
	err := b.database.Table("group_users").
		Select("user_id").
		Where("group_id = ?", groupID).
		Find(&userIDs).
		Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error getting user IDs in group")
	}

	// Get all the addresses for all these users devices, excluding this device
	var addresses []string
	err = b.database.Table("devices").
		Select("address").
		Where("address != ? AND user_id IN (?)", b.network.Address(), userIDs).
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
		if rd.connectedSockets > 1 {
			go b.sendReferences(addr)
		}
	}
}
