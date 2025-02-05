package chat

import (
	"encoding/binary"
	"errors"
	"strings"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
)

type groupState struct {
	name                       string
	users                      []uuid.UUID
	admins                     []uuid.UUID
	blockedUsers               []uuid.UUID
	mutedUntil                 int64
	retention                  int64
	clearBefore                int64
	postingRestricted          bool
	editingRestricted          bool
	userManagementRestricted   bool
	readReceiptsOverridden     bool
	readReceiptsEnabled        bool
	typingIndicatorsOverridden bool
	typingIndicatorsEnabled    bool
	deletedBy                  uuid.UUID
	ug                         updateGroup
}

func (b *bounce) createInitialGroupState(groupID uuid.UUID) (groupState, error) {
	// Create the group state
	gs := groupState{
		users:                      []uuid.UUID{},
		admins:                     []uuid.UUID{},
		blockedUsers:               []uuid.UUID{},
		postingRestricted:          true,
		editingRestricted:          true,
		userManagementRestricted:   true,
		deletedBy:                  uuid.Nil,
		readReceiptsOverridden:     false,
		readReceiptsEnabled:        true,
		typingIndicatorsOverridden: false,
		typingIndicatorsEnabled:    true,
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

	if gs.readReceiptsOverridden != otherState.readReceiptsOverridden {
		return false
	}

	if gs.readReceiptsEnabled != otherState.readReceiptsEnabled {
		return false
	}

	if gs.typingIndicatorsOverridden != otherState.typingIndicatorsOverridden {
		return false
	}

	if gs.typingIndicatorsEnabled != otherState.typingIndicatorsEnabled {
		return false
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
	case updateGroupTypeSetReadReceiptSettings:
		if myID == ug.Actor {
			return nil
		} else {
			return errReadReceiptOnlyMutableBySelf
		}
	case updateGroupTypeSetTypingIndicatorSettings:
		if myID == ug.Actor {
			return nil
		} else {
			return errTypingIndicatorOnlyMutableBySelf
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
	case updateGroupTypeDelete:
		return applyUpdateGroupDeleteToState(gs, ug)
	case updateGroupTypeBlock:
		return applyUpdateGroupBlockToState(gs, ug)
	case updateGroupTypeSetReadReceiptSettings:
		return applyUpdateGroupSetReadReceiptsToState(gs, ug)
	case updateGroupTypeSetTypingIndicatorSettings:
		return applyUpdateGroupSetTypingIndicatorsToState(gs, ug)
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

func applyUpdateGroupSetReadReceiptsToState(gs groupState, ug updateGroup) (groupState, error) {
	if len(ug.Data) != 2 {
		return gs, errInvalidPayloadLength
	}

	gs.readReceiptsOverridden = ug.Data[0] == readReceiptsOverriddenValue
	gs.readReceiptsEnabled = ug.Data[1] == readReceiptsEnabledValue

	return gs, nil
}

func applyUpdateGroupSetTypingIndicatorsToState(gs groupState, ug updateGroup) (groupState, error) {
	if len(ug.Data) != 2 {
		return gs, errInvalidPayloadLength
	}

	gs.typingIndicatorsOverridden = ug.Data[0] == typingIndicatorsOverriddenValue
	gs.typingIndicatorsEnabled = ug.Data[1] == typingIndicatorsEnabledValue

	return gs, nil
}
