package chat

import (
	"os"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	stdlog "log"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

func (bounce *Bounce) openDatabase() {
	databaseFile := bounce.configDirectory + "/bounce.db"

	gormLogger := logger.New(
		stdlog.New(os.Stdout, "\r\n", stdlog.LstdFlags), // TODO: https://gist.github.com/bnadland/2e4287b801a47dcfcc94
		logger.Config{
			//SlowThreshold:             time.Second,
			LogLevel:                  logger.Error,
			IgnoreRecordNotFoundError: true,
		},
	)

	var err error
	bounce.database, err = gorm.Open(sqlite.Open(databaseFile), &gorm.Config{
		Logger: gormLogger,
	})
	if err != nil {
		log.WithFields(log.Fields{
			"file":  databaseFile,
			"error": err.Error(),
		}).Fatal("error opening database")
	}

	err = bounce.database.AutoMigrate(
		&user{},
		&device{},
		&profileExport{},
		&introductionSignature{},
		&DirectMessage{}, // TODO: still need to decide if we'll export a simplified one for the UI
		&referenceOffer{},
	)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error migrating the database")
	}

	bounce.pruneDatabase(false)
	go bounce.keepDatabasePruned()
}

func (bounce *Bounce) keepDatabasePruned() {
	ticker := time.NewTicker(10 * time.Second) // TODO: can I get away with this frequency without resource costs?

	for _ = range ticker.C {
		bounce.pruneDatabase(true)
	}
}

func (bounce *Bounce) pruneDatabase(informUI bool) {
	bounce.pruneReferenceOffers()
	bounce.pruneDirectMessages(informUI)
	//bounce.pruneGroupMessages()
}

// If a reference offer was delivered, but a reference request was never received in response, it will only be deleted here
func (bounce *Bounce) pruneReferenceOffers() {
	tenMinutesAgo := time.Now().Add(-10 * time.Minute).Unix()
	err := bounce.database.Where("created_at < ?", tenMinutesAgo).Delete(referenceOffer{}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error pruning reference offers")
	}
}

func (bounce *Bounce) pruneDirectMessages(informUI bool) {
	now := time.Now().Unix()

	if informUI {
		// Find messages that should be pruned and delete them from the UI
		var dms []DirectMessage
		err := bounce.database.Select("id").Where("delete_at != 0 AND delete_at < ?", now).Find(&dms).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error selecting direct messages that are past retention for pruning")
		}
		for _, dm := range dms {
			bounce.userInterface.ExpireMessage(dm.ID)
		}
	}

	// Delete those messages from the database
	err := bounce.database.Where("delete_at != 0 AND delete_at < ?", now).Delete(DirectMessage{}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error batch deleting direct messages past retention")
	}
}

func (bounce *Bounce) buildInitialState() InitialState {
	var profile *User
	var count int64
	bounce.database.Model(&user{}).Where("profile = ?", true).Count(&count)
	if count > 0 {
		var dbProfile user
		bounce.database.Where("profile = ?", true).First(&dbProfile) // TODO: error check, clean this up
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

	dms := []DirectMessage{}
	bounce.database.Order("received_at asc").Find(&dms) // TODO: error check

	return InitialState{
		Profile:        profile,
		Users:          chatUsers,
		DirectMessages: dms,
	}
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
