package goengine

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/bounce-chat/bounce/chat"
	"github.com/bounce-chat/bounce/platform"
)

// ---------------------------------------------------------------------------
// At-rest protection: the keystore, the password and the recovery code
// ---------------------------------------------------------------------------

// The errors a user interface has to act on. gobind carries nothing of a Go
// error across but its text, so each of these crosses to Kotlin as an exception
// whose whole message is the code, and Kotlin compares messages against these
// constants. Any other error crosses with its own text.
const (
	ErrorPasswordRequired      = "password_required"
	ErrorWrongPassword         = "wrong_password"
	ErrorKeystoreSecretMissing = "keystore_secret_missing"
	ErrorKeystoreUnavailable   = "keystore_unavailable"
	ErrorKeyFileMissing        = "key_file_missing"
	ErrorKeyFileDamaged        = "key_file_damaged"
	ErrorWrongRecoveryCode     = "wrong_recovery_code"
	ErrorMistypedRecoveryCode  = "mistyped_recovery_code"
	ErrorNoRecoveryCode        = "no_recovery_code"
	ErrorInstallUnwrapped      = "install_unwrapped"
	ErrorRollUnfinished        = "roll_unfinished"
)

var errorCodes = []struct {
	err  error
	code string
}{
	{chat.ErrPasswordRequired, ErrorPasswordRequired},
	{chat.ErrWrongPassword, ErrorWrongPassword},
	{chat.ErrKeystoreSecretMissing, ErrorKeystoreSecretMissing},
	{chat.ErrKeystoreUnavailable, ErrorKeystoreUnavailable},
	{chat.ErrKeyFileMissing, ErrorKeyFileMissing},
	{chat.ErrKeyFileDamaged, ErrorKeyFileDamaged},
	{chat.ErrWrongRecoveryCode, ErrorWrongRecoveryCode},
	{chat.ErrMistypedRecoveryCode, ErrorMistypedRecoveryCode},
	{chat.ErrNoRecoveryCode, ErrorNoRecoveryCode},
	{chat.ErrInstallUnwrapped, ErrorInstallUnwrapped},
	{chat.ErrRollUnfinished, ErrorRollUnfinished},
}

// coded replaces an error the user interface acts on by one whose text is its
// code, and returns every other error as it is.
func coded(err error) error {
	for _, known := range errorCodes {
		if errors.Is(err, known.err) {
			return errors.New(known.code)
		}
	}
	return err
}

// SecretStore keeps the install's one keystore secret. It is implemented in
// Kotlin over the Android Keystore and handed to Start and Recover.
//
// The engine tells three answers apart when it asks for the secret, and a
// Kotlin exception cannot carry the Go value that marks one of them, so they
// are told apart here like this:
//
//   - Get returns the secret.
//   - Get returns nothing and does not throw: the keystore holds no secret for
//     this install. The engine then sends the user to the recovery code.
//   - Get throws: the keystore could not be asked just now. The engine reports
//     that and the user tries again; it never falls back to doing without.
//
// Both methods are called on a Go-owned thread, during Start or Recover, and
// may block.
type SecretStore interface {
	Get() ([]byte, error)
	Store(secret []byte) error
}

// boundSecretStore is the Kotlin store as the engine wants to see it
type boundSecretStore struct {
	store SecretStore
}

func (s boundSecretStore) Get() ([]byte, error) {
	secret, err := s.store.Get()
	if err != nil {
		return nil, err
	}
	if len(secret) == 0 {
		return nil, chat.ErrSecretNotFound
	}
	return secret, nil
}

func (s boundSecretStore) Store(secret []byte) error {
	return s.store.Store(secret)
}

// installPlatform is the platform of the install in configDir. A nil store is
// a device with no keystore, which the engine handles by doing without one.
func installPlatform(configDir string, secrets SecretStore) chat.Platform {
	if configDir == "" {
		configDir = platform.GetConfigDirectory()
	}
	install := chat.Platform{ConfigDirectory: configDir}
	if secrets != nil {
		install.Secrets = boundSecretStore{store: secrets}
	}
	return install
}

// Recover opens an install with its recovery code and protects it afresh, with
// a new keystore secret and with newPassword, which may be empty for none. It
// is for an install that Start has refused to open, and Start is called again
// afterwards with newPassword.
//
// BLOCKING: stretches the new password, which takes about a second.
func Recover(configDir string, secrets SecretStore, recoveryCode string, newPassword string) error {
	return coded(chat.Recover(installPlatform(configDir, secrets), recoveryCode, newPassword))
}

// Reset deletes everything the install keeps in configDir, for an install that
// nothing can open any more. The next Start is a first run. Files the app keeps
// elsewhere are Kotlin's to delete.
func Reset(configDir string) error {
	return chat.Reset(installPlatform(configDir, nil))
}

// SecurityStatus returns how the install is protected, as the JSON of
// chat.SecurityStatus.
func (a *Engine) SecurityStatus() (string, error) {
	e, err := a.engine()
	if err != nil {
		return "", err
	}
	status, err := e.SecurityStatus()
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(&status)
	if err != nil {
		return "", fmt.Errorf("marshalling security status: %w", err)
	}
	return string(b), nil
}

// SetPassword sets, changes or removes the password Start asks for.
// currentPassword is empty when none is set, and an empty newPassword removes
// it.
//
// BLOCKING: stretches both passwords, which takes about a second each.
func (a *Engine) SetPassword(currentPassword string, newPassword string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	return coded(e.SetPassword(currentPassword, newPassword))
}

// CreateRecoveryCode makes a new recovery code, which replaces the one before
// it, and returns it to be shown once. password is the install's password when
// it has one.
func (a *Engine) CreateRecoveryCode(password string) (string, error) {
	e, err := a.engine()
	if err != nil {
		return "", err
	}
	code, err := e.CreateRecoveryCode(password)
	if err != nil {
		return "", coded(err)
	}
	return code, nil
}
