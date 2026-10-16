package chat

import (
	"bytes"
	"errors"
	"sort"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// What an encrypted device sends a reader for one stored frame: the copy's host ID, the sealed payload, and the
// mailbox entry the reader asked for (see mailbox.go).  The reader derives the wrap key from the mailbox's secret
// and epoch, unwraps the DEK, and opens the frame.
type encryptedReceive struct {
	ID         uuid.UUID
	Payload    []byte
	Label      []byte
	WrappedDEK []byte
	savedAt    int64
}

func (er encryptedReceive) getID() uuid.UUID {
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

func (er encryptedReceive) getSavedAt() int64 {
	return er.savedAt
}

type encryptedCatchUp struct {
	Frames sortableEncryptedReceives
}

// Hosts order frames by when they received them, which is all they know; the reader re-sorts by the timestamps
// inside once the frames are open
type sortableEncryptedReceives []encryptedReceive

func (ser sortableEncryptedReceives) Len() int {
	return len(ser)
}
func (ser sortableEncryptedReceives) Swap(i, j int) {
	ser[i], ser[j] = ser[j], ser[i]
}
func (ser sortableEncryptedReceives) Less(i, j int) bool {
	if ser[i].savedAt == ser[j].savedAt {
		return bytes.Compare(ser[i].ID[:], ser[j].ID[:]) < 0
	}
	return ser[i].savedAt < ser[j].savedAt
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
func (b *Bounce) recordHostDelivery(peer string, hostID uuid.UUID) {
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
	decryptedFrames := []frame{}
	hostReferences := []frameReference{}
	for _, er := range ecu.Frames {
		decrypted, err := index.open(er)
		if err != nil {
			log.WithFields(log.Fields{
				"id":    er.ID,
				"peer":  peer,
				"error": err.Error(),
			}).Warn("could not open frame in encrypted catch up")
			continue
		}
		decryptedFrames = append(decryptedFrames, decrypted)
		hostReferences = append(hostReferences, frameReference{FrameID: er.ID, Type: typeEncryptedFrame})
		b.recordHostDelivery(peer, er.ID)
	}

	cu := &catchUp{
		Frames: decryptedFrames,
	}
	decryptedCatchUpPayload, err := msgpack.Marshal(cu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("cannot msgpack marshal catch up")
		return nil, false
	}

	br, first := b.handleCatchUp(peer, decryptedCatchUpPayload, true)

	// Ack the host's copies by the IDs the host knows them by, and tell the reference engine we have them
	if len(hostReferences) > 0 {
		go b.sendDirect(peer, &ack{References: hostReferences})
		b.loadCatchUp(peer, hostReferences)
	}
	b.sendDirect(peer, requestECRO{})
	return br, first
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

	br, _ := handler(peer, decrypted.Payload, false)
	if br != nil {
		go b.sendAck(peer, typeEncryptedFrame, er.ID)
		b.recordHostDelivery(peer, er.ID)
	}

	return nil, false
}

// generateEncryptedCatchUpFor serves the frames a peer requested from the mailboxes it asked for.  A frame is only
// served through a mailbox entry whose label the peer presented, so the host never hands out a wrap for a mailbox
// the peer did not prove it can read.
func (b *Bounce) generateEncryptedCatchUpFor(peer string, rr referenceRequest) *encryptedCatchUp {
	ecu := &encryptedCatchUp{}

	for _, reference := range rr.References {
		if reference.Type != typeEncryptedFrame {
			continue
		}
		var ef encryptedFrame
		err := b.database.Preload("Mailboxes").Take(&ef, "id = ?", reference.FrameID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id":   reference.FrameID,
					"peer": peer,
				}).Debug("requested encrypted frame not found")
				continue
			}
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up encrypted frame")
		}

		entry, ok := b.mailboxEntryFor(peer, ef)
		if !ok {
			log.WithFields(log.Fields{
				"id":   reference.FrameID,
				"peer": peer,
			}).Warn("encrypted frame requested by a peer that did not ask for any of its mailboxes")
			continue
		}

		ecu.Frames = append(ecu.Frames, encryptedReceive{
			ID:         ef.ID,
			Payload:    ef.Payload,
			Label:      entry.Label,
			WrappedDEK: entry.WrappedDEK,
			savedAt:    ef.SavedAt,
		})
	}

	sort.Sort(ecu.Frames)

	return ecu
}
