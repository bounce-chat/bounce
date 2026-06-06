package chat

import (
	"errors"
	"sync"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var draftCache = make(map[uuid.UUID]*draft)
var draftMutex sync.Mutex
var draftHandlerMutex sync.Mutex

func (b *Bounce) keepDraftsSynced() {
	for {
		time.Sleep(5 * time.Second)
		b.syncDrafts()
	}
}

type draft struct {
	SignedFrame
	ID        uuid.UUID `gorm:"type:uuid;primary_key;"`
	Thread    uuid.UUID
	Text      string
	Timestamp int64
	Saved     bool  `gorm:"-"`
	SavedAt   int64 `msgpack:"-"`
}

func (d *draft) BeforeCreate(tx *gorm.DB) error {
	d.SavedAt = time.Now().Unix()
	return nil
}

func (d *draft) AfterDelete(tx *gorm.DB) error {
	return tx.Clauses(clause.Returning{}).Where("frame_id = ? AND frame_type = ?", d.ID, typeDraft).Delete(&deliveryRecord{}).Error
}

func (d *draft) getID() uuid.UUID {
	return d.ID
}

func (d *draft) getType() uint16 {
	return typeDraft
}

func (d *draft) getPayload() []byte {
	bytes, err := msgpack.Marshal(signedContainer{
		Payload:   d.OriginalPayload,
		Signature: d.Signature,
		Signer:    d.Signer,
	})
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling draft")
	}
	return bytes
}

func (d *draft) getTimestamp() int64 {
	return d.Timestamp
}

func (d *draft) getSavedAt() int64 {
	return d.SavedAt
}

func (d *draft) getScope(myID uuid.UUID) int {
	return scopeSync
}

func (d *draft) getDestination(myID uuid.UUID) uuid.UUID {
	return myID
}

func (d *draft) getAuthor() uuid.UUID {
	return uuid.Nil
}

func (b *Bounce) handleDraft(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	draftHandlerMutex.Lock()
	draftHandlerMutex.Unlock()

	sc, err := b.unpackSignedContainer(payload)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unpacking signed container for draft")
		return nil, false
	}
	var d draft
	err = msgpack.Unmarshal(sc.Payload, &d)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling draft")
		return nil, false
	}
	d.OriginalPayload = sc.Payload
	d.Signature = sc.Signature
	d.Signer = sc.Signer

	var signerDevice device
	err = b.database.Select("revoked_at").Where("address = ?", d.Signer).First(&signerDevice).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"address": d.Signer,
			}).Error("signer device not found for draft")
			return nil, false
		} else {
			log.WithFields(log.Fields{
				"address": d.Signer,
				"error":   err.Error(),
			}).Fatal("database error looking up signing device")
		}
	}
	if signerDevice.RevokedAt != 0 && signerDevice.RevokedAt < d.Timestamp {
		log.WithFields(log.Fields{
			"id":     d.ID,
			"signer": d.Signer,
		}).Warn("ignoring draft signed by revoked device")
		go b.sendAck(peer, typeDraft, d.ID)
		return nil, false
	}

	if !b.signedByUser(sc, b.currentUserID()) {
		log.WithFields(log.Fields{
			"id":     d.ID,
			"signer": sc.Signer,
		}).Warn("received draft signed by another user, ignoring")
		return nil, false
	}

	var existingSavedDraft draft
	err = b.database.Take(&existingSavedDraft, "id = ?", d.ID).Error
	if err == nil {
		return &existingSavedDraft, false
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up draft")
	}

	draftMutex.Lock()
	existingDraft, ok := draftCache[d.Thread]
	draftMutex.Unlock()

	if !ok || d.Timestamp > existingDraft.Timestamp || (d.Timestamp == existingDraft.Timestamp && d.Text == "") {
		draftMutex.Lock()
		draftCache[d.Thread] = &d
		draftMutex.Unlock()

		if !catchUp {
			b.ui.UpdateDraft(Draft{Thread: d.Thread, Text: d.Text})
		}
	}

	if !catchUp {
		b.syncDrafts()
	}
	return &d, true
}

func (b *Bounce) UpdateDraft(threadID uuid.UUID, text string) {
	d := &draft{
		ID:        uuid.New(),
		Thread:    threadID,
		Text:      text,
		Timestamp: time.Now().Unix(),
		Saved:     false,
	}
	var err error
	d.OriginalPayload, err = msgpack.Marshal(d)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling draft")
	}
	sc := b.createSignedContainer(d.OriginalPayload)
	d.Signature = sc.Signature
	d.Signer = sc.Signer

	draftMutex.Lock()
	draftCache[threadID] = d
	draftMutex.Unlock()

	if text == "" {
		b.syncDrafts()
		b.pruneEncryptedDrafts()
	}
}

func (b *Bounce) syncDrafts() {
	draftMutex.Lock()
	defer draftMutex.Unlock()

	for _, d := range draftCache {
		if !d.Saved {
			err := b.database.Clauses(clause.Returning{}).Where("thread = ?", d.Thread).Delete(&draft{}).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error deleting drafts")
			}

			d.Saved = true
			err = b.database.Create(d).Error
			if err != nil {
				log.WithFields(log.Fields{
					"thread": d.Thread,
					"error":  err.Error(),
				}).Error("error saving draft")
			}

			b.broadcast(d)
		}
	}
}
