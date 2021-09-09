package chat

import (
	"errors"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

func (bounce *Bounce) openDatabase() {
	databaseFile := bounce.configDirectory + "/bounce.db"

	var err error
	bounce.database, err = gorm.Open(sqlite.Open(databaseFile), &gorm.Config{})
	if err != nil {
		log.WithFields(log.Fields{
			"file":  databaseFile,
			"error": err.Error(),
		}).Fatal("error opening database")
	}

	bounce.database.AutoMigrate(
		&user{},
		&device{},
		&profileExport{},
		&introductionSignature{},
		&DirectMessage{}, // TODO: still need to decide if we'll export a simplified one for the UI
		&referenceOffer{},
	)

	// TODO: prune old reference offers.  Better to kick off something that prunes everything old (messages, profile state changes, etc)?
}

func (bounce *Bounce) buildInitialState() InitialState {
	var profile *User
	var count int64
	bounce.database.Model(&user{}).Where("profile = ?", true).Count(&count)
	if count > 0 {
		var dbProfile user
		bounce.database.Model(&user{}).Where("profile = ?", true).First(&dbProfile) // TODO: error check, clean this up
		profile = &User{
			ID:   dbProfile.ID,
			Name: dbProfile.Name,
		}
	}

	users := []user{}
	bounce.database.Find(&users) // TODO: exclude current profile?  Self DMs are actually useful for sending data between devices, saving things
	chatUsers := []User{}
	for _, u := range users {
		chatUsers = append(chatUsers, User{
			ID:   u.ID,
			Name: u.Name,
		})
	}

	return InitialState{
		Profile: profile,
		Users:   chatUsers,
	}
}

func (bounce *Bounce) currentUser() user {
	var currentUser user
	err := bounce.database.Model(&user{}).Preload(clause.Associations).Where("profile = ?", true).First(&currentUser).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error loading current user")
	}
	return currentUser
}

func (bounce *Bounce) currentDevice() device {
	currentAddress, err := bounce.network.Address()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error loading current address when requesting current device")
	}

	var currentDevice device
	err = bounce.database.Model(&device{}).Preload(clause.Associations).Where("address = ?", currentAddress).First(&currentDevice).Error // TODO: do I need to load associations here?
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error loading current device")
	}
	return currentDevice
}

type device struct {
	ID        uuid.UUID              `gorm:"type:uuid;primary_key;" json:"-"`
	Name      string                 `json:"-"`
	UserID    uuid.UUID              `json:"-"`
	Address   string                 `gorm:"unique"`
	Signature *introductionSignature `json:",omitempty"`
}

func (device *device) BeforeCreate(tx *gorm.DB) error {
	device.ID = uuid.New()
	return nil
}

type introductionSignature struct {
	ID                           uuid.UUID `gorm:"type:uuid;primary_key;"`
	DeviceID                     uuid.UUID
	PreexistingDevice            string // TODO: should this reference a device model?
	SignatureOfNewDevice         []byte
	SignatureOfPreexistingDevice []byte
}

func (introductionSignature *introductionSignature) BeforeCreate(tx *gorm.DB) error {
	introductionSignature.ID = uuid.New()
	return nil
}

type user struct {
	ID      uuid.UUID `gorm:"type:uuid;primary_key;"`
	Name    string
	Profile bool `json:"-"`
	Devices []device
}

func (u *user) BeforeCreate(tx *gorm.DB) error {
	if u.Profile {
		var count int64
		tx.Model(&user{}).Where("profile = ?", true).Count(&count)
		if count > 0 {
			return errors.New("profile user already exists")
		}
	}
	return nil
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
	profileExport.ID = uuid.New()
	return nil
}
