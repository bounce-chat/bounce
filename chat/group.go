package chat

import (
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"github.com/zeebo/blake3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var groupMutex sync.Mutex

type group struct {
	ID                         uuid.UUID `gorm:"type:uuid;primary_key;"`
	Name                       string
	Image                      []byte
	CreatedBy                  uuid.UUID
	CreatedAt                  int64
	Retention                  int64
	ClearBefore                int64
	MutedUntil                 int64
	Users                      []user `gorm:"many2many:group_users;"`
	Admins                     string `gorm:"not null"`
	BlockedUsers               string
	RestrictUserManagement     bool
	RestrictGroupEdits         bool
	RestrictPosting            bool
	LastActivity               int64
	ReadReceiptsOverridden     bool      `msgpack:"-"`
	ReadReceiptsEnabled        bool      `msgpack:"-"`
	TypingIndicatorsOverridden bool      `msgpack:"-"`
	TypingIndicatorsEnabled    bool      `msgpack:"-"`
	DeliveryRecordsClearedFor  uuid.UUID `msgpack:"-"`
	payload                    []byte
	payloadMutex               sync.Mutex
}

func (g *group) BeforeCreate(tx *gorm.DB) error {
	if g.ID == uuid.Nil {
		log.Fatal("attempt to create group with nil ID, group ID must be set before creation")
	}
	g.LastActivity = time.Now().Unix()

	return nil
}

func (g *group) AfterDelete(tx *gorm.DB) error {
	err := tx.Where("id = ?", g.ID).Delete(&groupCreation{}).Error
	if err != nil {
		return err
	}
	err = tx.Where("destination = ?", g.ID).Delete(&groupMessage{}).Error
	if err != nil {
		return err
	}
	err = tx.Where("target = ? AND custom_scope = ?", g.ID, uuid.Nil).Delete(&updateGroup{}).Error
	if err != nil {
		return err
	}
	err = tx.Exec("DELETE FROM group_users WHERE group_id = ?", g.ID).Error
	if err != nil {
		return err
	}
	return nil
}

func (g *group) state() groupState {
	gs := groupState{
		name:                     g.Name,
		mutedUntil:               g.MutedUntil,
		retention:                g.Retention,
		clearBefore:              g.ClearBefore,
		postingRestricted:        g.RestrictPosting,
		editingRestricted:        g.RestrictGroupEdits,
		userManagementRestricted: g.RestrictUserManagement,
	}

	for _, u := range g.Users {
		gs.users = append(gs.users, u.ID)
	}

	for _, adminIDString := range strings.Split(g.Admins, ",") {
		adminID, err := uuid.Parse(adminIDString)
		if err != nil {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"group_id": g.ID,
				"admins":   g.Admins,
			}).Fatal("invalid UUID in group admin list")
		}

		gs.admins = append(gs.admins, adminID)
	}

	if len(g.BlockedUsers) > 0 {
		for _, blockedIDString := range strings.Split(g.BlockedUsers, ",") {
			blockedID, err := uuid.Parse(blockedIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":         err.Error(),
					"group_id":      g.ID,
					"blocked_users": g.BlockedUsers,
				}).Fatal("invalid UUID in group blocked list")
			}

			gs.blockedUsers = append(gs.blockedUsers, blockedID)
		}
	}

	return gs
}

func (b *bounce) createGroup(proposedGroup Group) error {
	if proposedGroup.ID != uuid.Nil {
		return errors.New("group UUID cannot be set from the UI")
	}

	if proposedGroup.Name == "" {
		return errors.New("cannot create group without name")
	}
	if !validGroupName(proposedGroup.Name) {
		return errInvalidGroupName
	}

	if len(proposedGroup.Users) == 0 {
		return errors.New("cannot create a group without any users")
	}

	users := []user{}
	userMap := map[uuid.UUID]bool{}
	uiUsers := []User{}
	userListContainsProfile := false
	for _, proposedUser := range proposedGroup.Users {
		var u user
		err := b.database.Preload("Devices.Signature").Preload(clause.Associations).Where("id = ?", proposedUser.ID).First(&u).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"user_id": proposedUser.ID,
				}).Error("attempt to create a group with user ID that doesn't exist in the database")
				return errors.New("cannot create group with unknown user")
			} else {
				log.WithFields(log.Fields{
					"user_id": proposedUser.ID,
					"error":   err.Error(),
				}).Fatal("error looking up user")
			}
		}
		users = append(users, u)
		if _, present := userMap[u.ID]; present {
			return errors.New("cannot create group with duplicate users")
		}
		userMap[u.ID] = true
		uiUsers = append(uiUsers, User{ID: u.ID, Name: u.Name})
		if u.ID == b.currentUserID() {
			userListContainsProfile = true
		}
	}
	if !userListContainsProfile {
		return errors.New("cannot create a new group without being a member")
	}

	if len(proposedGroup.Admins) == 0 {
		return errors.New("cannot create a group with no admins")
	}

	adminStrings := []string{}
	adminMap := map[uuid.UUID]bool{}
	for _, adminID := range proposedGroup.Admins {
		if _, present := userMap[adminID]; !present {
			return errors.New("cannot create a group with an admin that is not also a member")
		}
		adminStrings = append(adminStrings, adminID.String())
		if _, present := adminMap[adminID]; present {
			return errors.New("cannot create group with duplicate admins")
		}
		adminMap[adminID] = true
	}

	if proposedGroup.LastActivity != 0 {
		return errors.New("last activity for group cannot be set by the UI during group creation")
	}

	if proposedGroup.MutedUntil != 0 {
		return errors.New("notification muting for group cannot be set by the UI during group creation")
	}

	creationTime := time.Now().Unix()
	g := group{
		ID:                     uuid.Nil,
		Name:                   proposedGroup.Name,
		CreatedBy:              b.currentUserID(),
		CreatedAt:              creationTime,
		Retention:              proposedGroup.Retention,
		Users:                  users,
		Admins:                 strings.Join(adminStrings, ","),
		RestrictUserManagement: proposedGroup.RestrictUserManagement,
		RestrictGroupEdits:     proposedGroup.RestrictGroupEdits,
		RestrictPosting:        proposedGroup.RestrictPosting,
		LastActivity:           time.Now().Unix(),
	}

	groupData, err := msgpack.Marshal(g)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal group")
	}

	// Create a hash of the group without an ID to use as the group creation ID,
	// and the group ID upon handling, to ensure that the original group state
	// is not modified during replication
	hasher := blake3.New()
	written, _ := hasher.Write(groupData)
	if written != len(groupData) {
		log.WithFields(log.Fields{
			"length":  len(groupData),
			"written": written,
		}).Fatal("failed to write all group data into hasher")
	}
	digest := hasher.Digest()
	groupHash := make([]byte, 16)
	digest.Read(groupHash)
	groupID, err := uuid.FromBytes(groupHash)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot create UUID from hash of group data")
	}
	g.ID = groupID

	gc := groupCreation{
		ID:        groupID,
		Timestamp: creationTime,
		Data:      groupData,
	}
	gc.OriginalPayload, err = msgpack.Marshal(gc)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling group creation")
	}
	sc := b.createSignedContainer(gc.OriginalPayload)
	gc.Signature = sc.Signature
	gc.Signer = sc.Signer

	err = b.database.Transaction(func(tx *gorm.DB) error {
		err = tx.Omit("Users.*").Create(&g).Error
		if err != nil {
			return err
		}
		err = tx.Create(&gc).Error
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving new group and group creation")
	}

	b.reloadGroupConsensus(g.ID)

	b.broadcast(&gc)

	b.userInterface.OpenNewGroupChat(Group{
		ID:                     g.ID,
		Name:                   g.Name,
		Retention:              proposedGroup.Retention,
		Users:                  uiUsers,
		Admins:                 proposedGroup.Admins,
		CreatedBy:              g.CreatedBy,
		CreatedAt:              g.CreatedAt,
		RestrictUserManagement: proposedGroup.RestrictUserManagement,
		RestrictGroupEdits:     proposedGroup.RestrictGroupEdits,
		RestrictPosting:        proposedGroup.RestrictPosting,
	})

	return nil
}

func (b *bounce) getGroupRetention(groupID uuid.UUID) int64 {
	var g group
	err := b.database.Select("retention").First(&g, "id = ?", groupID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"id":    groupID,
				"error": err.Error(),
			}).Error("error selecting message retention for unknown group")
			return 0
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error selecting message retention from group")
		}
	}
	return g.Retention
}

func (b *bounce) userIsInGroup(groupID, userID uuid.UUID) bool {
	var exists bool
	err := b.database.Table("group_users").
		Select("count(*) = 1").
		Where("user_id = ? AND group_id = ?", userID, groupID).
		Find(&exists).
		Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error checking if user is in group")
	}
	return exists
}

func validGroupName(name string) bool {
	if name == "" {
		return false
	}

	if name != strings.TrimSpace(name) {
		return false
	}

	if strings.Contains(name, "\n") {
		return false
	}

	return utf8.ValidString(name) && utf8.RuneCountInString(name) <= MaximumNameLength
}

func (b *bounce) groupMessageWrittenBeforeHistoryCleared(groupID uuid.UUID, messageWrittenAt int64) bool {
	var g group
	err := b.database.Select("clear_before").First(&g, "id = ?", groupID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"id":    groupID,
				"error": err.Error(),
			}).Error("error selecting clear before for unknown group")
			return false
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error selecting clear before from group")
		}
	}

	return messageWrittenAt < g.ClearBefore
}

func (b *bounce) updateLastGroupActivity(groupID uuid.UUID, timestamp int64) {
	var g group
	err := b.database.Where("id = ?", groupID).First(&g).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"group_id": groupID,
			}).Error("error finding group for last activity update")
			return
		} else {
			log.WithFields(log.Fields{
				"group_id": groupID,
			}).Fatal("database error finding group for last activity update")
		}
	}

	if timestamp > g.LastActivity {
		err = b.database.Model(&g).Update("last_activity", timestamp).Error
		if err != nil {
			log.WithFields(log.Fields{
				"group_id": groupID,
			}).Fatal("database error updating group last activity")
		}
	}
}

func (b *bounce) isGroupAdmin(groupID, userID uuid.UUID) bool {
	var g group
	err := b.database.Select("admins").Where("id = ?", groupID).Find(&g).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"group_id": groupID,
			}).Error("group not found when checking admin membership")
			return false
		} else {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"group_id": groupID,
			}).Fatal("database error looking up group admin membership")
		}
	}

	for _, adminIDString := range strings.Split(g.Admins, ",") {
		adminID, err := uuid.Parse(adminIDString)
		if err != nil {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"group_id": groupID,
				"admins":   g.Admins,
			}).Fatal("invalid UUID in group admin list")
		}

		if adminID == userID {
			return true
		}
	}

	return false
}

func (b *bounce) addGroupAdmin(groupID, userID uuid.UUID) {
	var g group
	err := b.database.Select("admins").Where("id = ?", groupID).Find(&g).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"group_id": groupID,
			}).Error("group not found when checking admin membership")
			return
		} else {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"group_id": groupID,
			}).Fatal("database error looking up group admin membership")
		}
	}

	admins := []string{}
	alreadyAnAdmin := false
	for _, adminIDString := range strings.Split(g.Admins, ",") {
		adminID, err := uuid.Parse(adminIDString)
		if err != nil {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"group_id": groupID,
				"admins":   g.Admins,
			}).Fatal("invalid UUID in group admin list")

		}

		if adminID == userID {
			alreadyAnAdmin = true
		}

		admins = append(admins, adminIDString)
	}

	if !alreadyAnAdmin {
		admins = append(admins, userID.String())
	} else {
		log.WithFields(log.Fields{
			"group_id": groupID,
			"user_id":  userID,
		}).Warn("ignoring request to promote user to group admin where user is already an admin")
		return
	}

	err = b.database.Model(&g).Where("id = ?", groupID).Update("admins", strings.Join(admins, ",")).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"group_id": groupID,
			}).Error("group not found when adding admin")
			return
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error adding group admin")
		}
	}
}

func (b *bounce) removeGroupAdmin(groupID, userID uuid.UUID) {
	var g group
	err := b.database.Select("admins").Where("id = ?", groupID).Find(&g).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"group_id": groupID,
			}).Error("group not found when checking admin membership")
			return
		} else {
			log.WithFields(log.Fields{
				"group_id": groupID,
			}).Fatal("database error looking up group admin membership")
		}
	}

	admins := []string{}
	removedUser := false
	for _, adminIDString := range strings.Split(g.Admins, ",") {
		adminID, err := uuid.Parse(adminIDString)
		if err != nil {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"group_id": groupID,
				"admins":   g.Admins,
			}).Fatal("invalid UUID in group admin list")
		}

		if adminID == userID {
			removedUser = true
		} else {
			admins = append(admins, adminIDString)
		}
	}

	if len(admins) == 0 {
		log.WithFields(log.Fields{
			"group_id": groupID,
			"user_id":  userID,
		}).Error("cannot remove last admin from group")
		return
	}

	if removedUser {
		err = b.database.Model(&g).Where("id = ?", groupID).Update("admins", strings.Join(admins, ",")).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"group_id": groupID,
				}).Error("group not found when removing admin")
				return
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error removing group admin")
			}
		}
	} else {
		log.WithFields(log.Fields{
			"group_id": groupID,
			"user_id":  userID,
		}).Warn("ignoring request to remove admin from group where user is already not an admin")
	}
}

func (b *bounce) isBlockedFromGroup(groupID, userID uuid.UUID) bool {
	var g group
	err := b.database.Select("blocked_users").Where("id = ?", groupID).Find(&g).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"group_id": groupID,
			}).Error("group not found when checking blocked users")
			return false
		} else {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"group_id": groupID,
			}).Fatal("database error looking up blocked users")
		}
	}

	if len(g.BlockedUsers) > 0 {
		for _, blockedIDString := range strings.Split(g.BlockedUsers, ",") {
			blockedID, err := uuid.Parse(blockedIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":         err.Error(),
					"group_id":      groupID,
					"blocked_users": g.BlockedUsers,
				}).Fatal("invalid UUID in group blocked users list")
			}

			if blockedID == userID {
				return true
			}
		}
	}

	return false
}

func (b *bounce) blockUserFromGroup(groupID, userID uuid.UUID) {
	var g group
	err := b.database.Select("blocked_users").Where("id = ?", groupID).Find(&g).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"group_id": groupID,
			}).Error("group not found when getting blocked users")
			return
		} else {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"group_id": groupID,
			}).Fatal("database error looking up group blocked users")
		}
	}

	blocked := []string{}
	alreadyBlocked := false
	if len(g.BlockedUsers) > 0 {
		for _, blockedIDString := range strings.Split(g.BlockedUsers, ",") {
			blockedID, err := uuid.Parse(blockedIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":         err.Error(),
					"group_id":      groupID,
					"blocked_users": g.BlockedUsers,
				}).Error("invalid UUID in group blocked users list")
				continue
			}

			if blockedID == userID {
				alreadyBlocked = true
			}

			blocked = append(blocked, blockedIDString)
		}
	}

	if !alreadyBlocked {
		blocked = append(blocked, userID.String())
	} else {
		log.WithFields(log.Fields{
			"group_id": groupID,
			"user_id":  userID,
		}).Warn("ignoring request to block user that is already blocked")
		return
	}

	err = b.database.Model(&g).Where("id = ?", groupID).Update("blocked_users", strings.Join(blocked, ",")).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"group_id": groupID,
			}).Error("group not found when blocking user")
			return
		} else {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"group_id": groupID,
			}).Fatal("database error adding blocked user")
		}
	}
}
