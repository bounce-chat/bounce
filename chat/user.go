package chat

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type user struct {
	ID                        uuid.UUID `gorm:"type:uuid;primary_key;"`
	Name                      string
	Profile                   bool     `gorm:"index:,where:profile = true" json:"-"`
	MessageRetention          int64    `json:"-"`
	ClearBefore               int64    `json:"-"`
	LastDMSettingsUpdate      int64    `json:"-"`
	NotificationsEnabled      bool     `json:"-"`
	NotificationsMutedUntil   uint64   `json:"-"`
	LastLocalDMSettingsUpdate int64    `json:"-"`
	Devices                   []device `msgpack:"-"`
	payload                   []byte
	payloadMutex              sync.Mutex
}

func (u *user) BeforeCreate(tx *gorm.DB) error {
	if u.ID == uuid.Nil {
		log.Fatal("attempt to create user with nil ID, user ID must be set before creation")
	}

	if u.Profile {
		var count int64
		tx.Model(&user{}).Where("profile = ?", true).Count(&count)
		if count > 0 {
			return errors.New("profile user already exists")
		}
		u.NotificationsEnabled = false
	} else {
		// DMs with other people default to expiring in 1 week
		u.MessageRetention = 7 * 24 * 60 * 60
		u.NotificationsEnabled = true
	}
	u.LastDMSettingsUpdate = time.Now().Unix()
	u.LastLocalDMSettingsUpdate = time.Now().Unix()

	return nil
}

func (u *user) AfterDelete(tx *gorm.DB) error {
	return tx.Where("frame_id = ? AND frame_type = ?", u.ID, typeUser).Delete(&deliveryRecord{}).Error
}

func (u *user) getID() uuid.UUID {
	return u.ID
}

func (u *user) getScope(_ uuid.UUID) int {
	return scopeSync
}

func (u *user) getDestination(_ uuid.UUID) uuid.UUID {
	return uuid.Nil
}

func (u *user) getType() uint16 {
	return typeUser
}

func (u *user) getPayload() []byte {
	u.payloadMutex.Lock()
	defer u.payloadMutex.Unlock()

	if len(u.payload) == 0 {
		bytes, err := msgpack.Marshal(u)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal user")
		}
		u.payload = bytes
	}
	return u.payload
}

func (b *bounce) handleUser(peer string, payload []byte) {
	// Make sure this is a sync device
	srcDevice, exists := b.getDeviceFromAddress(peer)
	if !exists {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("ignoring a user sent from an unknown device")
		return
	}

	if srcDevice.UserID != b.currentUserID() {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("ignoring a user sent from a device that is not a sync device")
		return
	}

	// Unmarshal the device
	var u user
	err := msgpack.Unmarshal(payload, &u)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling user")
		return
	}

	// Save it
	err = b.database.Session(&gorm.Session{SkipHooks: true}).Create(&u).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error saving new user")
	}
	b.markDeliveredTo(&u, peer)

	// ACK it
	go b.broadcast(&ack{
		destination: srcDevice.ID,
		Users:       u.ID.String(),
	})

	// Tell the UI about it
	b.userInterface.UserImported(User{ID: u.ID, Name: u.Name})
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
	// Get some of the stack trace
	// TODO: this is just for debugging and can be removed later
	caller1 := "unknown"
	_, file, lineNumber, ok := runtime.Caller(1)
	if ok {
		caller1 = fmt.Sprintf("%s:%d", file, lineNumber)
	}
	caller2 := "unknown"
	_, file, lineNumber, ok = runtime.Caller(2)
	if ok {
		caller2 = fmt.Sprintf("%s:%d", file, lineNumber)
	}
	caller3 := "unknown"
	_, file, lineNumber, ok = runtime.Caller(3)
	if ok {
		caller3 = fmt.Sprintf("%s:%d", file, lineNumber)
	}

	if b.userID == uuid.Nil {
		currentUser, ok := b.currentUser()
		if !ok {
			log.WithFields(log.Fields{
				"caller1": caller1,
				"caller2": caller2,
				"caller3": caller3,
			}).Fatal("a current user must exist before currentUserID can be called")
		}
		b.userID = currentUser.ID
	}
	return b.userID
}

func (b *bounce) getUserDMRetention(id uuid.UUID) int64 {
	var u user
	err := b.database.Select("message_retention").First(&u, "id = ?", id).Error
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
	return u.MessageRetention
}

func (b *bounce) setProfile(profileName, deviceName string) (uuid.UUID, error) {
	newID := uuid.New()

	var count int64
	b.database.Model(&user{}).Where("profile = ?", true).Count(&count)
	if count > 0 {
		return newID, errors.New("profile already exists on this device")
	}

	return newID, b.database.Create(&user{
		ID:      newID,
		Name:    profileName,
		Profile: true,
		Devices: []device{
			device{
				Name:    deviceName,
				Address: b.network.Address(),
			},
		},
	}).Error
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

	uiUser := User{
		ID:   newUser.Profile.ID,
		Name: newUser.Profile.Name,
	}

	// Save to the database
	return uiUser, b.database.Create(&newUser.Profile).Error
	// TODO: some sort of UI feedback on the secret being accepted on the remote side?
	// As in, bounce only accepts DMs from user's in a shared group or who send an import secret
	// TODO: try to dial right away
	// TODO: broadcast this user and their devices to all sync devices
}
