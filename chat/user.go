package chat

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type user struct {
	ID               uuid.UUID `gorm:"type:uuid;primary_key;"`
	Name             string
	Profile          bool `json:"-"`
	MessageRetention int64
	Devices          []device
}

func (u *user) BeforeCreate(tx *gorm.DB) error {
	if u.Profile {
		var count int64
		tx.Model(&user{}).Where("profile = ?", true).Count(&count)
		if count > 0 {
			return errors.New("profile user already exists")
		}
	}

	// DMs default to expiring in 1 week
	u.MessageRetention = 7 * 24 * 60 * 60

	return nil
}

func (bounce *Bounce) currentUser() (user, bool) {
	var currentUser user
	err := bounce.database.Preload(clause.Associations).Where("profile = ?", true).First(&currentUser).Error
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

func (bounce *Bounce) currentUserID() uuid.UUID {
	if bounce.userID == uuid.Nil {
		currentUser, ok := bounce.currentUser()
		if !ok {
			log.Fatal("a current user must exist before currentUserID can be called")
		}
		bounce.userID = currentUser.ID
	}
	return bounce.userID
}

func (bounce *Bounce) getUserDMRetention(id uuid.UUID) int64 {
	var u user
	err := bounce.database.Select("message_retention").Find(&u, "id = ?", id).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error selecting message retention from user")
	}
	return u.MessageRetention
}

func (bounce *Bounce) setProfile(profileName, deviceName string) (uuid.UUID, error) {
	newID := uuid.New()

	var count int64
	bounce.database.Model(&user{}).Where("profile = ?", true).Count(&count)
	if count > 0 {
		return newID, errors.New("profile already exists on this device")
	}

	return newID, bounce.database.Create(&user{
		ID:      newID,
		Name:    profileName,
		Profile: true,
		Devices: []device{
			device{
				Name:    deviceName,
				Address: bounce.network.Address(),
			},
		},
	}).Error
}

func (bounce *Bounce) exportContact(name string, expiration int64, oneTime bool) []byte {
	var count int64
	bounce.database.Model(&user{}).Where("profile = ?", true).Count(&count)
	if count != 1 {
		log.Fatal("no contact exists to export")
	}

	myProfile, exists := bounce.currentUser()
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

	err = bounce.database.Save(&export).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving contact export")
	}

	return bytes
}

func (bounce *Bounce) importUser(data []byte) (User, error) {
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
		_, exists := bounce.getDeviceFromAddress(contactDevice.Address)
		if exists {
			return User{}, errors.New("contact contains device that already exists in database")
		}
	}

	// Make sure this contact has a valid device group
	if !newUser.Profile.validDeviceGroup() {
		return User{}, errors.New("invalid device group")
	}

	uiUser := User{
		ID:   newUser.Profile.ID,
		Name: newUser.Profile.Name,
	}

	// Save to the database
	return uiUser, bounce.database.Create(&newUser.Profile).Error
	// TODO: some sort of UI feedback on the secret being accepted on the remote side?
	// As in, bounce only accepts DMs from user's in a shared group or who send an import secret
	// TODO: try to dial right away
}
