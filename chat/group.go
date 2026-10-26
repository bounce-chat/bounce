package chat

import (
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Basekick-Labs/msgpack/v6"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

var groupMutex sync.Mutex

var errInvalidGroupImages = errors.New("group creation image is not an ID")
var errInvalidGroupInvites = errors.New("group creation has invites")
var errInvalidGroupBlockedUsers = errors.New("group creation has blocked users")

type group struct {
	ID                         ID `gorm:"primary_key;"`
	Name                       string
	Images                     string
	CreatedBy                  ID
	CreatedAt                  int64
	Retention                  int64
	ClearBefore                int64
	MutedUntil                 int64
	Users                      []user `gorm:"many2many:group_users;"`
	Admins                     string
	Invites                    string
	InvitedBy                  ID    `msgpack:"-"`
	InvitedAt                  int64 `msgpack:"-"`
	AcceptedAt                 int64 `msgpack:"-"`
	BlockedUsers               string
	RestrictUserManagement     bool
	RestrictGroupEdits         bool
	RestrictPosting            bool
	LastActivity               int64
	ReadReceiptsOverridden     bool  `msgpack:"-"`
	ReadReceiptsEnabled        bool  `msgpack:"-"`
	TypingIndicatorsOverridden bool  `msgpack:"-" gorm:"index:idx_group_typing_indicators"`
	TypingIndicatorsEnabled    bool  `msgpack:"-" gorm:"index:idx_group_typing_indicators"`
	DeliveryRecordsClearedFor  ID    `msgpack:"-"`
	LastOpened                 int64 `msgpack:"-"`
}

func (g *group) BeforeCreate(tx *gorm.DB) error {
	if g.ID == NilID {
		log.Fatal("attempt to create group with nil ID, group ID must be set before creation")
	}

	if err := validGroupCreationRecord(*g); err != nil {
		return err
	}

	g.LastActivity = time.Now().Unix()

	return nil
}

func validGroupCreationRecord(g group) error {
	if !validGroupName(g.Name) {
		return errInvalidGroupName
	}
	if g.Images != "" {
		if _, err := ParseID(g.Images); err != nil {
			return errInvalidGroupImages
		}
	}
	if g.Invites != "" {
		return errInvalidGroupInvites
	}
	if g.BlockedUsers != "" {
		return errInvalidGroupBlockedUsers
	}
	return nil
}

func (g *group) AfterDelete(tx *gorm.DB) error {
	if g.ID == NilID {
		log.Error("attempt to run delete hooks on empty group")
		return nil
	}
	err := deleteEach[groupCreation](tx, "id = ?", g.ID)
	if err != nil {
		return err
	}
	err = deleteEach[groupMessage](tx, "destination = ?", g.ID)
	if err != nil {
		return err
	}
	err = deleteEach[updateGroup](tx, "target = ? AND custom_scope = ?", g.ID, NilID)
	if err != nil {
		return err
	}
	err = tx.Exec("DELETE FROM group_users WHERE group_id = ?", g.ID).Error
	if err != nil {
		return err
	}
	err = deleteEach[file](tx, "type = ? AND destination = ?", fileTypeGroupImage, g.ID)
	if err != nil {
		return err
	}
	err = deleteEach[draft](tx, "thread = ?", g.ID)
	if err != nil {
		return err
	}
	return nil
}

func (g *group) state() groupState {
	gs := groupState{
		name:                       g.Name,
		images:                     []ID{},
		users:                      []ID{},
		admins:                     []ID{},
		blockedUsers:               []ID{},
		invites:                    []ID{},
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

	var err error
	gs.admins, err = parseIDList(g.Admins)
	if err != nil {
		log.WithFields(log.Fields{
			"error":    err.Error(),
			"group_id": g.ID,
		}).Error("skipping invalid IDs in group admin list")
	}
	gs.blockedUsers, err = parseIDList(g.BlockedUsers)
	if err != nil {
		log.WithFields(log.Fields{
			"error":    err.Error(),
			"group_id": g.ID,
		}).Error("skipping invalid IDs in group blocked user list")
	}
	gs.invites, err = parseIDList(g.Invites)
	if err != nil {
		log.WithFields(log.Fields{
			"error":    err.Error(),
			"group_id": g.ID,
		}).Error("skipping invalid IDs in group invite list")
	}
	gs.images, err = parseIDList(g.Images)
	if err != nil {
		log.WithFields(log.Fields{
			"error":    err.Error(),
			"group_id": g.ID,
		}).Error("skipping invalid IDs in group image list")
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

	// The icon is prepared first so the group can name it, and published once the group exists to be attached to
	var icon *file
	imagesString := ""
	if len(ng.Image) > 0 {
		var err error
		icon, err = b.prepareEmbeddedFile(ng.Image, scopeGroupWithInvites, fileTypeGroupImage)
		if err != nil {
			return errors.New("error preparing new group image: " + err.Error())
		}
		imagesString = icon.ID.String()
	}

	creationTime := time.Now().Unix() - 1 // Subtract one second to ensure any invites are ordered after the group creation
	g := group{
		ID:                     NilID,
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

	// The group creation's ID is generated from the marshalled group, which has no ID yet, and the creation time.
	// That is the group's ID too, so the original state of a group cannot be modified during replication.
	gc := groupCreation{
		Nonce:     newNonce(),
		Timestamp: creationTime,
		Data:      groupData,
	}
	gc.ID = gc.generateID()
	g.ID = gc.ID

	if icon != nil {
		err := b.publishFile(icon, g.ID, g.ID)
		if err != nil {
			return errors.New("error distributing new group image: " + err.Error())
		}
	}

	gc.OriginalPayload, err = msgpack.Marshal(gc)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling group creation")
	}
	sc := b.createSignedContainer(typeGroupCreation, gc.OriginalPayload)
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
		Admins:                 []ID{b.currentUserID()},
		CreatedBy:              g.CreatedBy,
		CreatedAt:              g.CreatedAt,
		LastActivity:           g.LastActivity,
		RestrictUserManagement: ng.RestrictUserManagement,
		RestrictGroupEdits:     ng.RestrictGroupEdits,
		RestrictPosting:        ng.RestrictPosting,
	}
	if len(ng.Image) > 0 {
		uiGroup.Images = []ID{icon.ID}
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

func (b *Bounce) userIsInGroup(groupID, userID ID) bool { // TODO: replace with current group state where possible
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

func (b *Bounce) updateLastGroupActivity(groupID ID, timestamp int64) {
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

func (b *Bounce) isGroupAdmin(groupID, userID ID) bool {
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

	adminIDs, err := parseIDList(g.Admins)
	if err != nil {
		log.WithFields(log.Fields{
			"error":    err.Error(),
			"group_id": groupID,
		}).Error("skipping invalid IDs in group admin list")
	}
	for _, adminID := range adminIDs {
		if adminID == userID {
			return true
		}
	}

	return false
}

func (b *Bounce) addGroupAdmin(groupID, userID ID) {
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
	adminIDs, err := parseIDList(g.Admins)
	if err != nil {
		log.WithFields(log.Fields{
			"error":    err.Error(),
			"group_id": groupID,
		}).Error("skipping invalid IDs in group admin list")
	}
	for _, adminID := range adminIDs {
		if adminID == userID {
			alreadyAnAdmin = true
		}
		admins = append(admins, adminID.String())
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

func (b *Bounce) removeGroupAdmin(groupID, userID ID) {
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
	adminIDs, err := parseIDList(g.Admins)
	if err != nil {
		log.WithFields(log.Fields{
			"error":    err.Error(),
			"group_id": groupID,
		}).Error("skipping invalid IDs in group admin list")
	}
	for _, adminID := range adminIDs {
		if adminID == userID {
			removedUser = true
		} else {
			admins = append(admins, adminID.String())
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

func (b *Bounce) isBlockedFromGroup(groupID, userID ID) bool {
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

	blockedIDs, err := parseIDList(g.BlockedUsers)
	if err != nil {
		log.WithFields(log.Fields{
			"error":    err.Error(),
			"group_id": groupID,
		}).Error("skipping invalid IDs in group blocked user list")
	}
	for _, blockedID := range blockedIDs {
		if blockedID == userID {
			return true
		}
	}

	return false
}

func (b *Bounce) blockUserFromGroup(groupID, userID ID) {
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
	blockedIDs, err := parseIDList(g.BlockedUsers)
	if err != nil {
		log.WithFields(log.Fields{
			"error":    err.Error(),
			"group_id": groupID,
		}).Error("skipping invalid IDs in group blocked user list")
	}
	for _, blockedID := range blockedIDs {
		if blockedID == userID {
			alreadyBlocked = true
		}
		blocked = append(blocked, blockedID.String())
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

func (b *Bounce) isInvited(groupID, userID ID) bool {
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

	inviteIDs, err := parseIDList(g.Invites)
	if err != nil {
		log.WithFields(log.Fields{
			"error":    err.Error(),
			"group_id": groupID,
		}).Error("skipping invalid IDs in group invite list")
	}
	for _, inviteID := range inviteIDs {
		if inviteID == userID {
			return true
		}
	}

	return false
}

func (b *Bounce) addGroupInvite(groupID, userID ID) {
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
	inviteIDs, err := parseIDList(g.Invites)
	if err != nil {
		log.WithFields(log.Fields{
			"error":    err.Error(),
			"group_id": groupID,
		}).Error("skipping invalid IDs in group invite list")
	}
	for _, inviteID := range inviteIDs {
		if inviteID == userID {
			alreadyInvited = true
		}
		invites = append(invites, inviteID.String())
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

func (b *Bounce) removeGroupInvite(groupID, userID ID) {
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
	inviteIDs, err := parseIDList(g.Invites)
	if err != nil {
		log.WithFields(log.Fields{
			"error":    err.Error(),
			"group_id": groupID,
		}).Error("skipping invalid IDs in group invite list")
	}
	for _, inviteID := range inviteIDs {
		if inviteID == userID {
			removedUser = true
		} else {
			invites = append(invites, inviteID.String())
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

func (b *Bounce) updateConsensusForGroupsWithUser(userID ID) {
	var groups []ID
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

func (b *Bounce) SetGroupLastOpened(groupID ID, timestamp int64) {
	err := b.database.Table("groups").Where("id = ?", groupID).Updates(map[string]interface{}{"last_opened": timestamp}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error updating group last opened time")
	}
}
