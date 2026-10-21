package chat

import (
	"os"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"gorm.io/gorm/clause"
)

// Frames can be left on an encrypted device by any peer that names the owner as a recipient.  That is how the
// devices of contacts this encrypted device has never met deliver to the owner, so it has to stay open, but it also
// means anyone who knows the owner's public key can write here.  Frames from peers that have not proven they are
// the owner are the untrusted tier, and share a storage budget so that a flood cannot fill the disk.  When the
// budget is full the oldest untrusted frames are evicted, but never ones younger than
// untrustedEncryptedFrameEvictableAfter, so a flood cannot push out a frame a contact left recently: once the budget
// is full of recent frames, new untrusted frames are refused and their senders will offer them again later.  Frames
// from the owner's own devices are not counted against this budget, as with chunk storage.
//
// These are vars rather than consts so tests can shrink them; nothing else reassigns them.
var untrustedEncryptedFrameStorageLimit = int64(1024 * 1024 * 1024) // 1GiB
var untrustedEncryptedFrameCountLimit = int64(1000000)
var untrustedEncryptedFramePayloadLimit = int64(1024 * 1024) // 1MiB, a sealed message or update is far smaller
var untrustedEncryptedFrameEvictableAfter = 7 * 24 * time.Hour

const untrustedEncryptedFrameEvictionBatch = 100

// The untrusted tier's size is tracked in memory so that storing a frame does not need a query.  It is loaded from
// the database on startup and again after each pruning pass, which corrects any drift from deletes that do not
// adjust it.
var untrustedEncryptedFrameStorageSize = int64(0)
var untrustedEncryptedFrameCount = int64(0)
var untrustedEncryptedFrameStorageMutex sync.Mutex

func (b *Bounce) untrustedEncryptedFrameStorage() (int64, int64) {
	untrustedEncryptedFrameStorageMutex.Lock()
	defer untrustedEncryptedFrameStorageMutex.Unlock()

	return untrustedEncryptedFrameStorageSize, untrustedEncryptedFrameCount
}

func (b *Bounce) adjustUntrustedEncryptedFrameStorage(sizeDelta int64, countDelta int64) {
	untrustedEncryptedFrameStorageMutex.Lock()
	defer untrustedEncryptedFrameStorageMutex.Unlock()

	untrustedEncryptedFrameStorageSize += sizeDelta
	if untrustedEncryptedFrameStorageSize < 0 {
		untrustedEncryptedFrameStorageSize = 0
	}
	untrustedEncryptedFrameCount += countDelta
	if untrustedEncryptedFrameCount < 0 {
		untrustedEncryptedFrameCount = 0
	}
}

// recheckUntrustedEncryptedFrameStorage loads the size of the untrusted tier from the database with a single
// aggregate query
func (b *Bounce) recheckUntrustedEncryptedFrameStorage() {
	var result struct {
		Size  int64
		Count int64
	}
	err := b.database.
		Model(&encryptedFrame{}).
		Select("COALESCE(SUM(LENGTH(payload)), 0) AS size, COUNT(*) AS count").
		Where("from_owner = ?", false).
		Scan(&result).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error checking untrusted encrypted frame storage size")
		return
	}

	untrustedEncryptedFrameStorageMutex.Lock()
	defer untrustedEncryptedFrameStorageMutex.Unlock()

	untrustedEncryptedFrameStorageSize = result.Size
	untrustedEncryptedFrameCount = result.Count
}

// makeRoomForUntrustedEncryptedFrame reports whether an untrusted frame may be stored, evicting old untrusted frames
// to make room for it if needed
func (b *Bounce) makeRoomForUntrustedEncryptedFrame(ef *encryptedFrame, peer string) bool {
	payloadSize := int64(len(ef.Payload))
	if payloadSize > untrustedEncryptedFramePayloadLimit {
		log.WithFields(log.Fields{
			"id":   ef.ID,
			"peer": peer,
			"size": payloadSize,
		}).Warn("refusing untrusted encrypted frame that is larger than any legitimate frame")
		return false
	}

	if os.Getenv("UNLIMITED_STORAGE") == "true" {
		return true
	}

	evictableBefore := time.Now().Add(-untrustedEncryptedFrameEvictableAfter).Unix()
	for {
		size, count := b.untrustedEncryptedFrameStorage()
		if size+payloadSize <= untrustedEncryptedFrameStorageLimit && count+1 <= untrustedEncryptedFrameCountLimit {
			return true
		}

		_, evicted := b.evictUntrustedEncryptedFramesSavedBefore(evictableBefore)
		if evicted == 0 {
			// Everything left in the untrusted tier is recent.  Refuse this frame rather than push out something a
			// contact left recently; the peer will offer it again later.  Debug, because a flood of refusals must
			// not become a flood of log lines.
			log.WithFields(log.Fields{
				"id":   ef.ID,
				"peer": peer,
			}).Debug("refusing untrusted encrypted frame, storage is full of recent frames")
			return false
		}
	}
}

// evictUntrustedEncryptedFramesSavedBefore deletes a batch of the oldest untrusted frames saved before the given
// time, returning the bytes and number of frames freed
func (b *Bounce) evictUntrustedEncryptedFramesSavedBefore(before int64) (int64, int64) {
	var oldest []struct {
		ID   ID
		Size int64
	}
	err := b.database.
		Model(&encryptedFrame{}).
		Select("id, LENGTH(payload) AS size").
		Where("from_owner = ? AND saved_at < ?", false, before).
		Order("saved_at asc").
		Limit(untrustedEncryptedFrameEvictionBatch).
		Scan(&oldest).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error finding oldest untrusted encrypted frames to evict")
		return 0, 0
	}
	if len(oldest) == 0 {
		return 0, 0
	}

	ids := make([]ID, 0, len(oldest))
	freed := int64(0)
	for _, frame := range oldest {
		ids = append(ids, frame.ID)
		freed += frame.Size
	}
	err = b.database.Clauses(clause.Returning{}).Where("id IN (?)", ids).Delete(&encryptedFrame{}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error evicting oldest untrusted encrypted frames")
		return 0, 0
	}
	b.adjustUntrustedEncryptedFrameStorage(-freed, -int64(len(ids)))

	log.WithFields(log.Fields{
		"frames": len(ids),
		"bytes":  freed,
	}).Warn("evicted oldest untrusted encrypted frames to free storage")

	return freed, int64(len(ids))
}
