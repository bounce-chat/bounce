package goengine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/bounce-chat/bounce/chat"
)

// Kotlin decides which screen to show by comparing an exception's message with
// the Error* constants, so an engine error that lost its code would land the
// user on the screen for an unknown failure.
func TestErrorsTheUIActsOnCrossAsTheirCodes(t *testing.T) {
	known := map[error]string{
		chat.ErrPasswordRequired:      ErrorPasswordRequired,
		chat.ErrWrongPassword:         ErrorWrongPassword,
		chat.ErrKeystoreSecretMissing: ErrorKeystoreSecretMissing,
		chat.ErrKeystoreUnavailable:   ErrorKeystoreUnavailable,
		chat.ErrKeyFileMissing:        ErrorKeyFileMissing,
		chat.ErrKeyFileDamaged:        ErrorKeyFileDamaged,
		chat.ErrWrongRecoveryCode:     ErrorWrongRecoveryCode,
		chat.ErrMistypedRecoveryCode:  ErrorMistypedRecoveryCode,
		chat.ErrNoRecoveryCode:        ErrorNoRecoveryCode,
		chat.ErrInstallUnwrapped:      ErrorInstallUnwrapped,
		chat.ErrRollUnfinished:        ErrorRollUnfinished,
	}
	if len(known) != len(errorCodes) {
		t.Fatalf("%d errors are checked here and %d have codes", len(known), len(errorCodes))
	}
	seen := map[string]bool{}
	for err, code := range known {
		if got := coded(err).Error(); got != code {
			t.Fatalf("%v crossed as %q, want %q", err, got, code)
		}
		// The engine wraps some of these with what it was doing at the time
		if got := coded(fmt.Errorf("opening the key file: %w", err)).Error(); got != code {
			t.Fatalf("wrapped %v crossed as %q, want %q", err, got, code)
		}
		if seen[code] {
			t.Fatalf("the code %q stands for two errors", code)
		}
		seen[code] = true
	}

	other := errors.New("disk full")
	if coded(other) != other {
		t.Fatalf("an error with no code should cross as it is")
	}
	if coded(nil) != nil {
		t.Fatalf("no error should stay no error")
	}
}

type fakeKotlinStore struct {
	secret []byte
	err    error
}

func (s *fakeKotlinStore) Get() ([]byte, error) { return s.secret, s.err }

func (s *fakeKotlinStore) Store(secret []byte) error {
	if s.err != nil {
		return s.err
	}
	s.secret = secret
	return nil
}

// The engine recovers from "no secret" and waits out "could not ask", so the
// two must not be confused on the way over from Kotlin.
func TestSecretStoreAnswersReachTheEngineApart(t *testing.T) {
	kotlin := &fakeKotlinStore{}
	store := installPlatform(t.TempDir(), kotlin).Secrets

	if _, err := store.Get(); !errors.Is(err, chat.ErrSecretNotFound) {
		t.Fatalf("a store that returned nothing should mean no secret, got %v", err)
	}

	if err := store.Store([]byte("thirty-two bytes of secret here!")); err != nil {
		t.Fatalf("storing: %v", err)
	}
	secret, err := store.Get()
	if err != nil || !bytes.Equal(secret, []byte("thirty-two bytes of secret here!")) {
		t.Fatalf("the secret did not come back: %q, %v", secret, err)
	}

	kotlin.err = errors.New("android.security.KeyStoreException: keystore busy")
	if _, err := store.Get(); err == nil || errors.Is(err, chat.ErrSecretNotFound) {
		t.Fatalf("a store that threw should mean the keystore could not be asked, got %v", err)
	}
	if err := store.Store([]byte("x")); err == nil {
		t.Fatalf("a store that threw on storing should fail")
	}

	if installPlatform(t.TempDir(), nil).Secrets != nil {
		t.Fatalf("no Kotlin store should be no keystore at all, not a store that fails")
	}
}

// The Kotlin mirror of chat.SecurityStatus names these three fields
func TestSecurityStatusJSONNamesItsFields(t *testing.T) {
	b, err := json.Marshal(chat.SecurityStatus{KeystoreUsed: true, PasswordSet: true, RecoveryCodeCreatedAt: 1795000000})
	if err != nil {
		t.Fatalf("marshalling security status: %v", err)
	}
	want := `{"KeystoreUsed":true,"PasswordSet":true,"RecoveryCodeCreatedAt":1795000000}`
	if string(b) != want {
		t.Fatalf("security status marshalled as %s, want %s", b, want)
	}
}
