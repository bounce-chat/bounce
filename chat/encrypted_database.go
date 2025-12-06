package chat

import (
	stdlog "log"
	"os"

	log "github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func (b *Bounce) openEncryptedDatabase() {
	databaseFile := b.configDirectory + "/bounce.enc.db"

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
		TranslateError: true,
		Logger:         gormLogger,
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
		&authorizedUser{},
		//&encryptedFrame{},
		//&recipient{},
	)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error migrating the database")
	}
}
