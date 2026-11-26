package goengine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"io"

	// Register the decoders image.Decode needs to measure attachments.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"

	"github.com/bbrks/go-blurhash"
	"github.com/bounce-chat/bounce/chat"
	"golang.org/x/image/draw"
)

// outgoingAttachment is what Kotlin sends for each file on an outgoing message.
//
// Only a location crosses the boundary, never bytes: Go opens and streams the
// file, so a 200MB video is never materialized as a byte[] on either side. The
// location is a content URI exactly as a picker or a share handed it to Kotlin,
// read through the FileStore given to Start, or a path. The id is Kotlin's own
// handle for the composer and is not used here: the engine assigns every
// attachment's ID.
//
// A file no larger than EmbeddedFileLimit is read into the engine's blobs
// directory whatever its location is. What happens to a larger one depends on
// where it came from, which only Kotlin knows, so Kotlin says:
//
//   - inPlace: the file stays where it is and is served from its location for
//     as long as its message lives. Kotlin must already hold the persistable
//     permission for the URI.
//   - copyIn: the engine copies the file into its blobs directory and serves
//     the copy. For a location that cannot be relied on, or that is slow to
//     open, such as a file in cloud storage.
//   - neither: the file is left out of the message. For a file shared from
//     another app, whose access does not outlive the share.
type outgoingAttachment struct {
	ID       string `json:"id"`
	Location string `json:"location"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	InPlace  bool   `json:"inPlace"`
	CopyIn   bool   `json:"copyIn"`
}

// prepareAttachments opens every attachment and, for images, measures them.
//
// A file larger than the embed limit is left unread for the engine, which
// hashes it or copies it as a stream. A smaller one is read here only to
// measure it, and rewound - unless the provider handed out a pipe, which cannot
// be rewound, and is read into memory instead.
//
// Deriving width/height/BlurHash here rather than in Kotlin is deliberate: the
// Go side already depends on go-blurhash and x/image, and this keeps the
// measurement identical to the desktop client (ui/fyneui/pending_message_attachments.go),
// so the same photo produces the same placeholder on every platform. Only a
// file small enough to embed is measured, because a larger image travels as a
// plain file.
func prepareAttachments(files chat.FileStore, raw string) ([]chat.OutgoingAttachment, error) {
	if raw == "" {
		return nil, nil
	}

	var atts []outgoingAttachment
	if err := json.Unmarshal([]byte(raw), &atts); err != nil {
		return nil, fmt.Errorf("bad attachments JSON: %w", err)
	}

	prepared := []chat.OutgoingAttachment{}
	closeAll := func() {
		for _, a := range prepared {
			a.Reader.Close()
		}
	}
	for _, at := range atts {
		f, err := files.Open(at.Location)
		if err != nil {
			closeAll()
			return nil, fmt.Errorf("opening attachment %q: %w", at.Name, err)
		}

		// What the file itself says about its size beats what the provider's
		// listing told Kotlin. A provider may also hand out a pipe, which has
		// no size and cannot be rewound.
		size := at.Size
		regular := false
		if info, statErr := f.Stat(); statErr == nil && info.Mode().IsRegular() {
			size = info.Size()
			regular = true
		}

		a := chat.OutgoingAttachment{Name: at.Name, Size: size, Reader: f, CopyIn: at.CopyIn}
		if at.InPlace {
			a.Source = at.Location
		}

		if regular && size <= chat.EmbeddedFileLimit {
			// Whether this is an image is decided by whether it actually
			// decodes, not by the name or the MIME type Kotlin guessed.
			if width, height, hash, isImage := measureImage(f); isImage {
				a.Image = &chat.ImageInfo{Width: width, Height: height, BlurHash: hash}
			}
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				f.Close()
				closeAll()
				return nil, fmt.Errorf("rewinding attachment %q: %w", at.Name, err)
			}
		} else if !regular {
			// Read once, as far as the embed limit, and measured from memory.
			// One byte past the limit shows the file is too large to embed.
			data, err := io.ReadAll(io.LimitReader(f, chat.EmbeddedFileLimit+1))
			if err != nil {
				f.Close()
				closeAll()
				return nil, fmt.Errorf("reading attachment %q: %w", at.Name, err)
			}
			a.Size = int64(len(data))
			if len(data) > chat.EmbeddedFileLimit {
				// What was read is still the start of the file, and the rest
				// follows it.
				a.Reader = struct {
					io.Reader
					io.Closer
				}{io.MultiReader(bytes.NewReader(data), f), f}
			} else {
				f.Close()
				a.Reader = io.NopCloser(bytes.NewReader(data))
				if width, height, hash, isImage := measureImage(bytes.NewReader(data)); isImage {
					a.Image = &chat.ImageInfo{Width: width, Height: height, BlurHash: hash}
				}
			}
		}
		prepared = append(prepared, a)
	}

	return prepared, nil
}

// measureImage decodes r and returns its dimensions and BlurHash. It mirrors
// ui/fyneui/pending_message_attachments.go: scale the longest edge down to ~32px with
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
// clientRef is an ID of the caller's choosing, or "", echoed back as ClientRef
// on the DisplaySentDirectMessage event for this message so the caller can
// match the report to the send that produced it.
//
// BLOCKING: database write plus attachment spooling.
func (a *Engine) SendDirectMessage(threadID string, text string, attachmentsJSON string, clientRef string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	tid, err := parseID("threadID", threadID)
	if err != nil {
		return err
	}
	ref, err := parseClientRef(clientRef)
	if err != nil {
		return err
	}
	attachments, err := prepareAttachments(a.files, attachmentsJSON)
	if err != nil {
		return err
	}

	return e.SendDirectMessage(tid, text, attachments, ref)
}

// SendGroupMessage sends a group message. See SendDirectMessage.
func (a *Engine) SendGroupMessage(threadID string, text string, attachmentsJSON string, clientRef string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	tid, err := parseID("threadID", threadID)
	if err != nil {
		return err
	}
	ref, err := parseClientRef(clientRef)
	if err != nil {
		return err
	}
	attachments, err := prepareAttachments(a.files, attachmentsJSON)
	if err != nil {
		return err
	}

	return e.SendGroupMessage(tid, text, attachments, ref)
}

// parseClientRef reads an optional client reference: "" means none.
func parseClientRef(s string) (chat.ID, error) {
	if s == "" {
		return chat.NilID, nil
	}
	return parseID("clientRef", s)
}
