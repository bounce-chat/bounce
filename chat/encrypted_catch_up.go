package chat

import (
	"errors"
	"sort"

	"github.com/Basekick-Labs/msgpack/v6"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// What an encrypted device sends a reader for one stored frame: the copy's host ID, the sealed payload, and the
// mailbox entry the reader asked for (see mailbox.go).  The reader derives the wrap key from the mailbox's secret
// and epoch, unwraps the DEK, and opens the frame.
type encryptedReceive struct {
	ID         ID
	Payload    []byte
	Label      []byte
	WrappedDEK []byte
}

func (er encryptedReceive) getID() ID {
	return er.ID
}

func (er encryptedReceive) getPayload() []byte {
	bytes, err := msgpack.Marshal(&er)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal encrypted receive")
	}
	return bytes
}

func (er encryptedReceive) getType() uint16 {
	return typeEncryptedReceive
}

// A catch up of frames stored on an encrypted device, staged the same way as a plain catch up: at most
// catchUpPayloadBudget bytes per catch up, the earliest first, with Incomplete set when more is waiting.  A host
// orders frames by when it received them, which is all it knows about them.
type encryptedCatchUp struct {
	Frames     []encryptedReceive
	Incomplete bool
	onComplete func()
}

func (ecu *encryptedCatchUp) complete() {
	if ecu.onComplete != nil {
		complete := ecu.onComplete
		ecu.onComplete = nil
		complete()
	}
}

func (ecu *encryptedCatchUp) getType() uint16 {
	return typeEncryptedCatchUp
}

func (ecu *encryptedCatchUp) getPayload() []byte {
	bytes, err := msgpack.Marshal(ecu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal catch up")
	}
	return bytes
}

// recordHostDelivery remembers that a host delivered one of its copies to us, so that it is acked rather than
// requested the next time the host offers it
func (b *Bounce) recordHostDelivery(peer string, hostID ID) {
	err := b.database.Clauses(clause.OnConflict{DoNothing: true}).Create(&deliveryRecord{
		Destination: peer,
		FrameID:     hostID,
		FrameType:   typeEncryptedFrame,
	}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
			"id":    hostID,
			"peer":  peer,
		}).Error("error saving delivery tracking for encrypted receive")
	}
}

// handleEncryptedCatchUp opens the frames a host served and replays them through the normal catch up, which knows
// them by the copy IDs the host does: those are what it acks, refuses and re-requests, so the host never sees the
// ID or type of a frame it stored.
func (b *Bounce) handleEncryptedCatchUp(peer string, payload []byte, _ bool) (broadcastable, bool) {
	var ecu encryptedCatchUp
	err := msgpack.Unmarshal(payload, &ecu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling encrypted catch up")
		return nil, false
	}

	index := b.mailboxIndex()
	cu := catchUp{Incomplete: ecu.Incomplete}
	hostCopies := []ID{}
	for _, er := range ecu.Frames {
		decrypted, err := index.open(er)
		if err != nil {
			// Not acked, and never asked for from this host again: it is in none of the mailboxes we can read now
			log.WithFields(log.Fields{
				"id":    er.ID,
				"peer":  peer,
				"error": err.Error(),
			}).Warn("could not open frame in encrypted catch up")
			b.refuseReference(peer, er.ID, typeEncryptedFrame)
			continue
		}
		cu.Frames = append(cu.Frames, decrypted)
		hostCopies = append(hostCopies, er.ID)
	}

	b.applyCatchUp(peer, cu, hostCopies)
	go b.sendDirect(peer, requestECRO{})
	return nil, false
}

func (b *Bounce) handleEncryptedReceive(peer string, payload []byte, _ bool) (broadcastable, bool) {
	var er encryptedReceive
	err := msgpack.Unmarshal(payload, &er)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling encrypted receive")
		return nil, false
	}

	decrypted, err := b.mailboxIndex().open(er)
	if err != nil {
		log.WithFields(log.Fields{
			"id":    er.ID,
			"peer":  peer,
			"error": err.Error(),
		}).Warn("could not open encrypted receive")
		return nil, false
	}

	handlers := b.getHandlers(false)
	handler, ok := handlers[decrypted.Type]
	if !ok {
		log.WithFields(log.Fields{
			"type": decrypted.Type,
		}).Error("encrypted receive type does not have a handler")
		return nil, false
	}

	// The host is acked by the ID of its copy, never the ID of the frame inside, which is why this is done here and
	// the frame is not handed back to the read loop to ack.  Otherwise the frame is treated as one that arrived from
	// any other device: the host is recorded as holding it, and the first time we see it, it is passed on.
	br, firstTimeSeen := handler(peer, decrypted.Payload, false)
	if br != nil {
		go b.sendAck(peer, typeEncryptedFrame, er.ID)
		b.recordHostDelivery(peer, er.ID)
		b.markDeliveredTo(br, peer)
		if firstTimeSeen {
			b.broadcast(br)
		}
	}

	return nil, false
}

// How many IDs go into one query when looking up requested frames, well under what SQLite will bind
const encryptedCatchUpLookupBatch = 500

// generateEncryptedCatchUpFor serves the frames a peer requested from the mailboxes it asked for.  A frame is only
// served through a mailbox entry whose label the peer presented, so the host never hands out a wrap for a mailbox
// the peer did not prove it can read.  The earliest frames that fit the budget are sent, and Incomplete is set when
// any were left for the next request.
func (b *Bounce) generateEncryptedCatchUpFor(peer string, rr referenceRequest) *encryptedCatchUp {
	ecu := &encryptedCatchUp{}

	requested := []ID{}
	for _, reference := range rr.References {
		if reference.Type == typeEncryptedFrame {
			requested = append(requested, reference.FrameID)
		}
	}

	// Put what was asked for in the order it arrived here, without loading any payloads yet
	type arrival struct {
		ID      ID
		SavedAt int64
		Arrived int64
	}
	arrivals := []arrival{}
	for start := 0; start < len(requested); start += encryptedCatchUpLookupBatch {
		batch := requested[start:min(start+encryptedCatchUpLookupBatch, len(requested))]
		var found []arrival
		err := b.database.
			Model(&encryptedFrame{}).
			Select("id, saved_at, rowid AS arrived").
			Where("id IN ?", batch).
			Scan(&found).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up requested encrypted frames")
		}
		arrivals = append(arrivals, found...)
	}
	sort.Slice(arrivals, func(i, j int) bool {
		if arrivals[i].SavedAt == arrivals[j].SavedAt {
			return arrivals[i].Arrived < arrivals[j].Arrived
		}
		return arrivals[i].SavedAt < arrivals[j].SavedAt
	})

	total := 0
	for _, next := range arrivals {
		var ef encryptedFrame
		err := b.database.Preload("Mailboxes").Take(&ef, "id = ?", next.ID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up encrypted frame")
		}

		entry, ok := b.mailboxEntryFor(peer, ef)
		if !ok {
			log.WithFields(log.Fields{
				"id":   next.ID,
				"peer": peer,
			}).Warn("encrypted frame requested by a peer that did not ask for any of its mailboxes")
			continue
		}

		size := len(ef.Payload)
		if len(ecu.Frames) > 0 && total+size > catchUpPayloadBudget {
			ecu.Incomplete = true
			break
		}
		total += size

		ecu.Frames = append(ecu.Frames, encryptedReceive{
			ID:         ef.ID,
			Payload:    ef.Payload,
			Label:      entry.Label,
			WrappedDEK: entry.WrappedDEK,
		})
	}

	return ecu
}
