package chat

import (
	"errors"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

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
	)

	bounce.seedTestDatabase()
}

func (bounce *Bounce) buildInitialState() InitialState {
	profileSet := false
	var count int64
	bounce.database.Model(&user{}).Where("profile = ?", true).Count(&count)
	if count > 0 {
		profileSet = true
	}

	users := []user{}
	bounce.database.Find(&users)
	chatUsers := []User{}
	for _, u := range users {
		chatUsers = append(chatUsers, User{
			ID:   u.ID.String(),
			Name: u.Name,
		})
	}

	return InitialState{
		ProfileSet: profileSet,
		Users:      chatUsers,
	}
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
	ID                       uuid.UUID `gorm:"type:uuid;primary_key;"`
	DeviceID                 uuid.UUID
	SigningDevice            string
	SigningDeviceSignature   []byte // TODO: clean these field names up
	SignatureOfSigningDevice []byte
}

func (introductionSignature *introductionSignature) BeforeCreate(tx *gorm.DB) error {
	introductionSignature.ID = uuid.New()
	return nil
}

type user struct {
	ID      uuid.UUID `gorm:"type:uuid;primary_key;" json:"-"`
	Name    string
	Profile bool `json:"-"`
	Devices []device
}

func (u *user) BeforeCreate(tx *gorm.DB) error {
	u.ID = uuid.New()

	if u.Profile {
		var count int64
		tx.Model(&user{}).Where("profile = ?", true).Count(&count)
		if count > 0 {
			return errors.New("profile user already exists")
		}
	}
	return nil
}
