package chat

import (
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
		&profile{},
		&user{},
		&device{},
	)

	bounce.seedTestDatabase()
}

func (bounce *Bounce) buildInitialState() InitialState {
	profileSet := false
	var count int64
	bounce.database.Model(&profile{}).Count(&count)
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

type profile struct {
	ID      uuid.UUID `gorm:"type:uuid;primary_key;"`
	Name    string
	Devices []device
}

func (profile *profile) BeforeCreate(tx *gorm.DB) error {
	profile.ID = uuid.New()
	return nil
}

type device struct {
	ID        uuid.UUID `gorm:"type:uuid;primary_key;"`
	Name      string
	UserID    uuid.UUID
	ProfileID uuid.UUID
	Address   string
}

func (device *device) BeforeCreate(tx *gorm.DB) error {
	device.ID = uuid.New()
	return nil
}

type mutualDeviceSignature struct {
	DeviceOne   string
	DeviceTwo   string
	OneSignsTwo []byte
	TwoSignsOne []byte
}

type user struct {
	ID      uuid.UUID `gorm:"type:uuid;primary_key;"`
	Name    string
	Devices []device
}

func (user *user) BeforeCreate(tx *gorm.DB) error {
	user.ID = uuid.New()
	return nil
}
