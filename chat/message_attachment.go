package chat

import (
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type fileAttachment struct {
	ID        ID
	FileID    ID
	MessageID ID
	Name      string
	Size      int64
}

func (fa *fileAttachment) AfterDelete(tx *gorm.DB) error {
	if fa.ID == NilID {
		return nil
	}
	return tx.Clauses(clause.Returning{}).Where("id = ?", fa.FileID).Delete(&file{}).Error
}

type imageAttachment struct {
	ID        ID
	FileID    ID
	MessageID ID
	Name      string
	Size      int64
	Width     int
	Height    int
	BlurHash  string
}

func (ia *imageAttachment) AfterDelete(tx *gorm.DB) error {
	if ia.ID == NilID {
		return nil
	}
	return tx.Clauses(clause.Returning{}).Where("id = ?", ia.FileID).Delete(&file{}).Error
}

// Attachments are part of a message's content, so they are packed into its ID.  The message ID itself is left out
// of each row, since it is what is being generated.
func packImageAttachments(ch *contentHasher, attachments []imageAttachment) *contentHasher {
	ch.int64(int64(len(attachments)))
	for _, ia := range attachments {
		ch.id(ia.ID).
			id(ia.FileID).
			bytes([]byte(ia.Name)).
			int64(ia.Size).
			int64(int64(ia.Width)).
			int64(int64(ia.Height)).
			bytes([]byte(ia.BlurHash))
	}
	return ch
}

func packFileAttachments(ch *contentHasher, attachments []fileAttachment) *contentHasher {
	ch.int64(int64(len(attachments)))
	for _, fa := range attachments {
		ch.id(fa.ID).
			id(fa.FileID).
			bytes([]byte(fa.Name)).
			int64(fa.Size)
	}
	return ch
}

// A pendingAttachment is a file that will be distributed once the message it belongs to has its ID.  Embedding
// or seeding a file broadcasts a file frame that names the message, so it cannot happen before the message's
// content, which includes the attachment, is fixed.
type pendingAttachment struct {
	fileID      ID
	name        string
	data        []byte // Embedded, or
	source      string // seeded from this path
	scope       int
	destination ID
}

// distributeAttachment embeds or seeds a pending attachment for a message.  Seeding hashes the whole file, so it
// runs in the background and reports its own failure.
func (b *Bounce) distributeAttachment(p pendingAttachment, messageID ID) error {
	if p.source != "" {
		go func() {
			err := b.seedFile(p.fileID, p.source, p.scope, p.destination, fileTypeMessageAttachment, messageID)
			if err != nil {
				log.WithFields(log.Fields{
					"id":    p.fileID,
					"name":  p.name,
					"error": err.Error(),
				}).Error("error seeding file attachment")
			}
		}()
		return nil
	}
	return b.embedFile(p.fileID, p.data, p.scope, p.destination, fileTypeMessageAttachment, messageID)
}
