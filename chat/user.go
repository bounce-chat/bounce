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
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const userIntroductionProfile = "profile"
const userIntroductionAddUser = "add_user"
const userIntroductionGroup = "group"

var blockedUsers = map[ID]bool{}
var blockedUsersMutex sync.Mutex

var errInvalidUserName = errors.New("invalid name")
var errInvalidUserImages = errors.New("user images is not a list of IDs")
var errInvalidEncryptedDevices = errors.New("user encrypted devices is not a list of addresses")

type user struct {
	ID                         ID `gorm:"primary_key;"`
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
	IntroductionMetadata       ID     `msgpack:"-"`
	Alias                      string `msgpack:"-"`
	Notes                      string `msgpack:"-"`
	Blocked                    bool   `msgpack:"-"`
	Accepted                   bool   `msgpack:"-"`
	Devices                    []device
	Groups                     []group          `gorm:"many2many:group_users;" msgpack:"-"`
	ProfileSettings            *profileSettings `msgpack:"-"`
}

func (u *user) BeforeCreate(tx *gorm.DB) error {
	if u.ID == NilID {
		return errors.New("user ID must be set before creation")
	}

	if err := validUserRecord(*u); err != nil {
		return err
	}

	u.LastActivity = time.Now().Unix()

	return nil
}

func validUserRecord(u user) error {
	if !validUserName(u.Name) {
		return errInvalidUserName
	}
	if !validImageHistory(u.Images) {
		return errInvalidUserImages
	}
	if !validEncryptedDeviceList(u.EncryptedDevices) {
		return errInvalidEncryptedDevices
	}
	return nil
}

func validEncryptedDeviceList(encryptedDevices string) bool {
	if encryptedDevices == "" {
		return true
	}
	for _, address := range strings.Split(encryptedDevices, ",") {
		if !onionAddressRegex.MatchString(address) {
			return false
		}
	}
	return true
}

func validImageHistory(images string) bool {
	_, err := parseIDList(images)
	return err == nil
}

func (u *user) images() []ID {
	imageHistory, err := parseIDList(u.Images)
	if err != nil {
		log.WithFields(log.Fields{
			"error":   err.Error(),
			"user_id": u.ID,
		}).Error("skipping invalid IDs in user images list")
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

func (b *Bounce) CurrentUserID() ID {
	return b.currentUserID()
}

var currentUserMutex sync.Mutex

func (b *Bounce) currentUserID() ID {
	currentUserMutex.Lock()
	defer currentUserMutex.Unlock()

	if b.userID == NilID {
		currentUser, ok := b.currentUser()
		if !ok {
			log.Fatal("a current user must exist before currentUserID can be called")
		}
		b.userID = currentUser.ID
	}
	return b.userID
}

func (b *Bounce) getDMRetention(id ID) int64 {
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

func (b *Bounce) addBlockedGroup(groupID ID) {
	var joinedBlockedGroups string
	err := b.database.Model(&profileSettings{}).Select("blocked_groups").Where("user_id = ?", b.currentUserID()).First(&joinedBlockedGroups).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error selecting profile user blocked groups")
	}

	blocked := []string{}
	alreadyBlocked := false
	blockedIDs, err := parseIDList(joinedBlockedGroups)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("skipping invalid IDs in blocked groups list")
	}
	for _, blockedID := range blockedIDs {
		if blockedID == groupID {
			alreadyBlocked = true
		}
		blocked = append(blocked, blockedID.String())
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

func (b *Bounce) blockedGroups() []ID {
	blocked := []ID{}

	var settings profileSettings
	err := b.database.Select("blocked_groups").Where("user_id = ?", b.currentUserID()).First(&settings).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error selecting profile user blocked groups")
	}

	blocked, err = parseIDList(settings.BlockedGroups)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("skipping invalid IDs in blocked groups list")
	}

	return blocked
}
func (b *Bounce) SetProfile(profileName string, image []byte, deviceName string) error {
	if !validUserName(profileName) {
		return errInvalidUserName
	}

	newID := NewID()

	// The icon is prepared first so the profile can name it, and published once the profile exists to be attached to
	var icon *file
	if len(image) > 0 {
		var err error
		icon, err = b.prepareEmbeddedFile(image, scopeGlobal, fileTypeUserImage)
		if err != nil {
			return errors.New("error preparing profile image: " + err.Error())
		}
	}

	var count int64
	b.database.Model(&user{}).Where("profile = ?", true).Count(&count)
	if count > 0 {
		return errors.New("profile already exists on this device")
	}

	d := device{
		Nonce:     newNonce(),
		UserID:    newID,
		Address:   b.network.Address(),
		Timestamp: time.Now().Unix(),
	}
	d.ID = d.generateID()

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
		Accepted:           true,
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
	if icon != nil {
		u.Images = icon.ID.String()
	} else {
		u.Images = ""
	}
	err = b.database.Create(u).Error
	if err != nil {
		return err
	}
	if icon != nil {
		err := b.publishFile(icon, u.ID, u.ID)
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
			Nonce:     newNonce(),
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

func (b *Bounce) directMessageWrittenBeforeHistoryCleared(userID ID, messageWrittenAt int64) bool {
	// User ID is computed via XOR of my ID with source and destination, if it's a self-DM it would be nil
	if userID == NilID {
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

func (b *Bounce) updateLastUserActivity(userID ID, timestamp int64) {
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

func (b *Bounce) dmOpenByDefault(userID ID) bool {
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

func xor(one, two ID) ID {
	var xored ID
	for i := range one {
		xored[i] = one[i] ^ two[i]
	}
	return xored
}

func cacheBlockedUser(userID ID) {
	blockedUsersMutex.Lock()
	defer blockedUsersMutex.Unlock()

	blockedUsers[userID] = true
}

func cacheUnblockedUser(userID ID) {
	blockedUsersMutex.Lock()
	defer blockedUsersMutex.Unlock()

	delete(blockedUsers, userID)
}

func blockedUser(userID ID) bool {
	blockedUsersMutex.Lock()
	defer blockedUsersMutex.Unlock()

	_, ok := blockedUsers[userID]
	return ok
}

func (b *Bounce) peerOwner(address string) (ID, bool) {
	if dev, ok := b.getDeviceFromAddress(address); ok {
		return dev.UserID, true
	}
	encryptedDeviceCacheMutex.Lock()
	owner, ok := encryptedDeviceCache[address]
	encryptedDeviceCacheMutex.Unlock()
	return owner, ok
}

func (b *Bounce) peerIsBlocked(address string) bool {
	owner, ok := b.peerOwner(address)
	return ok && blockedUser(owner)
}

func (b *Bounce) SetDMLastOpened(userID ID, timestamp int64) {
	err := b.database.Table("users").Where("id = ?", userID).Updates(map[string]interface{}{"last_opened": timestamp}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error updating user last opened time")
	}
}
