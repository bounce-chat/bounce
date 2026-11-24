package chat

import "errors"

// A Platform is everything the engine needs from the operating system it runs on.  One Platform describes one install:
// its config directory and the keystore entry that belongs to that directory, which is why the store takes no name.
type Platform struct {
	ConfigDirectory string

	// Secrets is nil on a platform with no keystore
	Secrets SecretStore
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
