package chat

import (
	"errors"
	stdlog "log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

var referenceRequestMutex sync.Mutex

var referenceStateOffered = 0
var referenceStateRequested = 1
var referenceStateRefused = 2 // the peer served it and we would not take it; do not ask them for it again

// TODO: might make sense to dynamically retry depending on size of offer
// How long to wait for an ack before re-sending a reference offer or request, or treating a request as failed.
// Atomic because tests shorten it while engine goroutines started by earlier tests are still reading it.
var referenceRetrySeconds = func() *atomic.Int64 {
	seconds := &atomic.Int64{}
	seconds.Store(30)
	return seconds
}()

func referenceRetryInterval() time.Duration {
	return time.Duration(referenceRetrySeconds.Load()) * time.Second
}

type frameReference struct {
	ID         ID     `gorm:"primary_key;" msgpack:"-"`
	FrameID    ID     `gorm:"index;uniqueIndex:idx_frame_id_type_peer"`
	Type       uint16 `gorm:"index;uniqueIndex:idx_frame_id_type_peer"`
	Peer       string `gorm:"index;uniqueIndex:idx_frame_id_type_peer" msgpack:"-"`
	State      int    `msgpack:"-"`
	LastAction int64  `msgpack:"-"`
	CreatedAt  int64  `msgpack:"-"`
}

func (fr *frameReference) BeforeCreate(tx *gorm.DB) error {
	if fr.ID != NilID {
		return errors.New("cannot create frame reference with already specified ID")
	}
	fr.ID = NewID()
	fr.CreatedAt = time.Now().Unix()
	return nil
}

func (b *Bounce) openReferenceDatabase() {
	gormLogger := logger.New(
		stdlog.New(os.Stdout, "\r\n", stdlog.LstdFlags),
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

	b.background(b.keepReferenceDatabasePruned)
}

func (b *Bounce) keepReferenceDatabasePruned() {
	databasePruningTicker := time.NewTicker(300 * time.Second)
	defer databasePruningTicker.Stop()

	for {
		select {
		case <-b.done:
			return
		case <-databasePruningTicker.C:
		}
		b.pruneReferences()
	}
}

// pruneReferences forgets what departed peers offered us.  There is no time limit: a peer that is still connected may
// be mid way through a sync of any size, and its references live until the frames arrive or its sockets go.  The
// reference offer delivery records are only there to dedupe acks, so those are cleared after a few minutes.
func (b *Bounce) pruneReferences() {
	var peers []string
	err := b.referenceDatabase.Model(&frameReference{}).Distinct("peer").Pluck("peer", &peers).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error listing peers with frame references")
	}
	for _, peer := range peers {
		// Every connected peer has a pool entry, whether or not we know whose device it is; this is a peek rather than
		// getRemoteDevice so that a departed peer does not get an empty entry made for it
		b.devicePool.deviceMutex.Lock()
		rd, inPool := b.devicePool.devices[peer]
		b.devicePool.deviceMutex.Unlock()
		if inPool && rd.connectedSockets() > 0 {
			continue
		}

		err := b.referenceDatabase.Where("peer = ?", peer).Delete(&frameReference{}).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error pruning frame references of a departed peer")
		}
		setIncompleteOfferFrom(peer, false)
	}

	fiveMinutesAgo := time.Now().Add(-5 * time.Minute).Unix()
	err = b.referenceDatabase.Where("created_at < ?", fiveMinutesAgo).Delete(&deliveryRecord{}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error pruning delivery records from reference database")
	}
}

// only load things from the reference offer that we don't have, the ones
// that we do have we ack while handling the offer
func (b *Bounce) loadReferenceOffer(peer string, ro []frameReference) {
	referenceRequestMutex.Lock()

	log.WithFields(log.Fields{
		"peer": peer,
		"len":  len(ro),
	}).Debug("reference engine learning about a reference offer")
	now := time.Now().Unix()

	// Every peer is allowed one offer's worth of references at a time, and the engine as a whole a fixed number, so
	// that a peer naming frames it never serves cannot grow this table without bound.  A frame offered again only
	// has its timestamp refreshed: a request already in flight is not made twice.
	var fromPeer, total int64
	b.referenceDatabase.Model(&frameReference{}).Where("peer = ?", peer).Count(&fromPeer)
	b.referenceDatabase.Model(&frameReference{}).Count(&total)
	room := min(int64(maximumReferencesPerOffer)-fromPeer, int64(maximumReferences)-total)
	refresh := clause.OnConflict{
		Columns:   []clause.Column{{Name: "frame_id"}, {Name: "type"}, {Name: "peer"}},
		DoUpdates: clause.Assignments(map[string]interface{}{"last_action": now}),
	}
	dropped := 0
	for _, fr := range ro {
		fr.State = referenceStateOffered
		fr.Peer = peer
		fr.LastAction = now

		if room < int64(len(ro)) {
			// Near the allowance, so tell a refresh from a new reference and count only the new ones
			refreshed := b.referenceDatabase.Model(&frameReference{}).
				Where("frame_id = ? AND type = ? AND peer = ?", fr.FrameID, fr.Type, peer).
				Update("last_action", now)
			if refreshed.Error != nil {
				log.WithFields(log.Fields{
					"error": refreshed.Error.Error(),
				}).Fatal("error refreshing reference from offer")
			}
			if refreshed.RowsAffected > 0 {
				continue
			}
			if room <= 0 {
				dropped++
				continue
			}
			room--
		}

		err := b.referenceDatabase.Clauses(refresh).Create(&fr).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error loading new reference offer into database")
		}
	}
	if dropped > 0 {
		log.WithFields(log.Fields{
			"peer":    peer,
			"dropped": dropped,
		}).Warn("peer offered more references than it may have outstanding, ignoring the rest")
	}

	b.makeReferenceRequests()
	referenceRequestMutex.Unlock()

	go func() {
		// If the request we generated from this offer fails, check after the expiration to see if any other
		// devices have offered the same references or if we should try this peer again
		time.Sleep(referenceRetryInterval() + time.Second)
		referenceRequestMutex.Lock()
		b.makeReferenceRequests()
		referenceRequestMutex.Unlock()
	}()
}

// Inform any devices that offered us a frame that we just handled that we no longer need that frame, and remove those references from the database
func (b *Bounce) loadCatchUp(peer string, cu []frameReference) {
	log.WithFields(log.Fields{
		"peer": peer,
		"len":  len(cu),
	}).Debug("reference engine learning about a catch up")
	acks := map[string][]frameReference{}
	for _, fr := range cu {
		// Get all of the references for each frame in the catch up that are not the peer that sent the catch up
		references := []frameReference{}
		err := b.referenceDatabase.Where("frame_id = ? AND type = ? AND peer != ?", fr.FrameID, fr.Type, peer).Find(&references).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error getting frame references from reference database")
		}

		// Ack these to any peer that offered it, or that didn't respond to our request
		for _, unneededReference := range references {
			acks[unneededReference.Peer] = append(acks[unneededReference.Peer], frameReference{FrameID: unneededReference.FrameID, Type: unneededReference.Type})
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

// requestOutstandingReferencesFrom is called when an incomplete catchup is finished, and is used to request the
// next batch of data from the peer.
func (b *Bounce) requestOutstandingReferencesFrom(peer string) {
	referenceRequestMutex.Lock()
	defer referenceRequestMutex.Unlock()

	var outstanding []frameReference
	err := b.referenceDatabase.Where("peer = ? AND state != ?", peer, referenceStateRefused).Find(&outstanding).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error getting outstanding frame references from reference database")
	}
	if len(outstanding) == 0 {
		return
	}

	log.WithFields(log.Fields{
		"peer": peer,
		"len":  len(outstanding),
	}).Debug("requesting the next stage of an incomplete catch up")
	go b.sendDirect(peer, &referenceRequest{References: outstanding})
	err = b.referenceDatabase.
		Table("frame_references").
		Where("peer = ? AND state != ?", peer, referenceStateRefused).
		Updates(map[string]interface{}{
			"state":       referenceStateRequested,
			"last_action": time.Now().Unix(),
		}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error updating frame references after requesting the next stage")
	}
}

// outstandingReferencesFrom counts the frames a peer has offered that we still want from them
func (b *Bounce) outstandingReferencesFrom(peer string) int64 {
	var outstanding int64
	err := b.referenceDatabase.Model(&frameReference{}).Where("peer = ? AND state != ?", peer, referenceStateRefused).Count(&outstanding).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error counting outstanding frame references")
	}
	return outstanding
}

// refuseReference records that a peer served us a frame we would not take, so that we never ask them for it again.
// It is not acked: a client that learns to take the frame later can still be offered it by the peer.
func (b *Bounce) refuseReference(peer string, frameID ID, frameType uint16) {
	err := b.referenceDatabase.
		Table("frame_references").
		Where("frame_id = ? AND type = ? AND peer = ?", frameID, frameType, peer).
		Updates(map[string]interface{}{
			"state":       referenceStateRefused,
			"last_action": time.Now().Unix(),
		}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marking a frame reference refused")
	}
}

func (b *Bounce) makeReferenceRequests() {
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
					time.Now().Unix()-referenceRetrySeconds.Load(),
				).
				Table("frame_references"),
			referenceStateRequested,
			time.Now().Unix()-referenceRetrySeconds.Load(),
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

func referencedIDs(references []frameReference) map[uint16][]ID {
	ids := make(map[uint16][]ID, len(referencedTypes))
	for _, frameType := range referencedTypes {
		ids[frameType] = []ID{}
	}

	for _, reference := range references {
		ids[reference.Type] = append(ids[reference.Type], reference.FrameID)
	}

	return ids
}
