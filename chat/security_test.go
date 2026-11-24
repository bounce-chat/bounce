package chat

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/alecthomas/assert/v2"
)

// newTestInstall makes an install the way a first run leaves it, and a Bounce that carries only what the security
// methods use, since none of them needs a running engine.  A nil store is a device with no keystore.
func newTestInstall(t *testing.T, secrets SecretStore) (*Bounce, masterKeySet) {
	t.Helper()
	platform := Platform{ConfigDirectory: t.TempDir(), Secrets: secrets}
	_, keys, err := unlock(platform, platform.ConfigDirectory+"/bounce.db", "")
	assert.NoError(t, err)
	return &Bounce{platform: platform}, keys
}

// openTestInstall is what the next start does with the key file
func openTestInstall(b *Bounce, password string) (masterKeySet, error) {
	_, keys, err := unlock(b.platform, b.platform.ConfigDirectory+"/bounce.db", password)
	return keys, err
}

func TestSetPasswordSetsChangesAndRemovesIt(t *testing.T) {
	b, keys := newTestInstall(t, &testSecretStore{})
	status, err := b.SecurityStatus()
	assert.NoError(t, err)
	assert.Equal(t, SecurityStatus{KeystoreUsed: true}, status)

	assert.NoError(t, b.SetPassword("", "correct horse"))
	status, err = b.SecurityStatus()
	assert.NoError(t, err)
	assert.Equal(t, SecurityStatus{KeystoreUsed: true, PasswordSet: true}, status)
	_, err = openTestInstall(b, "")
	assert.True(t, errors.Is(err, ErrPasswordRequired))
	opened, err := openTestInstall(b, "correct horse")
	assert.NoError(t, err)
	assert.True(t, opened.equal(keys), "a password changes how the master key is wrapped and not the key")

	// The password in use has to be given to change it
	before, err := os.ReadFile(b.platform.ConfigDirectory + "/" + keyFileName)
	assert.NoError(t, err)
	assert.True(t, errors.Is(b.SetPassword("wrong horse", "battery staple"), ErrWrongPassword))
	assert.True(t, errors.Is(b.SetPassword("", "battery staple"), ErrPasswordRequired))
	assert.True(t, errors.Is(b.SetPassword("wrong horse", ""), ErrWrongPassword), "and to remove it")
	after, err := os.ReadFile(b.platform.ConfigDirectory + "/" + keyFileName)
	assert.NoError(t, err)
	assert.Equal(t, before, after)

	assert.NoError(t, b.SetPassword("correct horse", "battery staple"))
	_, err = openTestInstall(b, "correct horse")
	assert.True(t, errors.Is(err, ErrWrongPassword))
	opened, err = openTestInstall(b, "battery staple")
	assert.NoError(t, err)
	assert.True(t, opened.equal(keys))

	assert.NoError(t, b.SetPassword("battery staple", ""))
	status, err = b.SecurityStatus()
	assert.NoError(t, err)
	assert.Equal(t, SecurityStatus{KeystoreUsed: true}, status)
	opened, err = openTestInstall(b, "")
	assert.NoError(t, err)
	assert.True(t, opened.equal(keys))

	assert.NoError(t, b.SetPassword("", ""), "removing a password that is not set is nothing to do")
}

func TestSetPasswordNeedsTheKeystoreTheInstallUses(t *testing.T) {
	store := &testSecretStore{}
	b, _ := newTestInstall(t, store)
	before, err := os.ReadFile(b.platform.ConfigDirectory + "/" + keyFileName)
	assert.NoError(t, err)

	store.unavailable = errors.New("the keyring is locked")
	assert.True(t, errors.Is(b.SetPassword("", "correct horse"), ErrKeystoreUnavailable))
	_, err = b.CreateRecoveryCode("")
	assert.True(t, errors.Is(err, ErrKeystoreUnavailable))
	store.unavailable = nil

	after, err := os.ReadFile(b.platform.ConfigDirectory + "/" + keyFileName)
	assert.NoError(t, err)
	assert.Equal(t, before, after)
}

// An install with no keystore and no password has had its master key on disk in the clear, so its first password
// also puts a new master key beside the old one, for the next start to move everything to
func TestFirstPasswordOnAnUnprotectedInstallSchedulesANewMasterKey(t *testing.T) {
	b, keys := newTestInstall(t, nil)
	status, err := b.SecurityStatus()
	assert.NoError(t, err)
	assert.Equal(t, SecurityStatus{}, status)

	assert.NoError(t, b.SetPassword("", "correct horse"))
	status, err = b.SecurityStatus()
	assert.NoError(t, err)
	assert.Equal(t, SecurityStatus{PasswordSet: true}, status)
	opened, err := openTestInstall(b, "correct horse")
	assert.NoError(t, err)
	assert.Equal(t, keys.master, opened.previous, "the key the data is under is kept until it has been replaced")
	assert.Equal(t, masterKeySize, len(opened.master))
	assert.NotEqual(t, keys.master, opened.master)

	// Changing the password after that is a rewrap like any other
	assert.NoError(t, b.SetPassword("correct horse", "battery staple"))
	again, err := openTestInstall(b, "battery staple")
	assert.NoError(t, err)
	assert.True(t, again.equal(opened))
}

func TestRemovingTheOnlyProtectionDropsTheRecoveryCode(t *testing.T) {
	b, _ := newTestInstall(t, nil)
	assert.NoError(t, b.SetPassword("", "correct horse"))
	code, err := b.CreateRecoveryCode("correct horse")
	assert.NoError(t, err)
	status, err := b.SecurityStatus()
	assert.NoError(t, err)
	assert.True(t, status.RecoveryCodeCreatedAt > 0)

	assert.NoError(t, b.SetPassword("correct horse", ""))
	status, err = b.SecurityStatus()
	assert.NoError(t, err)
	assert.Equal(t, SecurityStatus{}, status)
	assert.True(t, errors.Is(Recover(b.platform, code, ""), ErrNoRecoveryCode))

	// On a device with a keystore the install is still protected without the password, and the code stays
	b, _ = newTestInstall(t, &testSecretStore{})
	assert.NoError(t, b.SetPassword("", "correct horse"))
	_, err = b.CreateRecoveryCode("correct horse")
	assert.NoError(t, err)
	assert.NoError(t, b.SetPassword("correct horse", ""))
	status, err = b.SecurityStatus()
	assert.NoError(t, err)
	assert.True(t, status.KeystoreUsed)
	assert.True(t, status.RecoveryCodeCreatedAt > 0)
}

func TestCreateRecoveryCodeReplacesTheCodeBefore(t *testing.T) {
	b, keys := newTestInstall(t, &testSecretStore{})
	started := time.Now().Unix()
	first, err := b.CreateRecoveryCode("")
	assert.NoError(t, err)
	status, err := b.SecurityStatus()
	assert.NoError(t, err)
	assert.True(t, status.RecoveryCodeCreatedAt >= started && status.RecoveryCodeCreatedAt <= time.Now().Unix())

	second, err := b.CreateRecoveryCode("")
	assert.NoError(t, err)
	assert.NotEqual(t, first, second)
	assert.True(t, errors.Is(Recover(b.platform, first, ""), ErrWrongRecoveryCode), "only the latest code works")

	// With a password set it is asked for again, because the code opens everything the password does
	assert.NoError(t, b.SetPassword("", "correct horse"))
	_, err = b.CreateRecoveryCode("")
	assert.True(t, errors.Is(err, ErrPasswordRequired))
	_, err = b.CreateRecoveryCode("wrong horse")
	assert.True(t, errors.Is(err, ErrWrongPassword))
	third, err := b.CreateRecoveryCode("correct horse")
	assert.NoError(t, err)
	assert.True(t, errors.Is(Recover(b.platform, second, ""), ErrWrongRecoveryCode))

	// Making a code changes nothing about how the install opens
	opened, err := openTestInstall(b, "correct horse")
	assert.NoError(t, err)
	assert.True(t, opened.equal(keys))
	assert.NoError(t, Recover(b.platform, third, ""))
}

func TestAnUnprotectedInstallHasNoRecoveryCode(t *testing.T) {
	b, _ := newTestInstall(t, nil)
	_, err := b.CreateRecoveryCode("")
	assert.True(t, errors.Is(err, ErrInstallUnwrapped))
	status, err := b.SecurityStatus()
	assert.NoError(t, err)
	assert.Equal(t, SecurityStatus{}, status)
}

func TestRecoverAfterAForgottenPassword(t *testing.T) {
	store := &testSecretStore{}
	b, keys := newTestInstall(t, store)
	assert.NoError(t, b.SetPassword("", "correct horse"))
	code, err := b.CreateRecoveryCode("correct horse")
	assert.NoError(t, err)
	created, err := b.SecurityStatus()
	assert.NoError(t, err)

	assert.NoError(t, Recover(b.platform, code, "battery staple"))
	_, err = openTestInstall(b, "correct horse")
	assert.True(t, errors.Is(err, ErrWrongPassword))
	opened, err := openTestInstall(b, "battery staple")
	assert.NoError(t, err)
	assert.True(t, opened.equal(keys), "recovering wraps the same master key again, so nothing is lost")
	_, err = os.Stat(b.platform.ConfigDirectory + "/.pid")
	assert.True(t, errors.Is(err, os.ErrNotExist), "the instance lock is given back")

	// The code is the same one afterwards, and can be used again, this time for no password at all
	status, err := b.SecurityStatus()
	assert.NoError(t, err)
	assert.Equal(t, created.RecoveryCodeCreatedAt, status.RecoveryCodeCreatedAt)
	assert.NoError(t, Recover(b.platform, code, ""))
	status, err = b.SecurityStatus()
	assert.NoError(t, err)
	assert.Equal(t, SecurityStatus{KeystoreUsed: true, RecoveryCodeCreatedAt: created.RecoveryCodeCreatedAt}, status)
	opened, err = openTestInstall(b, "")
	assert.NoError(t, err)
	assert.True(t, opened.equal(keys))
}

func TestRecoverAfterTheKeystoreLostItsSecret(t *testing.T) {
	store := &testSecretStore{}
	b, keys := newTestInstall(t, store)
	code, err := b.CreateRecoveryCode("")
	assert.NoError(t, err)

	lost := store.secret
	store.secret = nil
	_, err = openTestInstall(b, "")
	assert.True(t, errors.Is(err, ErrKeystoreSecretMissing))

	assert.NoError(t, Recover(b.platform, code, ""))
	assert.Equal(t, keystoreSecretSize, len(store.secret))
	assert.NotEqual(t, lost, store.secret, "the keystore is given a new secret, as on a first run")
	opened, err := openTestInstall(b, "")
	assert.NoError(t, err)
	assert.True(t, opened.equal(keys))
}

// A data folder moved to a device with no keystore opens with the recovery code and a password, or with the code
// alone, which leaves it unprotected
func TestRecoverOnADeviceWithNoKeystore(t *testing.T) {
	b, keys := newTestInstall(t, &testSecretStore{})
	code, err := b.CreateRecoveryCode("")
	assert.NoError(t, err)
	b.platform.Secrets = nil
	_, err = openTestInstall(b, "")
	assert.True(t, errors.Is(err, ErrKeystoreSecretMissing))

	assert.NoError(t, Recover(b.platform, code, "correct horse"))
	status, err := b.SecurityStatus()
	assert.NoError(t, err)
	assert.False(t, status.KeystoreUsed)
	assert.True(t, status.PasswordSet)
	assert.True(t, status.RecoveryCodeCreatedAt > 0)
	opened, err := openTestInstall(b, "correct horse")
	assert.NoError(t, err)
	assert.True(t, opened.equal(keys))

	assert.NoError(t, Recover(b.platform, code, ""))
	status, err = b.SecurityStatus()
	assert.NoError(t, err)
	assert.Equal(t, SecurityStatus{}, status, "with nothing protecting the install the code is dropped")
	opened, err = openTestInstall(b, "")
	assert.NoError(t, err)
	assert.True(t, opened.equal(keys))
	assert.True(t, errors.Is(Recover(b.platform, code, ""), ErrNoRecoveryCode))
}

func TestRecoverChangesNothingWithoutTheRightCode(t *testing.T) {
	store := &testSecretStore{}
	b, _ := newTestInstall(t, store)
	assert.True(t, errors.Is(Recover(b.platform, "0000-0000-0000-0000-0000-0000-0000-0000", ""), ErrMistypedRecoveryCode))

	other, _, err := newRecoveryCode()
	assert.NoError(t, err)
	assert.True(t, errors.Is(Recover(b.platform, other, ""), ErrNoRecoveryCode), "an install that never had a code")

	code, err := b.CreateRecoveryCode("")
	assert.NoError(t, err)
	before, err := os.ReadFile(b.platform.ConfigDirectory + "/" + keyFileName)
	assert.NoError(t, err)
	secret := append([]byte{}, store.secret...)

	assert.True(t, errors.Is(Recover(b.platform, other, "correct horse"), ErrWrongRecoveryCode))
	mistyped := []byte(code)
	if mistyped[0] == '0' {
		mistyped[0] = '1'
	} else {
		mistyped[0] = '0'
	}
	assert.True(t, errors.Is(Recover(b.platform, string(mistyped), "correct horse"), ErrMistypedRecoveryCode))

	after, err := os.ReadFile(b.platform.ConfigDirectory + "/" + keyFileName)
	assert.NoError(t, err)
	assert.Equal(t, before, after)
	assert.Equal(t, secret, store.secret, "the keystore secret the key file depends on is left alone")
	_, err = os.Stat(b.platform.ConfigDirectory + "/.pid")
	assert.True(t, errors.Is(err, os.ErrNotExist), "the instance lock is given back")

	assert.NoError(t, os.Remove(b.platform.ConfigDirectory+"/"+keyFileName))
	assert.True(t, errors.Is(Recover(b.platform, code, ""), ErrKeyFileMissing))
}

// A recovery code made while a roll is unfinished holds both master keys, and it keeps them after the roll is done,
// because nothing can seal its slot again.  Recovering with it brings the previous key back, and the next start
// finds nothing left to move.
func TestRecoverWithACodeFromBeforeTheRollFinished(t *testing.T) {
	platform, kf, keys, rows := rollingTestInstall(t)
	databaseFile := platform.ConfigDirectory + "/bounce.db"
	b := &Bounce{platform: platform}
	code, err := b.CreateRecoveryCode("")
	assert.NoError(t, err)

	kf, keys, err = unlock(platform, databaseFile, "")
	assert.NoError(t, err)
	network := &rekeyTestnet{testnet: newTestnet()}
	rollMasterKey(kf, platform, network, databaseFile, keys)
	assertRollFinished(t, platform, keys, rows)

	assert.NoError(t, Recover(platform, code, ""))
	kf, recovered, err := unlock(platform, databaseFile, "")
	assert.NoError(t, err)
	assert.True(t, recovered.equal(keys))
	databaseKey, networkKey := rollMasterKey(kf, platform, network, databaseFile, recovered)
	assert.Equal(t, masterKeySet{master: keys.master}.databaseKey(), databaseKey)
	assert.Equal(t, masterKeySet{master: keys.master}.networkKey(), networkKey)
	assertRollFinished(t, platform, keys, rows)
}
