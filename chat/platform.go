package chat

import (
	"errors"
	"os"
)

// A Platform is everything the engine needs from the operating system it runs on.  One Platform describes one install:
// its config directory and the keystore entry that belongs to that directory, which is why the store takes no name.
type Platform struct {
	ConfigDirectory string

	// Secrets is nil on a platform with no keystore
	Secrets SecretStore

	// Files reaches the files an install reads and writes where the user keeps them
	Files FileStore
}

// A FileStore reaches the files an install reads and writes.  A file too large to embed is not copied when it is
// sent: its record keeps the location the user picked it from and it is served from there for as long as the record
// lives.  One that is received is downloaded straight into the location the user picked for it, and stays there.  A
// location is whatever the platform's file pickers hand out, a path on a desktop and a content URI on Android, and
// it means nothing to any other platform.  The engine's own blobs directory is reached through the same store, by
// path, so every platform's store has to handle a path inside the config directory as well.
type FileStore interface {
	// Open opens the file at a location for reading.  It is called again for every chunk served from the file,
	// so it has to keep working after a restart, and a location the platform can no longer read is an error.
	Open(location string) (*os.File, error)

	// Create opens the file at a location for reading and writing at any offset.  The file is created if it is
	// not there and keeps what it holds if it is.  A download calls it again for every chunk that arrives, which
	// can be days apart and on either side of a restart.
	Create(location string) (*os.File, error)

	// Partial is where a download to a location is written until all of it has arrived.  A platform that can keep
	// a second file beside the one the user picked names that file, and the engine renames it to the location
	// once the download is complete.  A platform that cannot returns the location itself, and the download is
	// written straight into the file the user picked.
	Partial(location string) string

	// Rename moves a completed download from where it was written to the location the user picked
	Rename(from, to string) error

	// Remove deletes a file the engine wrote for a download that will never be completed.  It is never called
	// for a file that is complete, nor for one the user attached.
	Remove(location string) error

	// Release is called when the last record of a file at a location has been deleted.  Whatever the platform
	// holds only to keep the location reachable is given up here, such as a permission, or a copy the platform
	// itself made where the user cannot reach it.  A file the user can reach is theirs and is never deleted.
	Release(location string) error
}

// A SecretStore keeps one secret for an install in the operating system's keystore.  The engine generates the secret
// and decides what it is for; the store only keeps it.
type SecretStore interface {
	// Get returns the stored secret.  It returns ErrSecretNotFound when the keystore answered and holds no secret
	// for this install, and any other error when the keystore could not be asked: it is locked, unreachable, or
	// the user refused.  The two must never be confused, because the engine treats the first as a lost secret and
	// the second as something to try again.
	Get() ([]byte, error)

	// Store saves the secret, replacing any earlier one
	Store(secret []byte) error
}

// ErrSecretNotFound is returned by a SecretStore that was reached and holds no secret for this install
var ErrSecretNotFound = errors.New("no secret is stored for this install")
