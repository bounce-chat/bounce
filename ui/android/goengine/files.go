package goengine

import (
	"errors"
	"os"
	"strings"

	"github.com/bounce-chat/bounce/platform"
)

// ---------------------------------------------------------------------------
// Files the user picked, reached where they are
// ---------------------------------------------------------------------------

// FileStore reaches the files a user attaches and the places a user downloads
// large files to. It is implemented in Kotlin over the ContentResolver and
// handed to Start.
//
// Android's pickers hand out content URIs, which mean nothing to Go, and Kotlin
// used to copy every picked file into the app's cache so that the engine had a
// path to read. Now the URI itself crosses, as the file's location, and Go asks
// Kotlin for a file descriptor whenever it needs to reach it. Nothing is
// copied: a large file that is sent is served from where it sits for as long
// as its message lives, and one that is received is written straight into the
// document the user created for it. For that to keep working after a restart
// Kotlin must hold the persistable permission for every location it hands
// over: to read one it attaches as inPlace (see outgoingAttachment), and to
// read and write one it passes to DownloadFileToDisk.
//
// Every method is called on Go-owned threads, possibly several at once, and
// may block: a provider can take seconds to open a file.
type FileStore interface {
	// Open returns a file descriptor open for reading on a content URI. The
	// descriptor has to be detached (ParcelFileDescriptor.detachFd), because
	// Go owns it from here and closes it. Open is called once for every chunk
	// served, so it must not do more than open the file. Throw when the URI
	// cannot be read; the engine treats that as a file that is gone.
	Open(uri string) (int32, error)

	// Create returns a detached file descriptor open for reading and writing
	// on a content URI the user picked as the place to download a file to. It
	// must not truncate: it is called once for every chunk that arrives, and
	// each is written at its own offset. Throw when the URI cannot be
	// written; that chunk is then asked for again later.
	Create(uri string) (int32, error)

	// Remove deletes the document at a content URI and gives back its
	// permission. The engine calls it only for a download that will never be
	// completed: one that was cancelled, or whose message was deleted first.
	// A document that is already gone is not an error.
	Remove(uri string) error

	// Release is called when the engine is finished with a location for good,
	// because the last message attached to the file there was deleted. The
	// location is whatever the engine was given: a content URI, whose
	// persistable permission is given back, or a path, which is deleted when
	// it is inside the app's own storage and left alone anywhere else. The
	// user's own file is never deleted.
	Release(location string) error
}

// boundFileStore is the Kotlin store as the engine wants to see it. A path is
// reached here, without crossing to Kotlin: the engine's own blobs directory is
// a directory of plain files, and installs from before content URIs were kept
// still hold records of files in the app's own storage. A nil store is a host
// with no content URIs, which is a test running on a desktop.
type boundFileStore struct {
	platform.LocalFiles
	store FileStore
}

func (s boundFileStore) Open(location string) (*os.File, error) {
	if !strings.HasPrefix(location, "content://") {
		return s.LocalFiles.Open(location)
	}
	if s.store == nil {
		return nil, errors.New("no file store to open a content URI with")
	}
	descriptor, err := s.store.Open(location)
	if err != nil {
		return nil, err
	}
	if descriptor < 0 {
		return nil, errors.New("the file store returned no descriptor")
	}
	return os.NewFile(uintptr(descriptor), location), nil
}

func (s boundFileStore) Create(location string) (*os.File, error) {
	if !strings.HasPrefix(location, "content://") {
		return s.LocalFiles.Create(location)
	}
	if s.store == nil {
		return nil, errors.New("no file store to write a content URI with")
	}
	descriptor, err := s.store.Create(location)
	if err != nil {
		return nil, err
	}
	if descriptor < 0 {
		return nil, errors.New("the file store returned no descriptor")
	}
	return os.NewFile(uintptr(descriptor), location), nil
}

// Partial is the location itself for a content URI. A URI names one document,
// and nothing can be made beside it, so a download is written straight into the
// document the user created and there is never anything to rename.
func (s boundFileStore) Partial(location string) string {
	if !strings.HasPrefix(location, "content://") {
		return s.LocalFiles.Partial(location)
	}
	return location
}

func (s boundFileStore) Remove(location string) error {
	if !strings.HasPrefix(location, "content://") {
		return s.LocalFiles.Remove(location)
	}
	if s.store == nil {
		return errors.New("no file store to remove a content URI with")
	}
	return s.store.Remove(location)
}

func (s boundFileStore) Release(location string) error {
	if s.store == nil {
		return nil
	}
	return s.store.Release(location)
}
