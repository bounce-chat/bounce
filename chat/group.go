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
	Images                     string
	CreatedBy                  uuid.UUID
	CreatedAt                  int64
	Retention                  int64
	ClearBefore                int64
	MutedUntil                 int64
	Users                      []user `gorm:"many2many:group_users;"`
	Admins                     string
	Invites                    string
	InvitedBy                  uuid.UUID `msgpack:"-"`
	InvitedAt                  int64     `msgpack:"-"`
	AcceptedAt                 int64     `msgpack:"-"`
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
	if g.ID == uuid.Nil {
		log.Error("attempt to run delete hooks on empty group")
		return nil
	}
	err := tx.Clauses(clause.Returning{}).Where("id = ?", g.ID).Delete(&groupCreation{}).Error
	if err != nil {
		return err
	}
	err = tx.Clauses(clause.Returning{}).Where("destination = ?", g.ID).Delete(&groupMessage{}).Error
	if err != nil {
		return err
	}
	err = tx.Clauses(clause.Returning{}).Where("target = ? AND custom_scope = ?", g.ID, uuid.Nil).Delete(&updateGroup{}).Error
	if err != nil {
		return err
	}
	err = tx.Clauses(clause.Returning{}).Exec("DELETE FROM group_users WHERE group_id = ?", g.ID).Error
	if err != nil {
		return err
	}
	err = tx.Clauses(clause.Returning{}).Where("type = ? AND destination =?", fileTypeGroupImage, g.ID).Delete(&file{}).Error
	if err != nil {
		return err
	}
	return nil
}

func (g *group) state() groupState {
	gs := groupState{
		name:                       g.Name,
		images:                     []uuid.UUID{},
		users:                      []uuid.UUID{},
		admins:                     []uuid.UUID{},
		blockedUsers:               []uuid.UUID{},
		invites:                    []uuid.UUID{},
		invitedBy:                  g.InvitedBy,
		invitedAt:                  g.InvitedAt,
		mutedUntil:                 g.MutedUntil,
		retention:                  g.Retention,
		clearBefore:                g.ClearBefore,
		postingRestricted:          g.RestrictPosting,
		editingRestricted:          g.RestrictGroupEdits,
		userManagementRestricted:   g.RestrictUserManagement,
		readReceiptsOverridden:     g.ReadReceiptsOverridden,
		readReceiptsEnabled:        g.ReadReceiptsEnabled,
		typingIndicatorsOverridden: g.TypingIndicatorsOverridden,
		typingIndicatorsEnabled:    g.TypingIndicatorsEnabled,
	}

	for _, u := range g.Users {
		gs.users = append(gs.users, u.ID)
	}

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

			gs.admins = append(gs.admins, adminID)
		}
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

			gs.invites = append(gs.invites, invitedID)
		}
	}

	if len(g.Images) > 0 {
		for _, imageIDString := range strings.Split(g.Images, ",") {
			imageID, err := uuid.Parse(imageIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":  err.Error(),
					"images": g.Images,
				}).Fatal("invalid UUID in group images list")
			}
			gs.images = append(gs.images, imageID)
		}
	}

	return gs
}

func (b *Bounce) CreateGroup(ng NewGroup) error {
	if ng.Name == "" {
		return errors.New("cannot create group without name")
	}
	if !validGroupName(ng.Name) {
		return errInvalidGroupName
	}
	profile, ok := b.currentUser()
	if !ok {
		return errors.New("cannot create group when profile does not exist")
	}

	iconID := uuid.Nil
	imagesString := ""
	if len(ng.Image) > 0 {
		iconID = uuid.New()
		imagesString = iconID.String()
	}

	creationTime := time.Now().Unix() - 1 // Subtract one second to ensure any invites are ordered after the group creation
	g := group{
		ID:                     uuid.Nil,
		Name:                   ng.Name,
		Images:                 imagesString,
		CreatedBy:              b.currentUserID(),
		CreatedAt:              creationTime,
		Retention:              ng.Retention,
		Users:                  []user{profile},
		Admins:                 b.currentUserID().String(),
		RestrictUserManagement: ng.RestrictUserManagement,
		RestrictGroupEdits:     ng.RestrictGroupEdits,
		RestrictPosting:        ng.RestrictPosting,
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

	if len(ng.Image) > 0 {
		err := b.embedFile(iconID, ng.Image, scopeGroupWithInvites, groupID, fileTypeGroupImage, groupID)
		if err != nil {
			return errors.New("error distributing new group image: " + err.Error())
		}
	}

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

	uiGroup := Group{
		ID:                     g.ID,
		Name:                   g.Name,
		Retention:              ng.Retention,
		Users:                  []User{User{ID: profile.ID}},
		Admins:                 []uuid.UUID{b.currentUserID()},
		CreatedBy:              g.CreatedBy,
		CreatedAt:              g.CreatedAt,
		LastActivity:           g.LastActivity,
		RestrictUserManagement: ng.RestrictUserManagement,
		RestrictGroupEdits:     ng.RestrictGroupEdits,
		RestrictPosting:        ng.RestrictPosting,
	}
	if len(ng.Image) > 0 {
		uiGroup.Images = []uuid.UUID{iconID}
	}

	go b.ui.OpenNewGroupChat(uiGroup)

	for _, i := range ng.InitialInvites {
		err = b.InviteUserToGroup(g.ID, i)
		if err != nil {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"user_id":  i,
				"group_id": g.ID,
			}).Error("error inviting user to new group")
		}
	}

	return nil
}

func (b *Bounce) userIsInGroup(groupID, userID uuid.UUID) bool { // TODO: replace with current group state where possible
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

func (b *Bounce) updateLastGroupActivity(groupID uuid.UUID, timestamp int64) {
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

func (b *Bounce) isGroupAdmin(groupID, userID uuid.UUID) bool {
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

			if adminID == userID {
				return true
			}
		}
	}

	return false
}

func (b *Bounce) addGroupAdmin(groupID, userID uuid.UUID) {
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

			if adminID == userID {
				alreadyAnAdmin = true
			}

			admins = append(admins, adminIDString)
		}
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

func (b *Bounce) removeGroupAdmin(groupID, userID uuid.UUID) {
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

			if adminID == userID {
				removedUser = true
			} else {
				admins = append(admins, adminIDString)
			}
		}
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

func (b *Bounce) isBlockedFromGroup(groupID, userID uuid.UUID) bool {
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

func (b *Bounce) blockUserFromGroup(groupID, userID uuid.UUID) {
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

func (b *Bounce) isInvited(groupID, userID uuid.UUID) bool {
	var g group
	err := b.database.Select("invites").Where("id = ?", groupID).Find(&g).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"group_id": groupID,
			}).Error("group not found when checking invite status")
			return false
		} else {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"group_id": groupID,
			}).Fatal("database error looking up group invites")
		}
	}

	if len(g.Invites) > 0 {
		for _, inviteIDString := range strings.Split(g.Invites, ",") {
			inviteID, err := uuid.Parse(inviteIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":    err.Error(),
					"group_id": groupID,
					"invites":  g.Invites,
				}).Fatal("invalid UUID in group invite list")
			}

			if inviteID == userID {
				return true
			}
		}
	}

	return false
}

func (b *Bounce) addGroupInvite(groupID, userID uuid.UUID) {
	var g group
	err := b.database.Select("invites").Where("id = ?", groupID).Find(&g).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"group_id": groupID,
			}).Error("group not found when checking invite status")
			return
		} else {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"group_id": groupID,
			}).Fatal("database error looking up group invites")
		}
	}

	invites := []string{}
	alreadyInvited := false
	if len(g.Invites) > 0 {
		for _, inviteIDString := range strings.Split(g.Invites, ",") {
			inviteID, err := uuid.Parse(inviteIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":    err.Error(),
					"group_id": groupID,
					"invites":  g.Invites,
				}).Fatal("invalid UUID in group invite list")

			}

			if inviteID == userID {
				alreadyInvited = true
			}

			invites = append(invites, inviteIDString)
		}
	}

	if !alreadyInvited {
		invites = append(invites, userID.String())
	} else {
		log.WithFields(log.Fields{
			"group_id": groupID,
			"user_id":  userID,
		}).Warn("ignoring request to invite user that is already invited")
		return
	}

	err = b.database.Model(&g).Where("id = ?", groupID).Update("invites", strings.Join(invites, ",")).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"group_id": groupID,
			}).Error("group not found when adding invite")
			return
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error adding group invite")
		}
	}
}

func (b *Bounce) removeGroupInvite(groupID, userID uuid.UUID) {
	var g group
	err := b.database.Select("invites").Where("id = ?", groupID).Find(&g).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"group_id": groupID,
			}).Error("group not found when checking invite status")
			return
		} else {
			log.WithFields(log.Fields{
				"group_id": groupID,
			}).Fatal("database error looking up group invites")
		}
	}

	invites := []string{}
	removedUser := false
	if len(g.Invites) > 0 {
		for _, inviteIDString := range strings.Split(g.Invites, ",") {
			inviteID, err := uuid.Parse(inviteIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":    err.Error(),
					"group_id": groupID,
					"invites":  g.Invites,
				}).Fatal("invalid UUID in group invite list")
			}

			if inviteID == userID {
				removedUser = true
			} else {
				invites = append(invites, inviteIDString)
			}
		}
	}

	if removedUser {
		err = b.database.Model(&g).Where("id = ?", groupID).Update("invites", strings.Join(invites, ",")).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"group_id": groupID,
				}).Error("group not found when removing invite")
				return
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error removing group invite")
			}
		}
	} else {
		log.WithFields(log.Fields{
			"group_id": groupID,
			"user_id":  userID,
		}).Warn("ignoring request to remove invite from group that already wasn't there")
	}
}

func (b *Bounce) updateConsensusForGroupsWithUser(userID uuid.UUID) {
	var groups []uuid.UUID
	err := b.database.Table("group_users").
		Select("group_id").
		Where("user_id = ?", userID).
		Find(&groups).
		Error
	if err != nil {
		log.WithFields(log.Fields{
			"error":   err.Error(),
			"user_id": userID,
		}).Error("error getting groups to update that contain user")
	}
	for _, groupID := range groups {
		b.reloadGroupConsensus(groupID)
		b.writeGroupConsensus(groupID)
	}
}
