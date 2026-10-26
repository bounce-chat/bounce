package chat

import (
	"io"

	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
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
	return deleteEach[file](tx, "id = ?", fa.FileID)
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
	return deleteEach[file](tx, "id = ?", ia.FileID)
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

// messageAttachments is everything the engine makes of the attachments the UI asked for on one message: the rows
// that become part of the message's content, the prepared files waiting to be published once the message has its
// ID, and what to show the UI afterwards
type messageAttachments struct {
	images    []imageAttachment
	files     []fileAttachment
	prepared  []*file
	imageDTOs []ImageAttachment
	fileDTOs  []FileAttachment
}

// prepareMessageAttachments reads, hashes and assigns IDs to a message's attachments, before the message exists.  An
// attachment that cannot be read, is too large to embed and has no source, or cannot be prepared is left out and
// the rest of the message still goes.  Images too large to embed travel as plain file attachments, seeded.
func (b *Bounce) prepareMessageAttachments(attachments []OutgoingAttachment, scope int) messageAttachments {
	result := messageAttachments{}
	for _, a := range attachments {
		f, err := b.prepareAttachment(a, scope)
		if err != nil {
			log.WithFields(log.Fields{
				"name":  a.Name,
				"error": err.Error(),
			}).Error("leaving attachment out of message")
			continue
		}
		result.prepared = append(result.prepared, f)

		if a.Image != nil && a.Size <= EmbeddedFileLimit {
			result.images = append(result.images, imageAttachment{
				ID:       NewID(),
				FileID:   f.ID,
				Name:     a.Name,
				Size:     f.Size,
				Width:    a.Image.Width,
				Height:   a.Image.Height,
				BlurHash: a.Image.BlurHash,
			})
			result.imageDTOs = append(result.imageDTOs, ImageAttachment{
				ID:       f.ID,
				Name:     a.Name,
				Size:     f.Size,
				Width:    a.Image.Width,
				Height:   a.Image.Height,
				BlurHash: a.Image.BlurHash,
			})
		} else {
			result.files = append(result.files, fileAttachment{
				ID:     NewID(),
				FileID: f.ID,
				Name:   a.Name,
				Size:   f.Size,
			})
			result.fileDTOs = append(result.fileDTOs, FileAttachment{
				ID:   f.ID,
				Name: a.Name,
				Size: f.Size,
			})
		}
	}
	return result
}

// prepareAttachment prepares one attachment's file: embedded from its reader when small enough, otherwise seeded
// from its source.  The reader is always closed.
func (b *Bounce) prepareAttachment(a OutgoingAttachment, scope int) (*file, error) {
	if a.Reader != nil {
		defer a.Reader.Close()
	}

	if a.Size > EmbeddedFileLimit {
		if a.Source == "" {
			return nil, ErrFileTooBig
		}
		return b.prepareSeededFile(a.Source, scope, fileTypeMessageAttachment)
	}

	if a.Reader == nil {
		if a.Source == "" {
			return nil, io.ErrUnexpectedEOF
		}
		return b.prepareSeededFile(a.Source, scope, fileTypeMessageAttachment)
	}
	data, err := io.ReadAll(a.Reader)
	if err != nil {
		return nil, err
	}
	if len(data) > EmbeddedFileLimit {
		if a.Source == "" {
			return nil, ErrFileTooBig
		}
		return b.prepareSeededFile(a.Source, scope, fileTypeMessageAttachment)
	}
	return b.prepareEmbeddedFile(data, scope, fileTypeMessageAttachment)
}

// stampMessageID fills in the message every attachment row belongs to, once the message has its ID
func (ma *messageAttachments) stampMessageID(messageID ID) {
	for i := range ma.images {
		ma.images[i].MessageID = messageID
	}
	for i := range ma.files {
		ma.files[i].MessageID = messageID
	}
}

// publish sends every prepared file as attached to the message.  Everything that could be wrong with an attachment
// was found while preparing it, so a failure here is a database problem and the caller does not send the message:
// its ID commits to this attachment list, so none can be dropped now.
func (b *Bounce) publishMessageAttachments(ma messageAttachments, destination ID, messageID ID) error {
	for _, f := range ma.prepared {
		err := b.publishFile(f, destination, messageID)
		if err != nil {
			log.WithFields(log.Fields{
				"file":  f.ID,
				"error": err.Error(),
			}).Error("error publishing attachment, not sending message")
			return errAttachmentNotDistributed
		}
	}
	return nil
}

// closeAttachments releases the readers of attachments that will not be sent
func closeAttachments(attachments []OutgoingAttachment) {
	for _, a := range attachments {
		if a.Reader != nil {
			a.Reader.Close()
		}
	}
}
