package chat

import (
	"errors"
	stdlog "log"
	"os"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

var referenceStateOffered = 0
var referenceStateRequested = 1

var referenceRetrySeconds = 5

type frameReference struct {
	ID      uuid.UUID `gorm:"type:uuid;primary_key;" msgpack:"-"`
	FrameID uuid.UUID `gorm:"index;uniqueIndex:idx_frame_id_type_peer"`
	Type    uint16    `gorm:"index;uniqueIndex:idx_frame_id_type_peer"`
	Peer    string    `gorm:"index;uniqueIndex:idx_frame_id_type_peer" msgpack:"-"`
	State   int       `msgpack:"-"`
	Time    int64     `msgpack:"-"`
}

type referenceEngine struct {
	database *gorm.DB
}

func (b *bounce) createReferenceEngine() {
	b.referenceEngine = &referenceEngine{}

	gormLogger := logger.New(
		stdlog.New(os.Stdout, "\r\n", stdlog.LstdFlags), // TODO: https://gist.github.com/bnadland/2e4287b801a47dcfcc94
		logger.Config{
			//SlowThreshold:             time.Second,
			LogLevel:                  logger.Error,
			IgnoreRecordNotFoundError: true,
		},
	)

	var err error
	b.referenceEngine.database, err = gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		Logger: gormLogger,
	})
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error opening reference database")
	}

	sqliteDB, err := b.referenceEngine.database.DB()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error getting underlying database interface from gorm while opening reference database")
	}
	sqliteDB.SetMaxOpenConns(1)

	err = b.referenceEngine.database.AutoMigrate(
		&referenceOffer{},
		&frameReference{},
	)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error migrating the reference database")
	}

	go b.keepReferenceDatabasePruned()
}

func (b *bounce) keepReferenceDatabasePruned() {
	databasePruningTicker := time.NewTicker(300 * time.Second) // TODO: properly shut this down at shutdown?

	for _ = range databasePruningTicker.C {
		//b.pruningDatabase.Add(1)
		b.pruneReferenceOffers()
		//b.pruningDatabase.Done()
	}

}

// If a reference offer was delivered, but a reference request was never received in response, it will only be deleted here
func (b *bounce) pruneReferenceOffers() {
	tenMinutesAgo := time.Now().Add(-10 * time.Minute).Unix()
	err := b.referenceEngine.database.Where("created_at < ?", tenMinutesAgo).Delete(referenceOffer{}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error pruning reference offers")
	}
}

//
// only load things from the reference offer that we don't have, the ones
// that we do have we ack while handing the offer
//
func (b *bounce) loadReferenceOffer(peer string, ro []frameReference) {
	now := time.Now().Unix()

	for _, fr := range ro {
		fr.State = referenceStateOffered
		fr.Peer = peer
		fr.Time = now

		err := b.referenceEngine.database.Clauses(clause.OnConflict{DoNothing: true}).Create(&fr).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error loading new reference offer into database")
		}
	}

	b.makeReferenceRequests()
}

//
// tell everyone we have these now (except the device that sent them, they will be ack'd by the handlers).  delete all records for it.
// TODO: if someone sends us a catch up with malformed payloads, we'll ack them to the other devices that might have the correct payloads
// if we do this.  ok?  Alternatively we could continuously make offers, but that's more expensive
// could also have this function check to make sure things were actually saved and only ack in that case, or have the catch up handler confirm
// the save and then only pass the ones that saved to this function
//
func (b *bounce) loadCatchUp(peer string, cu []frameReference) { // TODO: loadSuccessfulCatchUps?
	for _, fr := range cu {
		// Get all of the references for each frame in the catch up that are not the peer that sent the catch up
		references := []frameReference{}
		err := b.referenceEngine.database.Where("id = ? AND type = ? AND peer != ?", fr.ID, fr.Type, peer).Find(&references).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error getting frame references from reference database")
		}

		// Ack these to any peer that offered it, or that didn't respond to our request
		for _, unneededReference := range references {
			b.sendDirectAck(unneededReference.Peer, unneededReference)
		}

		// Batch delete all reference frames in the database for this frame
		err = b.referenceEngine.database.Where("frame_id = ? AND type = ?", fr.FrameID, fr.Type).Delete(&frameReference{}).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error deleting frame reference from reference database")
		}
	}
}

func (b *bounce) makeReferenceRequests() {
	// Get a unique list of all the frames we need to learn about and a device we can get them from.  This includes all frames that have been offered to
	// us that we haven't requested yet, as well as alternative devices for frames that we have requested but have not received a response for in time
	references := []frameReference{}
	err := b.referenceEngine.database.Model(&frameReference{}).
		Distinct("frame_id", "type").
		Where(
			"state = ? AND frame_id NOT IN (?)",
			referenceStateOffered,
			b.referenceEngine.database.Select("frame_id").
				Where(
					"state = ? AND time > ?",
					referenceStateRequested,
					time.Now().Unix()-int64(referenceRetrySeconds),
				).
				Table("frame_references"),
		).
		Find(&references).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// We have nothing to ask for right now
			return
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error getting frame references from reference database")
		}
	}

	// Prepare reference requests for each device that is in the list of references to request
	referenceRequests := map[string][]frameReference{}
	for _, reference := range references {
		referenceRequests[reference.Peer] = append(referenceRequests[reference.Peer], frameReference{FrameID: reference.FrameID, Type: reference.Type})
	}
	for peer, frs := range referenceRequests {
		// broadcast the reference request to the peer
		rd := b.getRemoteDevice(peer)
		rd.messages <- &referenceRequest{
			References: frs,
		}
		for _, fr := range frs {
			// Update the references to indicate the new state and time we made these requests
			err = b.referenceEngine.database.
				Table("frame_references").
				Where("frame_id = ? AND type = ? AND peer = ?", fr.FrameID, fr.Type, fr.Peer).
				Updates(map[string]interface{}{
					"state": referenceStateRequested,
					"time":  time.Now().Unix(),
				}).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("error updating frame reference after making reference request")
			}
		}
	}
}

// Extract all the IDs in the references by type
func referencedIDs(references []frameReference) map[uint16][]uuid.UUID {
	ids := map[uint16][]uuid.UUID{
		typeDirectMessage: []uuid.UUID{},
		typeGroupMessage:  []uuid.UUID{},
		typeUpdateDM:      []uuid.UUID{},
		typeDevice:        []uuid.UUID{},
		typeAddUser:       []uuid.UUID{},
		typeGroupCreation: []uuid.UUID{},
		typeUpdateGroup:   []uuid.UUID{},
		typeCatchUp:       []uuid.UUID{}, // TODO: needed?
	}

	for _, reference := range references {
		ids[reference.Type] = append(ids[reference.Type], reference.FrameID)
	}

	return ids
}
