package goengine

import (
	"errors"
	"os"
	"strings"
)

// ---------------------------------------------------------------------------
// Files the user picked, read where they are
// ---------------------------------------------------------------------------

// FileStore reaches the files a user attaches. It is implemented in Kotlin over
// the ContentResolver and handed to Start.
//
// Android's pickers hand out content URIs, which mean nothing to Go, and Kotlin
// used to copy every picked file into the app's cache so that the engine had a
// path to read. Now the URI itself crosses, as the attachment's location, and
// Go asks Kotlin for a file descriptor whenever it needs to read it. Nothing is
// copied, and a large file is served from where it sits for as long as its
// message lives. For that to keep working after a restart Kotlin must hold the
// persistable permission for every location it attaches as inPlace; see
// outgoingAttachment.
//
// Both methods are called on Go-owned threads, possibly several at once, and
// may block: a provider can take seconds to open a file.
type FileStore interface {
	// Open returns a file descriptor open for reading on a content URI. The
	// descriptor has to be detached (ParcelFileDescriptor.detachFd), because
	// Go owns it from here and closes it. Open is called once for every chunk
	// served, so it must not do more than open the file. Throw when the URI
	// cannot be read; the engine treats that as a file that is gone.
	Open(uri string) (int32, error)

	// Release is called when the engine is finished with a location for good,
	// because the last message attached to the file there was deleted. The
	// location is whatever the engine was given: a content URI, whose
	// persistable permission is given back, or a path, which is deleted when
	// it is inside the app's own storage and left alone anywhere else. The
	// user's own file is never deleted.
	Release(location string) error
}

// boundFileStore is the Kotlin store as the engine wants to see it. A path is
// opened here, without crossing to Kotlin. A nil store is a host with no
// content URIs, which is a test running on a desktop.
type boundFileStore struct {
	store FileStore
}

func (s boundFileStore) Open(location string) (*os.File, error) {
	if !strings.HasPrefix(location, "content://") {
		return os.Open(location)
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

func (s boundFileStore) Release(location string) error {
	if s.store == nil {
		return nil
	}
	return s.store.Release(location)
}
