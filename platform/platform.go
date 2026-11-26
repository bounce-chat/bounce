// Package platform supplies what the engine needs from the operating system it runs on: the directory an install
// keeps its files in, the keystore that holds the install's secret, and a way to open the files a user picks.
package platform

import (
	"sync"
	"testing"

	"github.com/bounce-chat/bounce/chat"
)

// Keystores that hold named entries file every Bounce secret under this service.  The account beside it is the
// install's config directory, so the main install and an encrypted device on one machine keep separate secrets.
const keystoreService = "Bounce"

// Get returns the platform of the main install
func Get() chat.Platform {
	configDirectory := GetConfigDirectory()
	return chat.Platform{
		ConfigDirectory: configDirectory,
		Secrets:         secretStoreFor(configDirectory),
		Files:           LocalFiles{},
	}
}

// GetEncrypted returns the platform of an encrypted device
func GetEncrypted() chat.Platform {
	configDirectory := GetEncryptedConfigDirectory()
	return chat.Platform{
		ConfigDirectory: configDirectory,
		Secrets:         secretStoreFor(configDirectory),
		Files:           LocalFiles{},
	}
}

var testSecretStoresMutex sync.Mutex
var testSecretStores = map[string]*memorySecretStore{}

// secretStoreFor returns the keystore entry belonging to a config directory, or nil where the platform has no
// keystore.  A test binary gets a store held in memory, one per directory, and never reaches the real keystore.
func secretStoreFor(configDirectory string) chat.SecretStore {
	if !testing.Testing() {
		return newSecretStore(configDirectory)
	}

	testSecretStoresMutex.Lock()
	defer testSecretStoresMutex.Unlock()
	store, ok := testSecretStores[configDirectory]
	if !ok {
		store = &memorySecretStore{}
		testSecretStores[configDirectory] = store
	}
	return store
}

type memorySecretStore struct {
	sync.Mutex
	secret []byte
}

func (s *memorySecretStore) Get() ([]byte, error) {
	s.Lock()
	defer s.Unlock()
	if s.secret == nil {
		return nil, chat.ErrSecretNotFound
	}
	return append([]byte{}, s.secret...), nil
}

func (s *memorySecretStore) Store(secret []byte) error {
	s.Lock()
	defer s.Unlock()
	s.secret = append([]byte{}, secret...)
	return nil
}
