//go:build (linux && !android) || freebsd || openbsd || netbsd || dragonfly

package platform

import (
	"encoding/hex"
	"errors"

	"github.com/bounce-chat/bounce/chat"
	"github.com/godbus/dbus/v5"
	"github.com/zalando/go-keyring"
)

// A secretServiceStore keeps the secret in the desktop's Secret Service, which is GNOME Keyring, KWallet or KeePassXC
// depending on the desktop.  The service stores text, so the secret is kept as hex.
type secretServiceStore struct {
	account string
}

func newSecretStore(configDirectory string) chat.SecretStore {
	return &secretServiceStore{account: configDirectory}
}

// sessionBusReachable reports whether a session bus is already running.  The keyring library reaches the bus through
// a call that starts one with dbus-launch when it finds none, and on a headless machine that leaves a bus and a
// keyring daemon running after Bounce has gone.  Asking here first, without the automatic start, means the library is
// only ever called when its bus exists.
func sessionBusReachable() error {
	connection, err := dbus.SessionBusPrivateNoAutoStartup()
	if err != nil {
		return err
	}
	connection.Close()
	return nil
}

// Get blocks for as long as the desktop's unlock prompt is on screen when the keyring is locked
func (s *secretServiceStore) Get() ([]byte, error) {
	err := sessionBusReachable()
	if err != nil {
		return nil, err
	}

	encoded, err := keyring.Get(keystoreService, s.account)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, chat.ErrSecretNotFound
	}
	if err != nil {
		return nil, err
	}

	// An entry that does not decode was not written by this store, and waiting will not change it, so it is
	// reported as no entry rather than as a keystore to try again
	secret, err := hex.DecodeString(encoded)
	if err != nil || len(secret) == 0 {
		return nil, chat.ErrSecretNotFound
	}
	return secret, nil
}

func (s *secretServiceStore) Store(secret []byte) error {
	err := sessionBusReachable()
	if err != nil {
		return err
	}
	return keyring.Set(keystoreService, s.account, hex.EncodeToString(secret))
}
