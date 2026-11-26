package goengine

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bounce-chat/bounce/chat"
	"github.com/bounce-chat/bounce/platform"
)

// The Kotlin layer parses every engine event with kotlinx.serialization, so the
// JSON encoding of chat's DTOs is a wire format now. These tests pin the two
// properties Kotlin depends on and that are easy to break from the Go side:
// IDs render as hex strings (not 32-element byte arrays), and maps keyed
// by chat.ID render as string-keyed objects.

func TestIDsMarshalAsHexStrings(t *testing.T) {
	id := chat.MustParseID("550e8400e29b41d4a716446655440000550e8400e29b41d4a716446655440000")

	b, err := json.Marshal(chat.DirectMessage{ID: id, Author: id, Thread: id})
	if err != nil {
		t.Fatalf("marshalling direct message: %v", err)
	}

	got := string(b)
	if !strings.Contains(got, `"550e8400e29b41d4a716446655440000550e8400e29b41d4a716446655440000"`) {
		t.Fatalf("ID did not marshal as a hex string, got %s", got)
	}
	// A [32]byte that lost its TextMarshaler would come out as a JSON array.
	if strings.Contains(got, `"ID":[`) {
		t.Fatalf("ID marshalled as a byte array, Kotlin cannot parse this: %s", got)
	}
}

func TestBulkUpdateMapsKeyOnIDStrings(t *testing.T) {
	id := chat.MustParseID("550e8400e29b41d4a716446655440000550e8400e29b41d4a716446655440000")

	bu := chat.BulkUpdate{
		Source:         id,
		Seen:           []chat.ID{id},
		GroupMessages:  map[chat.ID][]chat.GroupMessage{id: {{ID: id, Text: "hi"}}},
		DirectMessages: map[chat.ID][]chat.DirectMessage{id: {{ID: id, Text: "hi"}}},
		ReadReceipts:   map[chat.ID][]chat.ReadReceipt{id: {{ID: id}}},
	}

	b, err := json.Marshal(bu)
	if err != nil {
		// encoding/json only accepts non-string map keys when the key type
		// implements TextMarshaler. chat.ID does; this guards a regression.
		t.Fatalf("marshalling bulk update: %v", err)
	}

	var round chat.BulkUpdate
	if err := json.Unmarshal(b, &round); err != nil {
		t.Fatalf("unmarshalling bulk update: %v", err)
	}
	if len(round.GroupMessages[id]) != 1 || round.GroupMessages[id][0].Text != "hi" {
		t.Fatalf("bulk update did not round trip: %+v", round)
	}
}

// Every DTO that crosses to Kotlin must survive a marshal/unmarshal round trip.
// A type that fails here is a type Kotlin will silently receive as garbage.
func TestEveryUIDTORoundTrips(t *testing.T) {
	id := chat.MustParseID("550e8400e29b41d4a716446655440000550e8400e29b41d4a716446655440000")

	cases := []struct {
		name string
		val  any
		into any
	}{
		{"User", chat.User{ID: id, Name: "Alice", Images: []chat.ID{id}}, &chat.User{}},
		{"Device", chat.Device{ID: id, Name: "Phone", Address: "abc"}, &chat.Device{}},
		{"Settings", chat.Settings{DefaultGroupRetention: 42}, &chat.Settings{}},
		{"DMState", chat.DMState{Open: true, Retention: 7}, &chat.DMState{}},
		{"DirectMessage", chat.DirectMessage{ID: id, Text: "hello"}, &chat.DirectMessage{}},
		{"GroupMessage", chat.GroupMessage{ID: id, Text: "hello"}, &chat.GroupMessage{}},
		{"Group", chat.Group{ID: id, Name: "Friends", Admins: []chat.ID{id}}, &chat.Group{}},
		{"NewGroup", chat.NewGroup{Name: "Friends", InitialInvites: []chat.ID{id}}, &chat.NewGroup{}},
		{"ImageAttachment", chat.ImageAttachment{ID: id, Width: 4, Height: 2}, &chat.ImageAttachment{}},
		{"FileAttachment", chat.FileAttachment{ID: id, Name: "a.pdf"}, &chat.FileAttachment{}},
		{"ReadReceipt", chat.ReadReceipt{ID: id, Actor: id, Target: id}, &chat.ReadReceipt{}},
		{"Draft", chat.Draft{Thread: id, Text: "wip"}, &chat.Draft{}},
		{"FileProgress", chat.FileProgress{ID: id, Progress: 0.5}, &chat.FileProgress{}},
		{"UpdateDMRetention", chat.UpdateDMRetention{ID: id, Retention: 1}, &chat.UpdateDMRetention{}},
		{"UpdateDMClearHistory", chat.UpdateDMClearHistory{ID: id}, &chat.UpdateDMClearHistory{}},
		{"UpdateGroupName", chat.UpdateGroupName{ID: id, Name: "N"}, &chat.UpdateGroupName{}},
		{"UpdateGroupRetention", chat.UpdateGroupRetention{ID: id}, &chat.UpdateGroupRetention{}},
		{"UpdateGroupInviteUser", chat.UpdateGroupInviteUser{ID: id}, &chat.UpdateGroupInviteUser{}},
		{"UpdateGroupRemoveUser", chat.UpdateGroupRemoveUser{ID: id}, &chat.UpdateGroupRemoveUser{}},
		{"UpdateGroupClearHistory", chat.UpdateGroupClearHistory{ID: id}, &chat.UpdateGroupClearHistory{}},
		{"UpdateGroupAdminPromoted", chat.UpdateGroupAdminPromoted{ID: id}, &chat.UpdateGroupAdminPromoted{}},
		{"UpdateGroupAdminDemoted", chat.UpdateGroupAdminDemoted{ID: id}, &chat.UpdateGroupAdminDemoted{}},
		{"UpdateGroupInviteRevoked", chat.UpdateGroupInviteRevoked{ID: id}, &chat.UpdateGroupInviteRevoked{}},
		{"UpdateGroupInviteAccepted", chat.UpdateGroupInviteAccepted{ID: id}, &chat.UpdateGroupInviteAccepted{}},
		{"UpdateGroupInviteRejected", chat.UpdateGroupInviteRejected{ID: id}, &chat.UpdateGroupInviteRejected{}},
		{"UserBlockedGroup", chat.UserBlockedGroup{ID: id}, &chat.UserBlockedGroup{}},
		{"RemovedFromGroup", chat.RemovedFromGroup{Group: id, Actor: id}, &chat.RemovedFromGroup{}},
		{"GroupDeleted", chat.GroupDeleted{Group: id, Actor: id}, &chat.GroupDeleted{}},
		{"UpdateUserUpdateName", chat.UpdateUserUpdateName{ID: id, Name: "N"}, &chat.UpdateUserUpdateName{}},
		{"UpdateUserUpdateImage", chat.UpdateUserUpdateImage{ID: id}, &chat.UpdateUserUpdateImage{}},
		{"InitialState", chat.InitialState{NetworkOnline: true}, &chat.InitialState{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.val)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if err := json.Unmarshal(b, tc.into); err != nil {
				t.Fatalf("unmarshal of %s: %v", b, err)
			}
		})
	}
}

// prepareAttachments is the one piece of real logic in the binding, so its
// failure modes are worth pinning: a bad path must not leak open files.
func TestPrepareAttachmentsRejectsMissingFile(t *testing.T) {
	_, err := prepareAttachments(boundFileStore{}, `[{"id":"550e8400e29b41d4a716446655440000550e8400e29b41d4a716446655440000","location":"/nonexistent/nope","name":"nope"}]`)
	if err == nil {
		t.Fatal("expected an error for a missing attachment path")
	}
	if !strings.Contains(err.Error(), "opening attachment") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPrepareAttachmentsEmpty(t *testing.T) {
	p, err := prepareAttachments(boundFileStore{}, "")
	if err != nil {
		t.Fatalf("empty attachments should be fine: %v", err)
	}
	if len(p) != 0 {
		t.Fatal("expected nothing prepared")
	}
}

// attachmentJSON is one attachment the way Kotlin sends it
func attachmentJSON(t *testing.T, at outgoingAttachment) string {
	t.Helper()
	raw, err := json.Marshal([]outgoingAttachment{at})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// testPNG is a small image that decodes
func testPNG(t *testing.T) []byte {
	t.Helper()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 40, 30))); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func TestPrepareAttachmentsReadsASmallFileAndMeasuresAnImage(t *testing.T) {
	data := testPNG(t)
	path := filepath.Join(t.TempDir(), "small.png")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	// A small file is embedded whatever Kotlin said should happen to a large one
	prepared, err := prepareAttachments(boundFileStore{}, attachmentJSON(t, outgoingAttachment{Location: path, Name: "small.png", CopyIn: true}))
	if err != nil {
		t.Fatal(err)
	}
	a := prepared[0]
	read, err := io.ReadAll(a.Reader)
	if err != nil || !bytes.Equal(data, read) || a.Size != int64(len(data)) {
		t.Fatalf("read %d of %d bytes, size %d, error %v", len(read), len(data), a.Size, err)
	}
	if a.Image == nil || a.Image.Width != 40 || a.Image.Height != 30 {
		t.Fatalf("the image was not measured: %+v", a.Image)
	}
	if a.Source != "" {
		t.Fatalf("a location is only a source when Kotlin says it can be served in place, got %q", a.Source)
	}
}

func TestPrepareAttachmentsLeavesALargeFileToTheEngine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.bin")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, chat.EmbeddedFileLimit+1); err != nil {
		t.Fatal(err)
	}

	for name, test := range map[string]struct {
		at     outgoingAttachment
		source string
		copyIn bool
	}{
		"served in place":    {outgoingAttachment{Location: path, InPlace: true}, path, false},
		"copied in":          {outgoingAttachment{Location: path, CopyIn: true}, "", true},
		"neither":            {outgoingAttachment{Location: path}, "", false},
		"a size from Kotlin": {outgoingAttachment{Location: path, Size: chat.EmbeddedFileLimit + 1, InPlace: true}, path, false},
	} {
		t.Run(name, func(t *testing.T) {
			prepared, err := prepareAttachments(boundFileStore{}, attachmentJSON(t, test.at))
			if err != nil {
				t.Fatal(err)
			}
			a := prepared[0]
			defer a.Reader.Close()
			if a.Size != chat.EmbeddedFileLimit+1 || a.Source != test.source || a.CopyIn != test.copyIn || a.Image != nil {
				t.Fatalf("size %d, source %q, copy in %v, image %v", a.Size, a.Source, a.CopyIn, a.Image)
			}
		})
	}
}

func TestPrepareAttachmentsTrustsTheFileOverTheSizeItWasGiven(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.bin")
	if err := os.WriteFile(path, []byte("the start of the file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, chat.EmbeddedFileLimit+10); err != nil {
		t.Fatal(err)
	}

	prepared, err := prepareAttachments(boundFileStore{}, attachmentJSON(t, outgoingAttachment{Location: path, Size: 5, CopyIn: true}))
	if err != nil {
		t.Fatal(err)
	}
	a := prepared[0]
	defer a.Reader.Close()
	if a.Size != chat.EmbeddedFileLimit+10 || !a.CopyIn {
		t.Fatalf("the engine must be told the file is large: size %d, copy in %v", a.Size, a.CopyIn)
	}
	read, err := io.Copy(io.Discard, a.Reader)
	if err != nil || read != chat.EmbeddedFileLimit+10 {
		t.Fatalf("the file is handed over unread, but %d bytes were left, error %v", read, err)
	}
}

// pipeFiles hands out the reading end of a pipe, as a provider that produces a
// file while it is being read does. A pipe has no size and cannot seek.
type pipeFiles struct {
	platform.LocalFiles
	data []byte
}

func (p pipeFiles) Open(location string) (*os.File, error) {
	reading, writing, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	go func() {
		writing.Write(p.data)
		writing.Close()
	}()
	return reading, nil
}

func (p pipeFiles) Release(location string) error {
	return nil
}

func TestPrepareAttachmentsReadsAFileThatCannotSeek(t *testing.T) {
	data := testPNG(t)
	prepared, err := prepareAttachments(pipeFiles{data: data}, attachmentJSON(t, outgoingAttachment{Location: "content://example/1", Name: "piped.png"}))
	if err != nil {
		t.Fatal(err)
	}
	a := prepared[0]
	read, err := io.ReadAll(a.Reader)
	if err != nil || !bytes.Equal(data, read) || a.Size != int64(len(data)) || a.Image == nil {
		t.Fatalf("read %d of %d bytes, size %d, image %v, error %v", len(read), len(data), a.Size, a.Image, err)
	}

	// One too large to embed is found out by reading it, and loses nothing of what was read finding out
	large := make([]byte, chat.EmbeddedFileLimit+10)
	copy(large, "the start of the file")
	prepared, err = prepareAttachments(pipeFiles{data: large}, attachmentJSON(t, outgoingAttachment{Location: "content://example/2", CopyIn: true}))
	if err != nil {
		t.Fatal(err)
	}
	a = prepared[0]
	defer a.Reader.Close()
	if a.Size <= chat.EmbeddedFileLimit || !a.CopyIn || a.Image != nil {
		t.Fatalf("the engine must be told the file is large: size %d, copy in %v, image %v", a.Size, a.CopyIn, a.Image)
	}
	start := make([]byte, 21)
	if _, err := io.ReadFull(a.Reader, start); err != nil || string(start) != "the start of the file" {
		t.Fatalf("got %q, %v", start, err)
	}
	rest, err := io.Copy(io.Discard, a.Reader)
	if err != nil || rest != chat.EmbeddedFileLimit+10-21 {
		t.Fatalf("%d bytes followed, error %v", rest, err)
	}
}

// kotlinFiles stands in for the store Kotlin implements. It can open one
// content URI and remembers everything it was asked.
type kotlinFiles struct {
	uri      string
	path     string
	opened   []string
	created  []string
	removed  []string
	released []string
}

// A descriptor handed to Go belongs to Go. The file it was opened through is
// kept reachable for as long as the test binary runs, so that its finalizer
// never closes a descriptor that is no longer its own.
var handedOver []*os.File

func (k *kotlinFiles) Open(uri string) (int32, error) {
	k.opened = append(k.opened, uri)
	if uri != k.uri {
		return 0, errors.New("FileNotFoundException: No content provider")
	}
	f, err := os.Open(k.path)
	if err != nil {
		return 0, err
	}
	handedOver = append(handedOver, f)
	return int32(f.Fd()), nil
}

func (k *kotlinFiles) Create(uri string) (int32, error) {
	k.created = append(k.created, uri)
	if uri != k.uri {
		return 0, errors.New("SecurityException: no permission to write")
	}
	f, err := os.OpenFile(k.path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return 0, err
	}
	handedOver = append(handedOver, f)
	return int32(f.Fd()), nil
}

func (k *kotlinFiles) Remove(uri string) error {
	k.removed = append(k.removed, uri)
	return os.Remove(k.path)
}

func (k *kotlinFiles) Release(location string) error {
	k.released = append(k.released, location)
	return nil
}

func TestContentURIsAreOpenedByKotlinAndPathsAreNot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "picked.txt")
	if err := os.WriteFile(path, []byte("picked with the photo picker"), 0600); err != nil {
		t.Fatal(err)
	}
	kotlin := &kotlinFiles{uri: "content://media/picker/0/com.android.providers.media.photopicker/media/1", path: path}
	files := boundFileStore{store: kotlin}

	prepared, err := prepareAttachments(files, attachmentJSON(t, outgoingAttachment{Location: kotlin.uri, Name: "picked.txt"}))
	if err != nil {
		t.Fatal(err)
	}
	read, err := io.ReadAll(prepared[0].Reader)
	if err != nil || string(read) != "picked with the photo picker" {
		t.Fatalf("read %q through the descriptor Kotlin opened, error %v", read, err)
	}

	f, err := files.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if len(kotlin.opened) != 1 || kotlin.opened[0] != kotlin.uri {
		t.Fatalf("only the content URI crosses to Kotlin, got %v", kotlin.opened)
	}

	// A URI that can no longer be read is an error, and so is any URI on a host with no store
	if _, err := files.Open("content://media/gone"); err == nil {
		t.Fatal("expected Kotlin's failure to open the URI")
	}
	if _, err := (boundFileStore{}).Open(kotlin.uri); err == nil {
		t.Fatal("expected an error with no store to open the URI")
	}

	// Kotlin is told about every location the engine is finished with, because a path may be a copy it made
	if err := files.Release(kotlin.uri); err != nil {
		t.Fatal(err)
	}
	if err := files.Release(path); err != nil {
		t.Fatal(err)
	}
	if len(kotlin.released) != 2 || kotlin.released[0] != kotlin.uri || kotlin.released[1] != path {
		t.Fatalf("released %v", kotlin.released)
	}
	if err := (boundFileStore{}).Release(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("releasing a location never deletes anything on this side")
	}
}

func TestADownloadToAContentURIIsWrittenThroughKotlinIntoTheDocumentItself(t *testing.T) {
	directory := t.TempDir()
	document := filepath.Join(directory, "saved.bin")
	kotlin := &kotlinFiles{uri: "content://com.android.providers.downloads.documents/document/msf%3A31", path: document}
	files := boundFileStore{store: kotlin}

	// Nothing can be made beside a document, so the download has no partial file of its own
	if partial := files.Partial(kotlin.uri); partial != kotlin.uri {
		t.Fatalf("a download to a content URI is written into the document itself, got %q", partial)
	}

	// Chunks arrive in any order, each opening the document again and writing at its own offset
	for _, chunk := range []struct {
		offset int64
		data   string
	}{{6, "second"}, {0, "first "}} {
		f, err := files.Create(kotlin.uri)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteAt([]byte(chunk.data), chunk.offset); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	written, err := os.ReadFile(document)
	if err != nil || string(written) != "first second" {
		t.Fatalf("the document holds %q, error %v", written, err)
	}
	if len(kotlin.created) != 2 {
		t.Fatalf("each chunk opens the document through Kotlin, got %v", kotlin.created)
	}

	// A cancelled download removes the document, through Kotlin, which is what holds the permission to
	if err := files.Remove(kotlin.uri); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(document); !os.IsNotExist(err) {
		t.Fatalf("the document should be gone, got %v", err)
	}
	if len(kotlin.removed) != 1 || kotlin.removed[0] != kotlin.uri {
		t.Fatalf("removed %v", kotlin.removed)
	}

	// A path is a file an earlier version downloaded into the app's own storage, and is reached without Kotlin
	path := filepath.Join(directory, "earlier.bin")
	if partial := files.Partial(path); partial != path+".bouncedownload" {
		t.Fatalf("a download to a path keeps its partial file beside it, got %q", partial)
	}
	f, err := files.Create(files.Partial(path))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := files.Rename(files.Partial(path), path); err != nil {
		t.Fatal(err)
	}
	if err := files.Remove(path); err != nil {
		t.Fatal(err)
	}
	if len(kotlin.created) != 2 || len(kotlin.removed) != 1 {
		t.Fatalf("a path must not cross to Kotlin, created %v, removed %v", kotlin.created, kotlin.removed)
	}

	// A URI that cannot be written is an error, and so is any URI on a host with no store
	if _, err := files.Create("content://com.android.providers.downloads.documents/document/gone"); err == nil {
		t.Fatal("expected Kotlin's failure to open the URI for writing")
	}
	if _, err := (boundFileStore{}).Create(kotlin.uri); err == nil {
		t.Fatal("expected an error with no store to write the URI")
	}
	if err := (boundFileStore{}).Remove(kotlin.uri); err == nil {
		t.Fatal("expected an error with no store to remove the URI")
	}
}

// memoryFile is a file the engine has opened, held in memory
type memoryFile struct {
	*bytes.Reader
	closed bool
}

func (m *memoryFile) Size() int64 {
	return m.Reader.Size()
}

func (m *memoryFile) Close() error {
	m.closed = true
	return nil
}

func TestABoundReaderHandsBackWhatThereIsAndNothingPastTheEnd(t *testing.T) {
	file := &memoryFile{Reader: bytes.NewReader([]byte("0123456789"))}
	reader := &FileReader{reader: file}
	if reader.Size() != 10 {
		t.Fatalf("size %d", reader.Size())
	}

	for _, read := range []struct {
		offset int64
		length int
		want   string
	}{
		{0, 4, "0123"},
		{4, 4, "4567"},
		// Kotlin is asked for more than is left when a player reaches the end, and wants what there is
		{8, 4, "89"},
		{10, 4, ""},
		{99, 4, ""},
		{3, 0, ""},
	} {
		got, err := reader.ReadAt(read.offset, read.length)
		if err != nil || string(got) != read.want {
			t.Fatalf("reading %d at %d gave %q, error %v, want %q", read.length, read.offset, got, err, read.want)
		}
	}
	if _, err := reader.ReadAt(-1, 4); err == nil {
		t.Fatal("expected an error for a negative offset")
	}
	if _, err := reader.ReadAt(0, -1); err == nil {
		t.Fatal("expected an error for a negative length")
	}

	if err := reader.Close(); err != nil || !file.closed {
		t.Fatalf("closing the reader closes the file, got %v, closed %v", err, file.closed)
	}
}
