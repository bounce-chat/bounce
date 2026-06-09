package chat

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const userIntroductionProfile = "profile"
const userIntroductionAddUser = "add_user"
const userIntroductionGroup = "group"

var blockedUsers = map[uuid.UUID]bool{}
var blockedUsersMutex sync.Mutex

type user struct {
	ID                         uuid.UUID `gorm:"type:uuid;primary_key;"`
	Name                       string
	Images                     string
	Profile                    bool `gorm:"index:,where:profile = true" msgpack:"-"`
	EncryptedDevices           string
	PublicECDSAKey             []byte `msgpack:"-"`
	PrivateECDSAKey            []byte `msgpack:"-"`
	PublicECDHKey              []byte
	PrivateECDHKey             []byte `msgpack:"-"`
	OpenDM                     bool   `msgpack:"-"`
	LastOpened                 int64  `msgpack:"-"`
	Retention                  int64  `msgpack:"-"`
	ClearBefore                int64  `msgpack:"-"`
	MutedUntil                 int64  `msgpack:"-"`
	LastActivity               int64  `msgpack:"-"`
	ReadReceiptsOverridden     bool   `msgpack:"-"`
	ReadReceiptsEnabled        bool   `msgpack:"-"`
	TypingIndicatorsOverridden bool   `msgpack:"-"`
	TypingIndicatorsEnabled    bool   `msgpack:"-"`
	IntroductionMethod         string `msgpack:"-"`
	IntroductionTime           int64  `msgpack:"-"`
	Alias                      string `msgpack:"-"`
	Notes                      string `msgpack:"-"`
	Blocked                    bool   `msgpack:"-"`
	Devices                    []device
	Groups                     []group          `gorm:"many2many:group_users;" msgpack:"-"`
	ProfileSettings            *profileSettings `msgpack:"-"`
}

func (u *user) BeforeCreate(tx *gorm.DB) error {
	if u.ID == uuid.Nil {
		return errors.New("user ID must be set before creation")
	}

	u.LastActivity = time.Now().Unix()

	return nil
}

func (u *user) images() []uuid.UUID {
	imageHistory := []uuid.UUID{}
	if len(u.Images) > 0 {
		for _, imageIDString := range strings.Split(u.Images, ",") {
			imageID, err := uuid.Parse(imageIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":  err.Error(),
					"images": u.Images,
				}).Fatal("invalid UUID in user images list")
			}
			imageHistory = append(imageHistory, imageID)
		}
	}

	return imageHistory
}

func (b *Bounce) currentUser() (user, bool) {
	var currentUser user
	err := b.database.Preload("Devices.Signature").Preload(clause.Associations).Where("profile = ?", true).First(&currentUser).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return currentUser, false
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error loading current user")
		}
	}

	return currentUser, true
}

func (b *Bounce) currentUserID() uuid.UUID {
	if b.userID == uuid.Nil {
		currentUser, ok := b.currentUser()
		if !ok {
			log.Fatal("a current user must exist before currentUserID can be called")
		}
		b.userID = currentUser.ID
	}
	return b.userID
}

func (b *Bounce) getDMRetention(id uuid.UUID) int64 {
	var u user
	err := b.database.Select("retention").First(&u, "id = ?", id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Warn("error selecting message retention for unknown user")
			return 0
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error selecting message retention from user")
		}
	}
	return u.Retention
}

func (b *Bounce) addBlockedGroup(groupID uuid.UUID) {
	var joinedBlockedGroups string
	err := b.database.Model(&profileSettings{}).Select("blocked_groups").Where("user_id = ?", b.currentUserID()).First(&joinedBlockedGroups).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error selecting profile user blocked groups")
	}

	blocked := []string{}
	alreadyBlocked := false
	if len(joinedBlockedGroups) > 0 {
		for _, blockedIDString := range strings.Split(joinedBlockedGroups, ",") {
			blockedID, err := uuid.Parse(blockedIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":          err.Error(),
					"user_id":        b.currentUserID(),
					"blocked_groups": joinedBlockedGroups,
				}).Fatal("invalid UUID in blocked groups list")

			}
			if blockedID == groupID {
				alreadyBlocked = true
			}
			blocked = append(blocked, blockedIDString)
		}
	}
	if !alreadyBlocked {
		blocked = append(blocked, groupID.String())
	}

	err = b.database.Model(&profileSettings{}).Select("blocked_groups").Where("user_id = ?", b.currentUserID()).Update("blocked_groups", strings.Join(blocked, ",")).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error updating blocked groups list")
	}
}

func (b *Bounce) blockedGroups() []uuid.UUID {
	blocked := []uuid.UUID{}

	var settings profileSettings
	err := b.database.Select("blocked_groups").Where("user_id = ?", b.currentUserID()).First(&settings).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error selecting profile user blocked groups")
	}

	if len(settings.BlockedGroups) > 0 {
		for _, blockedIDString := range strings.Split(settings.BlockedGroups, ",") {
			blockedID, err := uuid.Parse(blockedIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":          err.Error(),
					"user_id":        b.currentUserID(),
					"blocked_groups": settings.BlockedGroups,
				}).Fatal("invalid UUID in blocked groups list")

			}
			blocked = append(blocked, blockedID)
		}
	}

	return blocked
}
func (b *Bounce) SetProfile(profileName string, image []byte, deviceName string) error {
	newID := uuid.New()
	iconID := uuid.Nil
	if len(image) > 0 {
		iconID = uuid.New()
	}

	var count int64
	b.database.Model(&user{}).Where("profile = ?", true).Count(&count)
	if count > 0 {
		return errors.New("profile already exists on this device")
	}

	d := device{
		ID:        uuid.New(),
		Address:   b.network.Address(),
		Timestamp: time.Now().Unix(),
	}

	curve := ecdh.X25519()
	privateECDHKey, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error generating x25519 private key")
	}
	publicECDHKey := privateECDHKey.PublicKey()
	publicECDSAKey, privateECDSAKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error generating ECDSA keypair")
	}

	u := &user{
		ID:                 newID,
		Name:               profileName,
		Profile:            true,
		PublicECDSAKey:     []byte(publicECDSAKey),
		PrivateECDSAKey:    privateECDSAKey,
		PublicECDHKey:      publicECDHKey.Bytes(),
		PrivateECDHKey:     privateECDHKey.Bytes(),
		OpenDM:             true,
		IntroductionMethod: userIntroductionProfile,
		IntroductionTime:   time.Now().Unix(),
		Devices: []device{
			d,
		},
		ProfileSettings: &profileSettings{
			DefaultGroupRetention:          int64(time.Duration(24 * time.Hour * 7 * 4).Seconds()),
			DefaultSendReadReceipts:        true,
			DefaultSendTypingIndicators:    true,
			NewGroupRestrictUserManagement: true,
			NewGroupRestrictGroupEdits:     false,
			NewGroupRestrictPosting:        false,
			DefaultDMRetention:             int64(time.Duration(24 * time.Hour * 7 * 4).Seconds()),
		},
	}
	if len(image) > 0 {
		u.Images = iconID.String()
	} else {
		u.Images = ""
	}
	err = b.database.Create(u).Error
	if err != nil {
		return err
	}
	if len(image) > 0 {
		err := b.embedFile(iconID, image, scopeGlobal, u.ID, fileTypeUserImage, u.ID)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Warn("error creating file for new profile image")
		}
	}

	go func() {
		b.ui.ProfileSet(
			User{
				ID:               u.ID,
				Name:             u.Name,
				Images:           u.images(),
				IntroductionTime: u.IntroductionTime,
			},
			Device{
				ID:        d.ID,
				Address:   d.Address,
				CreatedAt: d.Timestamp,
				Local:     true,
			},
		)
		b.ui.SetDMState(
			u.ID,
			DMState{
				Open:                           true,
				Retention:                      u.ProfileSettings.DefaultDMRetention,
				MutedUntil:                     0,
				OverrideReadReceiptSetting:     false,
				ReadReceiptsEnabled:            u.ProfileSettings.DefaultSendReadReceipts,
				OverrideTypingIndicatorSetting: false,
				TypingIndicatorsEnabled:        u.ProfileSettings.DefaultSendTypingIndicators,
			},
		)
		b.updateSettingsState()
		b.RenameDevice(d.ID, deviceName)
		b.createAndShareDeviceECDHKey()

		// Set the keys to their current values in an updateUser, just to keep a permanent copy
		// in the database, in case we need to re-key an encrypted sync device later
		ks := keySet{
			PublicECDSAKey:  u.PublicECDSAKey,
			PrivateECDSAKey: u.PrivateECDSAKey,
			PublicECDHKey:   u.PublicECDHKey,
			PrivateECDHKey:  u.PrivateECDHKey,
		}
		keySetData, err := msgpack.Marshal(&ks)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("failed to marshal key set")
			return
		}

		err = b.applyAndBroadcastUpdateUser(updateUser{
			ID:        uuid.New(),
			Target:    b.currentUserID(),
			Timestamp: time.Now().Unix(),
			Type:      updateUserTypeReplaceKeys,
			Data:      keySetData,
		})
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("failed to set initial keys in an updateUser")
		}
	}()
	return nil
}

func (b *Bounce) directMessageWrittenBeforeHistoryCleared(userID uuid.UUID, messageWrittenAt int64) bool {
	// User ID is computed via XOR of my ID with source and destination, if it's a self-DM it would be nil
	if userID == uuid.Nil {
		userID = b.currentUserID()
	}

	var u user
	err := b.database.Select("clear_before").First(&u, "id = ?", userID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"id":    userID,
				"error": err.Error(),
			}).Error("error selecting clear before for unknown user")
			return false
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error selecting clear before from group")
		}
	}

	return messageWrittenAt < u.ClearBefore
}

func (b *Bounce) updateLastUserActivity(userID uuid.UUID, timestamp int64) {
	var u user
	err := b.database.Where("id = ?", userID).First(&u).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"user_id": userID,
			}).Error("error finding user for last activity update")
			return
		} else {
			log.WithFields(log.Fields{
				"user_id": userID,
			}).Fatal("database error finding user for last activity update")
		}
	}

	if timestamp > u.LastActivity {
		err = b.database.Model(&u).Update("last_activity", timestamp).Error
		if err != nil {
			log.WithFields(log.Fields{
				"user_id": userID,
			}).Fatal("database error updating user last activity")
		}
	}
}

func (b *Bounce) dmOpenByDefault(userID uuid.UUID) bool {
	var u user
	err := b.database.Select("introduction_method").Where("id = ?", userID).First(&u).Error
	if err != nil {
		return false
	}

	return u.IntroductionMethod != userIntroductionGroup
}

func validUserName(name string) bool {
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

func xor(uuid1, uuid2 uuid.UUID) uuid.UUID {
	xored := [16]byte{}
	for i, b := range uuid1 {
		xored[i] = b ^ uuid2[i]
	}

	xorUUID, err := uuid.FromBytes(xored[:])
	if err != nil {
		log.WithFields(log.Fields{
			"uuid1": uuid1,
			"uuid2": uuid2,
			"xored": xored,
			"error": err.Error(),
		}).Fatal("unable to create UUID from XORed UUIDs")
	}

	return xorUUID
}

func cacheBlockedUser(userID uuid.UUID) {
	blockedUsersMutex.Lock()
	defer blockedUsersMutex.Unlock()

	blockedUsers[userID] = true
}

func cacheUnblockedUser(userID uuid.UUID) {
	blockedUsersMutex.Lock()
	defer blockedUsersMutex.Unlock()

	delete(blockedUsers, userID)
}

func blockedUser(userID uuid.UUID) bool {
	blockedUsersMutex.Lock()
	defer blockedUsersMutex.Unlock()

	_, ok := blockedUsers[userID]
	return ok
}

func (b *Bounce) SetDMLastOpened(userID uuid.UUID, timestamp int64) {
	err := b.database.Table("users").Where("id = ?", userID).Updates(map[string]interface{}{"last_opened": timestamp}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error updating user last opened time")
	}
}
