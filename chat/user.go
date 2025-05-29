package chat

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type user struct {
	ID                         uuid.UUID `gorm:"type:uuid;primary_key;"`
	Name                       string
	Images                     string
	Profile                    bool  `gorm:"index:,where:profile = true" json:"-" msgpack:"-"`
	Retention                  int64 `json:"-" msgpack:"-"`
	ClearBefore                int64 `json:"-" msgpack:"-"`
	MutedUntil                 int64 `json:"-" msgpack:"-"`
	LastActivity               int64 `json:"-" msgpack:"-"`
	ReadReceiptsOverridden     bool  `json:"-" msgpack:"-"`
	ReadReceiptsEnabled        bool  `json:"-" msgpack:"-"`
	TypingIndicatorsOverridden bool  `json:"-" msgpack:"-"`
	TypingIndicatorsEnabled    bool  `json:"-" msgpack:"-"`
	Devices                    []device
	Groups                     []group          `gorm:"many2many:group_users;" json:"-" msgpack:"-"`
	ProfileSettings            *profileSettings `json:"-" msgpack:"-"`
	LocalSettings              *localSettings   `json:"-" msgpack:"-"`
	payload                    []byte
	payloadMutex               sync.Mutex
}

func (u *user) BeforeCreate(tx *gorm.DB) error {
	if u.ID == uuid.Nil {
		return errors.New("user ID must be set before creation")
	}

	u.LastActivity = time.Now().Unix()

	return nil
}

func (b *bounce) currentUser() (user, bool) {
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

func (b *bounce) currentUserID() uuid.UUID {
	if b.userID == uuid.Nil {
		currentUser, ok := b.currentUser()
		if !ok {
			log.Fatal("a current user must exist before currentUserID can be called")
		}
		b.userID = currentUser.ID
	}
	return b.userID
}

func (b *bounce) getDMRetention(id uuid.UUID) int64 {
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

func (b *bounce) addBlockedGroup(groupID uuid.UUID) {
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

func (b *bounce) blockedGroups() []uuid.UUID {
	blocked := []uuid.UUID{}

	var joinedBlockedGroups string
	err := b.database.Model(&profileSettings{}).Select("blocked_groups").Where("user_id = ?", b.currentUserID()).First(&joinedBlockedGroups).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error selecting profile user blocked groups")
	}

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
			blocked = append(blocked, blockedID)
		}
	}

	return blocked
}

func (b *bounce) setProfile(profileName, deviceName string) (uuid.UUID, error) {
	newID := uuid.New()

	var count int64
	b.database.Model(&user{}).Where("profile = ?", true).Count(&count)
	if count > 0 {
		return newID, errors.New("profile already exists on this device")
	}

	d := device{
		ID:        uuid.New(),
		Address:   b.network.Address(),
		Timestamp: time.Now().Unix(),
	}
	u := &user{
		ID:      newID,
		Name:    profileName,
		Profile: true,
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
		},
		LocalSettings: &localSettings{
			DarkMode:                        true,
			NeverAskForBatteryOptimizations: false,
		},
	}
	err := b.database.Create(u).Error
	if err != nil {
		return newID, err
	}

	go b.userInterface.DeviceAdded(Device{
		ID:        d.ID,
		Address:   d.Address,
		CreatedAt: d.Timestamp,
		Local:     true,
	})
	err = b.renameDevice(d.ID, deviceName)
	return newID, err
}

type profileExport struct {
	ID         uuid.UUID `gorm:"type:uuid;primary_key;" json:"-"`
	Secret     string
	Name       string `json:"-"`
	OneTimeUse bool   `json:"-"`
	Expiration int64
	Profile    user `gorm:"-"`
}

func (profileExport *profileExport) BeforeCreate(tx *gorm.DB) error {
	if profileExport.ID != uuid.Nil {
		log.Fatal("unexpected profileExport primary key assigned before create")
	}
	profileExport.ID = uuid.New()
	return nil
}

func (b *bounce) exportContact(name string, expiration int64, oneTime bool) []byte {
	myProfile, exists := b.currentUser()
	if !exists {
		log.Fatal("cannot export contact when no profile exists")
	}
	secretBytes := make([]byte, 16)
	rand.Read(secretBytes)
	secret := fmt.Sprintf("%x", secretBytes)

	export := profileExport{
		Name:       name,
		Secret:     secret,
		Expiration: expiration,
		OneTimeUse: oneTime,
		Profile:    myProfile,
	}

	bytes, err := json.Marshal(export)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling contact export")
	}

	err = b.database.Create(&export).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving contact export")
	}

	return bytes
}

func (b *bounce) importUser(data []byte) (User, error) {
	newUser := profileExport{}
	err := json.Unmarshal(data, &newUser)
	if err != nil {
		return User{}, err
	}

	// Make sure the export hasn't expired
	if newUser.Expiration != 0 && time.Now().Unix() >= newUser.Expiration {
		return User{}, errors.New("file has expired")
	}

	// Make sure we don't already know about any of the devices
	for _, contactDevice := range newUser.Profile.Devices {
		_, exists := b.getDeviceFromAddress(contactDevice.Address)
		if exists {
			return User{}, errors.New("contact contains device that already exists in database")
		}
	}

	// Make sure this contact has a valid device group
	if !b.hasValidDeviceGroup(newUser.Profile) {
		return User{}, errors.New("invalid device group")
	}

	// Save the new user to the database
	err = b.database.Create(&newUser.Profile).Error
	if err != nil {
		return User{}, err
	}

	// TODO: some sort of UI feedback on the secret being accepted on the remote side?
	// As in, bounce only accepts DMs from user's in a shared group or who send an import secret

	// Inform the user sync devices about it
	//b.broadcast(&newUser.Profile)

	// Set the defaul local DM settings
	b.setDMMutedUntil(newUser.Profile.ID, int64(0)) // TODO: make this default a setting on our profile

	// Set the default DM settings
	defaultMessageRetention := int64(7 * 24 * 60 * 60) // TODO: make this a setting on our profile
	b.setDMRetention(newUser.Profile.ID, defaultMessageRetention)

	// Try to connect to the user we just imported
	b.userConnectionDesired(newUser.Profile.ID)

	// Return a UI representation of the user to the frontend
	return User{
		ID:   newUser.Profile.ID,
		Name: newUser.Profile.Name,
	}, nil
}

func (b *bounce) directMessageWrittenBeforeHistoryCleared(userID uuid.UUID, messageWrittenAt int64) bool {
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

func (b *bounce) updateLastUserActivity(userID uuid.UUID, timestamp int64) {
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
