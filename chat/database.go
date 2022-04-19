package chat

import (
	"os"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"

	stdlog "log"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

func (b *bounce) openDatabase() {
	databaseFile := b.configDirectory + "/bounce.db" //TODO: if needed: ?_busy_timeout=5000

	gormLogger := logger.New(
		stdlog.New(os.Stdout, "\r\n", stdlog.LstdFlags), // TODO: https://gist.github.com/bnadland/2e4287b801a47dcfcc94
		logger.Config{
			//SlowThreshold:             time.Second,
			LogLevel:                  logger.Error,
			IgnoreRecordNotFoundError: true,
		},
	)

	var err error
	b.database, err = gorm.Open(sqlite.Open(databaseFile), &gorm.Config{
		Logger: gormLogger,
	})
	if err != nil {
		log.WithFields(log.Fields{
			"file":  databaseFile,
			"error": err.Error(),
		}).Fatal("error opening database")
	}
	// To prevent database is locked errors
	// TODO: is this the correct approach?
	//b.database.Exec("PRAGMA journal_mode=WAL;")
	sqliteDB, err := b.database.DB()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error getting underlying database interface from gorm while opening database")
	}
	sqliteDB.SetMaxOpenConns(1)

	err = b.database.AutoMigrate(
		&user{},
		&device{},
		&profileExport{},
		&introductionSignature{},
		&DirectMessage{}, // TODO: still need to decide if we'll export a simplified one for the UI
		&referenceOffer{},
		&updateLocalDMSettings{},
		&syncDeviceOffer{},
		&deliveryRecord{},
		&updateDMSettings{},
		&group{},
		&GroupMessage{},
	)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error migrating the database")
	}

	b.pruneDatabase(false)
	go b.keepDatabasePruned()
}

func (b *bounce) keepDatabasePruned() {
	b.databasePruningTicker = time.NewTicker(10 * time.Second) // TODO: can I get away with this frequency without resource costs?

	for _ = range b.databasePruningTicker.C {
		b.pruningDatabase.Add(1)
		b.pruneDatabase(true)
		b.pruningDatabase.Done()
	}
}

func (b *bounce) pruneDatabase(informUI bool) {
	b.pruneReferenceOffers()
	b.pruneDirectMessages(informUI)
	//b.pruneGroupMessages()
	b.pruneSyncDeviceOffers()
	b.pruneDeliveryRecords()
}

// If a reference offer was delivered, but a reference request was never received in response, it will only be deleted here
func (b *bounce) pruneReferenceOffers() {
	tenMinutesAgo := time.Now().Add(-10 * time.Minute).Unix()
	err := b.database.Where("created_at < ?", tenMinutesAgo).Delete(referenceOffer{}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error pruning reference offers")
	}
}

func (b *bounce) pruneDirectMessages(informUI bool) {
	now := time.Now().Unix()

	if informUI {
		// Find messages that should be pruned and delete them from the UI
		var dms []DirectMessage
		err := b.database.Select("id").Where("delete_at != 0 AND delete_at < ?", now).Find(&dms).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error selecting direct messages that are past retention for pruning")
		}
		for _, dm := range dms {
			b.userInterface.DeleteMessage(dm.ID)
		}
	}

	// Delete those messages from the database
	err := b.database.Where("delete_at != 0 AND delete_at < ?", now).Delete(&DirectMessage{}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error batch deleting direct messages past retention")
	}

	// Find all messages that are undeliverable and inform the UI, marking them for deletion if they don't have indefinite retention
	var dms []DirectMessage
	err = b.database.
		Select("direct_messages.id", "direct_messages.retention_seconds").
		Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == direct_messages.id AND delivery_records.frame_type == ?", typeDirectMessage).
		Where(
			"delivery_records.id IS NULL AND direct_messages.written_at < ? AND undeliverable = false",
			time.Now().Add(-undeliverableAfter).Unix(),
		).
		Find(&dms).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error selecting message that are undeliverable while pruning database")
	}
	for _, dm := range dms {
		err = b.database.Model(&dm).Update("undeliverable", true).Error
		if err != nil {
			log.WithFields(log.Fields{
				"message_id": dm.ID,
				"error":      err.Error(),
			}).Fatal("error updating undeliverable field of undeliverable direct message")
		}
		if informUI {
			b.userInterface.MarkMessageUndeliverable(dm.ID)
		}
		if dm.RetentionSeconds > 0 {
			deleteAt := time.Now().Unix() + dm.RetentionSeconds
			err = b.database.Model(&dm).Update("delete_at", deleteAt).Error
			if err != nil {
				log.WithFields(log.Fields{
					"message_id": dm.ID,
					"error":      err.Error(),
				}).Fatal("error updating delete_at of undeliverable direct message with retention")
			}
			if informUI {
				b.userInterface.UpdateMessageDeletionTime(dm.ID, deleteAt)
			}
		}
	}
}

func (b *bounce) pruneSyncDeviceOffers() {
	fiveMinutesAgo := time.Now().Add(-5 * time.Minute).Unix()

	err := b.database.Where("timestamp < ?", fiveMinutesAgo).Delete(syncDeviceOffer{}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error pruning old sync device offers")
	}
}

func (b *bounce) pruneDeliveryRecords() {
	// Some objects are not meant to persist to the database, or at least not meant
	// to persist long.  These objects cannot prune delivery records when they delete
	// since they are never stored in the database.  To prevent leaks, we prune them
	// here well after they are needed.
	aDayAgo := time.Now().Add(-24 * time.Hour).Unix()
	err := b.database.Where(
		"created_at < ? AND frame_type IN (?, ?)",
		aDayAgo,
		typeReferenceOffer,
		typeCatchUp,
	).Delete(&deliveryRecord{}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error pruning delivery records")
	}
}

func (b *bounce) buildInitialState() InitialState {
	var profile *User
	var count int64
	b.database.Model(&user{}).Where("profile = ?", true).Count(&count)
	if count > 0 {
		var dbProfile user
		b.database.Where("profile = ?", true).First(&dbProfile) // TODO: error check, clean this up
		profile = &User{
			ID:   dbProfile.ID,
			Name: dbProfile.Name,
		}
	}

	users := []user{}
	b.database.Find(&users) // TODO: exclude current profile?  Self DMs are actually useful for sending data between devices, saving things
	chatUsers := []User{}
	for _, u := range users {
		chatUsers = append(chatUsers, User{
			ID:   u.ID,
			Name: u.Name,
		})
	}

	groups := []group{}
	b.database.Preload(clause.Associations).Find(&groups)
	chatGroups := []Group{}
	for _, g := range groups {
		userList := []uuid.UUID{}
		for _, u := range g.Users {
			userList = append(userList, u.ID)
		}
		chatGroups = append(chatGroups, Group{
			ID:      g.ID,
			Name:    g.Name,
			UserIDs: userList,
		})
	}

	dms := []DirectMessage{}
	b.database.Order("saved_at asc").Find(&dms) // TODO: error check
	gms := []GroupMessage{}
	b.database.Order("saved_at asc").Find(&gms) // TODO: error check

	return InitialState{
		Profile:        profile,
		Users:          chatUsers,
		Groups:         chatGroups,
		DirectMessages: dms,
		GroupMessages:  gms,
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
	if profileExport.ID != uuid.Nil {
		log.Fatal("unexpected profileExport primary key assigned before create")
	}
	profileExport.ID = uuid.New()
	return nil
}
