package chat

import (
	"errors"
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
	databaseFile := b.configDirectory + "/bounce.db"

	// Define a logger for gorm that uses logrus
	gormLogger := logger.New(
		stdlog.New(os.Stdout, "\r\n", stdlog.LstdFlags), // TODO: https://gist.github.com/bnadland/2e4287b801a47dcfcc94
		logger.Config{
			LogLevel:                  logger.Error,
			IgnoreRecordNotFoundError: true,
		},
	)

	// Open the database
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

	// Set max connections to 1 to avoid locks
	sqliteDB, err := b.database.DB()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error getting underlying database interface from gorm while opening database")
	}
	sqliteDB.SetMaxOpenConns(1)

	// Migrate
	err = b.database.AutoMigrate(
		&user{},
		&device{},
		&profileExport{},
		&introductionSignature{},
		&directMessage{},
		&syncDeviceOffer{},
		&deliveryRecord{},
		&updateDM{},
		&groupCreation{},
		&group{},
		&groupMessage{},
		&updateGroup{},
		&addUser{},
		&addUserOffer{},
	)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error migrating the database")
	}

	// Prune and kick off pruning loop
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
	b.pruneDirectMessages(informUI)
	b.pruneGroupMessages(informUI)
}

func (b *bounce) pruneDirectMessages(informUI bool) {
	now := time.Now().Unix()

	if informUI {
		// Find messages that should be pruned and delete them from the UI
		var dms []directMessage
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
	err := b.database.Where("delete_at != 0 AND delete_at < ?", now).Delete(&directMessage{}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error batch deleting direct messages past retention")
	}

	// Find all messages that are undeliverable and inform the UI, marking them for deletion if they don't have indefinite retention
	var dms []directMessage
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

func (b *bounce) pruneGroupMessages(informUI bool) {
	now := time.Now().Unix()

	if informUI {
		// Find messages that should be pruned and delete them from the UI
		var gms []groupMessage
		err := b.database.Select("id").Where("delete_at != 0 AND delete_at < ?", now).Find(&gms).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error selecting group messages that are past retention for pruning")
		}
		for _, gm := range gms {
			b.userInterface.DeleteMessage(gm.ID)
		}
	}

	// Delete those messages from the database
	err := b.database.Where("delete_at != 0 AND delete_at < ?", now).Delete(&groupMessage{}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error batch deleting group messages past retention")
	}

	// Find all messages that are undeliverable and inform the UI, marking them for deletion if they don't have indefinite retention
	var gms []groupMessage
	err = b.database.
		Select("group_messages.id", "group_messages.retention_seconds").
		Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == group_messages.id AND delivery_records.frame_type == ?", typeGroupMessage).
		Where(
			"delivery_records.id IS NULL AND group_messages.written_at < ? AND undeliverable = false",
			time.Now().Add(-undeliverableAfter).Unix(),
		).
		Find(&gms).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error selecting group message that are undeliverable while pruning database")
	}
	for _, gm := range gms {
		err = b.database.Model(&gm).Update("undeliverable", true).Error
		if err != nil {
			log.WithFields(log.Fields{
				"message_id": gm.ID,
				"error":      err.Error(),
			}).Fatal("error updating undeliverable field of undeliverable group message")
		}
		if informUI {
			b.userInterface.MarkMessageUndeliverable(gm.ID)
		}
		if gm.RetentionSeconds > 0 {
			deleteAt := time.Now().Unix() + gm.RetentionSeconds
			err = b.database.Model(&gm).Update("delete_at", deleteAt).Error
			if err != nil {
				log.WithFields(log.Fields{
					"message_id": gm.ID,
					"error":      err.Error(),
				}).Fatal("error updating delete_at of undeliverable group message with retention")
			}
			if informUI {
				b.userInterface.UpdateMessageDeletionTime(gm.ID, deleteAt)
			}
		}
	}
}

func (b *bounce) buildInitialState() InitialState {
	// Load the profile
	var profile *User
	var dbProfile user
	err := b.database.Where("profile = ?", true).First(&dbProfile).Error
	if err == nil {
		profile = &User{
			ID:   dbProfile.ID,
			Name: dbProfile.Name,
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up profile user")
	}

	// Load all users
	users := []user{}
	err = b.database.Find(&users).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up all users")
	}
	chatUsers := []User{}
	for _, u := range users {
		chatUsers = append(chatUsers, User{
			ID:   u.ID,
			Name: u.Name,
		})
	}

	// Load all groups
	groups := []group{}
	err = b.database.Preload(clause.Associations).Find(&groups).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up all groups")
	}
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

	// Load all direct messages
	dms := []directMessage{}
	err = b.database.Order("saved_at asc").Find(&dms).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up all direct messages")
	}
	exportedDMs := []DirectMessage{}
	for _, dm := range dms {
		exportedDMs = append(
			exportedDMs,
			DirectMessage{
				ID:        dm.ID,
				Author:    dm.Author,
				Thread:    dm.getDestination(b.currentUserID()),
				WrittenAt: dm.WrittenAt,
				Text:      dm.Text,
			},
		)
	}

	// Load all group messages
	gms := []groupMessage{}
	err = b.database.Order("saved_at asc").Find(&gms).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up all group messages")
	}
	exportedGMs := []GroupMessage{}
	for _, gm := range gms {
		exportedGMs = append(
			exportedGMs,
			GroupMessage{
				ID:        gm.ID,
				Author:    gm.Author,
				Thread:    gm.getDestination(b.currentUserID()),
				WrittenAt: gm.WrittenAt,
				Text:      gm.Text,
			},
		)
	}

	// Create the initial state for the UI
	return InitialState{
		Profile:        profile,
		Users:          chatUsers,
		Groups:         chatGroups,
		DirectMessages: exportedDMs,
		GroupMessages:  exportedGMs,
	}
}
