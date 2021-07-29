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
}

func (bounce *Bounce) buildInitialState() InitialState {
	return InitialState{}
}

type profile struct {
	ID      uuid.UUID `gorm:"type:uuid;primary_key;"`
	Name    string
	Devices []device
}

type device struct {
	ID        uuid.UUID `gorm:"type:uuid;primary_key;"`
	Name      string
	UserID    uuid.UUID
	ProfileID uuid.UUID
	Address   string
}

type user struct {
	ID      uuid.UUID `gorm:"type:uuid;primary_key;"`
	Name    string
	Devices []device
}
