//go:build darwin && !ios

package platform

import (
	"errors"

	"github.com/bounce-chat/bounce/chat"
	"github.com/keybase/go-keychain"
)

// A keychainStore keeps the secret as a generic password in the user's login keychain, through the Security
// framework.  An item made this way trusts the application that made it, so another program gets a dialog before it
// can read the secret.  The /usr/bin/security tool is not used, because an item it makes trusts the tool, and then
// anything that runs the tool can read it.
type keychainStore struct {
	account string
}

func newSecretStore(configDirectory string) chat.SecretStore {
	return &keychainStore{account: configDirectory}
}

// Get returns an error other than ErrSecretNotFound when the user denies the keychain's dialog or the keychain is
// locked, since the item is still there and the next attempt can succeed
func (s *keychainStore) Get() ([]byte, error) {
	secret, err := keychain.GetGenericPassword(keystoreService, s.account, "", "")
	if err != nil {
		return nil, err
	}
	if len(secret) == 0 {
		return nil, chat.ErrSecretNotFound
	}
	return secret, nil
}

// Store adds the item, and updates it in place when one exists already, which the keychain reports as a duplicate
func (s *keychainStore) Store(secret []byte) error {
	err := keychain.AddItem(keychain.NewGenericPassword(keystoreService, s.account, keystoreService, secret, ""))
	if !errors.Is(err, keychain.ErrorDuplicateItem) {
		return err
	}

	existing := keychain.NewItem()
	existing.SetSecClass(keychain.SecClassGenericPassword)
	existing.SetService(keystoreService)
	existing.SetAccount(s.account)
	replacement := keychain.NewItem()
	replacement.SetData(secret)
	return keychain.UpdateItem(existing, replacement)
}
