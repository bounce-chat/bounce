package goengine

import (
	"encoding/json"
	"fmt"
	"image"
	"io"
	"os"

	// Register the decoders image.Decode needs to measure attachments.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"

	"github.com/bbrks/go-blurhash"
	"github.com/bounce-chat/bounce/chat"
	"github.com/google/uuid"
	"golang.org/x/image/draw"
)

// outgoingAttachment is what Kotlin sends for each file on an outgoing message.
//
// Only a path crosses the boundary, never bytes: Go opens and streams the file,
// so a 200MB video is never materialized as a byte[] on either side. Kotlin is
// responsible for resolving a content:// URI to a real path first, which it has
// to do anyway to get a stable handle.
type outgoingAttachment struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// preparedAttachments is the engine-shaped result of resolving the JSON list.
type preparedAttachments struct {
	readers map[uuid.UUID]io.ReadCloser
	sources map[uuid.UUID]string
	images  []chat.ImageAttachment
	files   []chat.FileAttachment
}

func (p *preparedAttachments) closeAll() {
	for _, r := range p.readers {
		r.Close()
	}
}

// prepareAttachments opens every attachment and, for images, measures them.
//
// Deriving width/height/BlurHash here rather than in Kotlin is deliberate: the
// Go side already depends on go-blurhash and x/image, and this keeps the
// measurement identical to the desktop client (ui/pending_message_attachments.go),
// so the same photo produces the same placeholder on every platform.
func prepareAttachments(raw string) (*preparedAttachments, error) {
	p := &preparedAttachments{
		readers: map[uuid.UUID]io.ReadCloser{},
		sources: map[uuid.UUID]string{},
		images:  []chat.ImageAttachment{},
		files:   []chat.FileAttachment{},
	}
	if raw == "" {
		return p, nil
	}

	var atts []outgoingAttachment
	if err := json.Unmarshal([]byte(raw), &atts); err != nil {
		return nil, fmt.Errorf("bad attachments JSON: %w", err)
	}

	for _, at := range atts {
		id, err := parseID("attachment id", at.ID)
		if err != nil {
			p.closeAll()
			return nil, err
		}

		f, err := os.Open(at.Path)
		if err != nil {
			p.closeAll()
			return nil, fmt.Errorf("opening attachment %q: %w", at.Name, err)
		}

		size := at.Size
		if size == 0 {
			if info, statErr := f.Stat(); statErr == nil {
				size = info.Size()
			}
		}

		// Whether this is an image is decided by whether it actually decodes,
		// not by the name or the MIME type Kotlin guessed.
		width, height, hash, isImage := measureImage(f)
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			f.Close()
			p.closeAll()
			return nil, fmt.Errorf("rewinding attachment %q: %w", at.Name, err)
		}

		p.readers[id] = f
		p.sources[id] = at.Path

		if isImage {
			p.images = append(p.images, chat.ImageAttachment{
				ID:       id,
				Name:     at.Name,
				Size:     size,
				Width:    width,
				Height:   height,
				BlurHash: hash,
			})
		} else {
			p.files = append(p.files, chat.FileAttachment{
				ID:   id,
				Name: at.Name,
				Size: size,
			})
		}
	}

	return p, nil
}

// measureImage decodes r and returns its dimensions and BlurHash. It mirrors
// ui/pending_message_attachments.go: scale the longest edge down to ~32px with
// nearest-neighbour first, because BlurHash over a full-resolution photo is
// slow enough to be felt on a phone.
func measureImage(r io.Reader) (width, height int, hash string, ok bool) {
	img, _, err := image.Decode(r)
	if err != nil {
		return 0, 0, "", false
	}

	width = img.Bounds().Dx()
	height = img.Bounds().Dy()

	larger := width
	if height > larger {
		larger = height
	}
	scaleFactor := 1
	if larger > 32 {
		scaleFactor = larger / 32
	}
	smaller := image.NewRGBA(image.Rect(0, 0, img.Bounds().Max.X/scaleFactor, img.Bounds().Max.Y/scaleFactor))
	draw.NearestNeighbor.Scale(smaller, smaller.Rect, img, img.Bounds(), draw.Over, nil)

	if h, err := blurhash.Encode(4, 4, smaller); err == nil {
		hash = h
	}
	return width, height, hash, true
}

// SendDirectMessage sends a DM.
//
// The message ID is assigned by the engine - chat.Bounce.SendDirectMessage
// calls log.Fatal if the caller sets one - so this takes the thread and text
// rather than a whole serialized message.
//
// BLOCKING: database write plus attachment spooling.
func (a *Engine) SendDirectMessage(threadID string, text string, attachmentsJSON string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	tid, err := parseID("threadID", threadID)
	if err != nil {
		return err
	}
	p, err := prepareAttachments(attachmentsJSON)
	if err != nil {
		return err
	}

	e.SendDirectMessage(chat.DirectMessage{
		Thread:           tid,
		Text:             text,
		ImageAttachments: p.images,
		FileAttachments:  p.files,
	}, p.readers, p.sources)
	return nil
}

// SendGroupMessage sends a group message. See SendDirectMessage.
func (a *Engine) SendGroupMessage(threadID string, text string, attachmentsJSON string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	tid, err := parseID("threadID", threadID)
	if err != nil {
		return err
	}
	p, err := prepareAttachments(attachmentsJSON)
	if err != nil {
		return err
	}

	e.SendGroupMessage(chat.GroupMessage{
		Thread:           tid,
		Text:             text,
		ImageAttachments: p.images,
		FileAttachments:  p.files,
	}, p.readers, p.sources)
	return nil
}
