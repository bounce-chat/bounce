package chat

import (
	"errors"
	stdlog "log"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

var referenceRequestMutex sync.Mutex

var referenceStateOffered = 0
var referenceStateRequested = 1

// TODO: might make sense to dynamically retry depending on size of offer
var referenceRetrySeconds = 5

type frameReference struct {
	ID         uuid.UUID `gorm:"type:uuid;primary_key;" msgpack:"-"`
	FrameID    uuid.UUID `gorm:"index;uniqueIndex:idx_frame_id_type_peer"`
	Type       uint16    `gorm:"index;uniqueIndex:idx_frame_id_type_peer"`
	Peer       string    `gorm:"index;uniqueIndex:idx_frame_id_type_peer" msgpack:"-"`
	State      int       `msgpack:"-"`
	LastAction int64     `msgpack:"-"`
	CreatedAt  int64     `msgpack:"-"`
}

func (fr *frameReference) BeforeCreate(tx *gorm.DB) error {
	if fr.ID != uuid.Nil {
		return errors.New("cannot create frame reference with already specified ID")
	}
	fr.ID = uuid.New()
	fr.CreatedAt = time.Now().Unix()
	return nil
}

func (b *bounce) openReferenceDatabase() {
	gormLogger := logger.New(
		stdlog.New(os.Stdout, "\r\n", stdlog.LstdFlags), // TODO: https://gist.github.com/bnadland/2e4287b801a47dcfcc94
		logger.Config{
			//SlowThreshold:             time.Second,
			LogLevel:                  logger.Error,
			IgnoreRecordNotFoundError: true,
		},
	)

	var err error
	b.referenceDatabase, err = gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{
		Logger: gormLogger,
	})
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error opening reference database")
	}

	sqliteDB, err := b.referenceDatabase.DB()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error getting underlying database interface from gorm while opening reference database")
	}
	sqliteDB.SetMaxOpenConns(1)

	err = b.referenceDatabase.AutoMigrate(
		&deliveryRecord{},
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
		fiveMinutesAgo := time.Now().Add(-5 * time.Minute).Unix()

		err := b.referenceDatabase.Where("created_at < ?", fiveMinutesAgo).Delete(&frameReference{}).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error pruning frame references")
		}

		err = b.referenceDatabase.Where("created_at < ?", fiveMinutesAgo).Delete(&deliveryRecord{}).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error pruning delivery records from reference database")
		}
		//b.pruningDatabase.Done()
	}

}

//
// only load things from the reference offer that we don't have, the ones
// that we do have we ack while handing the offer
//
func (b *bounce) loadReferenceOffer(peer string, ro []frameReference) {
	referenceRequestMutex.Lock()

	log.WithFields(log.Fields{
		"peer": peer,
		"len":  len(ro),
	}).Debug("reference engine learning about a reference offer")
	now := time.Now().Unix()

	for _, fr := range ro {
		fr.State = referenceStateOffered
		fr.Peer = peer
		fr.LastAction = now

		err := b.referenceDatabase.Clauses(clause.OnConflict{DoNothing: true}).Create(&fr).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error loading new reference offer into database")
		}
	}

	b.makeReferenceRequests()
	referenceRequestMutex.Unlock()

	go func() {
		// If the request we generated from this offer fails, check after the expiration to see if any other
		// devices have offered the same references or if we should try this peer again
		time.Sleep(time.Duration(referenceRetrySeconds+1) * time.Second)
		referenceRequestMutex.Lock()
		b.makeReferenceRequests()
		referenceRequestMutex.Unlock()
	}()
}

//
// Inform any devies that offered us a frame that we just handled that we no longer need that frame, and remove those references from the database
//
func (b *bounce) loadCatchUp(peer string, cu []frameReference) {
	log.WithFields(log.Fields{
		"peer": peer,
		"len":  len(cu),
	}).Debug("reference engine learning about a catch up")
	acks := map[string][]frameReference{}
	for _, fr := range cu {
		// Get all of the references for each frame in the catch up that are not the peer that sent the catch up
		references := []frameReference{}
		err := b.referenceDatabase.Where("id = ? AND type = ? AND peer != ?", fr.ID, fr.Type, peer).Find(&references).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error getting frame references from reference database")
		}

		// Ack these to any peer that offered it, or that didn't respond to our request
		for _, unneededReference := range references {
			acks[unneededReference.Peer] = append(acks[unneededReference.Peer], frameReference{ID: unneededReference.FrameID, Type: unneededReference.Type})
		}

		// Batch delete all reference frames in the database for this frame
		err = b.referenceDatabase.Where("frame_id = ? AND type = ?", fr.FrameID, fr.Type).Delete(&frameReference{}).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error deleting frame reference from reference database")
		}
	}

	for peer, refs := range acks {
		go b.sendDirect(peer, &ack{References: refs})
	}
}

func (b *bounce) makeReferenceRequests() {
	// Make sure we aren't handling a catch up while checking what we still need to request
	catchUpMutex.Lock()

	// Get a unique list of all the frames we need to learn about and a device we can get them from.  This includes all frames that have been offered to
	// us that we haven't requested yet, as well as alternative devices for frames that we have requested but have not received a response for in time
	references := []frameReference{}
	err := b.referenceDatabase.
		Where(
			"(state = ? AND frame_id NOT IN (?)) OR (state = ? AND last_action < ?)",
			referenceStateOffered,
			b.referenceDatabase.Select("frame_id").
				Where(
					"state = ? AND last_action > ?",
					referenceStateRequested,
					time.Now().Unix()-int64(referenceRetrySeconds),
				).
				Table("frame_references"),
			referenceStateRequested,
			time.Now().Unix()-int64(referenceRetrySeconds),
		).
		Find(&references).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error getting frame references from reference database")
	}
	catchUpMutex.Unlock()

	// Prepare reference requests for each device that is in the list of references to request
	referenceRequests := map[string][]frameReference{}
	for _, reference := range references {
		referenceRequests[reference.Peer] = append(referenceRequests[reference.Peer], reference)
	}
	for peer, frs := range referenceRequests {
		log.WithFields(log.Fields{
			"peer": peer,
			"len":  len(frs),
		}).Debug("making a reference request")
		// broadcast the reference request to the peer
		go b.sendDirect(peer, &referenceRequest{References: frs})
		for _, fr := range frs {
			// Update the references to indicate the new state and time we made these requests
			err = b.referenceDatabase.
				Table("frame_references").
				Where("frame_id = ? AND type = ? AND peer = ?", fr.FrameID, fr.Type, fr.Peer).
				Updates(map[string]interface{}{
					"state":       referenceStateRequested,
					"last_action": time.Now().Unix(),
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
		typeReferenceOffer: []uuid.UUID{},
		typeDirectMessage:  []uuid.UUID{},
		typeGroupMessage:   []uuid.UUID{},
		typeUpdateDM:       []uuid.UUID{},
		typeDevice:         []uuid.UUID{},
		typeAddUser:        []uuid.UUID{},
		typeGroupCreation:  []uuid.UUID{},
		typeUpdateGroup:    []uuid.UUID{},
		typeConfirmation:   []uuid.UUID{},
		typeUpdateUser:     []uuid.UUID{},
		typeUpdateDevice:   []uuid.UUID{},
		typeReadReceipt:    []uuid.UUID{},
		typeUpdateSettings: []uuid.UUID{},
	}

	for _, reference := range references {
		ids[reference.Type] = append(ids[reference.Type], reference.FrameID)
	}

	return ids
}
