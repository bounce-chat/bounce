package chat

import (
	"crypto/rand"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// ErrInstallUnwrapped is returned when a recovery code is asked for on an install that has no keystore and no
// password.  Its key file can be read by anyone who has it, so there is nothing a recovery code would recover.
var ErrInstallUnwrapped = errors.New("this install has no keystore and no password, so it has no use for a recovery code")

// ErrRollUnfinished is returned when a first password is set on an unprotected install whose master key is still
// being replaced.  Starting Bounce again finishes the replacement, and the password can be set after that.
var ErrRollUnfinished = errors.New("the master key is still being replaced, which finishes the next time Bounce starts")

// The key file is read, changed and saved again as one step, and two of those at once would lose one of the changes
var keyFileMutex sync.Mutex

// SecurityStatus says how an install is protected, for a user interface's security settings
type SecurityStatus struct {
	// The master key is wrapped by a secret in the operating system's keystore, so the install opens on this device
	// and for this user only
	KeystoreUsed bool

	// A password is set, and Open needs it
	PasswordSet bool

	// When the recovery code was made, in seconds since the Unix epoch, and zero when there is none.  Nothing else
	// about a recovery code is kept.
	RecoveryCodeCreatedAt int64
}

func (b *Bounce) SecurityStatus() (SecurityStatus, error) {
	keyFileMutex.Lock()
	defer keyFileMutex.Unlock()

	kf, err := readKeyFile(b.platform.ConfigDirectory)
	if err != nil {
		return SecurityStatus{}, err
	}
	return SecurityStatus{
		KeystoreUsed:          kf.KeystoreUsed,
		PasswordSet:           kf.PasswordUsed,
		RecoveryCodeCreatedAt: kf.RecoveryCreatedAt,
	}, nil
}

// SetPassword sets, changes or removes the password Open asks for.  currentPassword is the one in use, empty when
// there is none, and newPassword is the one wanted, empty to remove it.  When currentPassword is not the password in
// use nothing is changed and the error is ErrWrongPassword, or ErrPasswordRequired when it was left empty.
//
// A password only changes how the master key is wrapped, so it takes effect at once and nothing is encrypted again.
// The first password on an install that had no protection is the exception: its master key has been on disk in the
// clear, so a new one is put beside it here and the next start moves everything over to it.
//
// Removing the password from an install with no keystore leaves it with no protection, and its recovery code is
// dropped with it.
func (b *Bounce) SetPassword(currentPassword string, newPassword string) error {
	keyFileMutex.Lock()
	defer keyFileMutex.Unlock()

	kf, err := readKeyFile(b.platform.ConfigDirectory)
	if err != nil {
		return err
	}
	keys, err := openKeyFile(kf, b.platform, currentPassword)
	if err != nil {
		return err
	}
	if newPassword == "" && !kf.PasswordUsed {
		return nil
	}

	if !kf.wrapped() {
		// A key set has room for one previous key
		if keys.previous != nil {
			return ErrRollUnfinished
		}
		keys = masterKeySet{master: make([]byte, masterKeySize), previous: keys.master}
		_, err = rand.Read(keys.master)
		if err != nil {
			return err
		}
	}

	err = kf.wrapWith(kf.keystoreSecret, newPassword)
	if err != nil {
		return err
	}
	if !kf.wrapped() {
		kf.dropRecovery()
	}
	return kf.save(b.platform.ConfigDirectory, keys)
}

// CreateRecoveryCode makes a recovery code for the install and returns it to be shown to the user, once: it is not
// kept, so it cannot be shown again.  A new code replaces the one before it, which stops working.  password is the
// install's password when it has one, asked for again because the code opens everything the password does.
//
// An install with no keystore and no password gets ErrInstallUnwrapped.
func (b *Bounce) CreateRecoveryCode(password string) (string, error) {
	keyFileMutex.Lock()
	defer keyFileMutex.Unlock()

	kf, err := readKeyFile(b.platform.ConfigDirectory)
	if err != nil {
		return "", err
	}
	if !kf.wrapped() {
		return "", ErrInstallUnwrapped
	}
	keys, err := openKeyFile(kf, b.platform, password)
	if err != nil {
		return "", err
	}

	code, recoverySecret, err := newRecoveryCode()
	if err != nil {
		return "", err
	}
	err = kf.sealRecovery(keys, recoverySecret, time.Now().Unix())
	if err != nil {
		return "", err
	}
	err = kf.save(b.platform.ConfigDirectory, keys)
	if err != nil {
		return "", err
	}
	return code, nil
}

// Recover opens an install with its recovery code and wraps its master key afresh, for a user who has forgotten
// the password or a device whose keystore has lost the install's secret.  It works on an install that is not open,
// and Open is called afterwards with newPassword, which may be empty for no password.
//
// The keystore is set up as on a first run, with a new secret, and left out when it cannot be used.  The recovery
// code keeps working afterwards, unless the install ends up with no keystore and no password, where the code is
// dropped because there is nothing left for it to recover.
//
// ErrMistypedRecoveryCode means the code was not copied correctly, ErrWrongRecoveryCode that it is a code for some
// other install or an earlier code for this one, and ErrNoRecoveryCode that the install never had one.
func Recover(platform Platform, recoveryCode string, newPassword string) error {
	recoverySecret, err := parseRecoveryCode(recoveryCode)
	if err != nil {
		return err
	}

	// The instance lock, so that the key file is never changed under an engine that has the install open
	b := &Bounce{platform: platform}
	b.ensureOnlyOneInstance()
	defer b.releaseInstance()

	kf, err := readKeyFile(platform.ConfigDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return ErrKeyFileMissing
	}
	if err != nil {
		return err
	}
	keys, err := kf.openRecovery(recoverySecret)
	if err != nil {
		return err
	}

	err = kf.wrapWith(newKeystoreSecret(platform.Secrets), newPassword)
	if err != nil {
		return err
	}
	if !kf.wrapped() {
		kf.dropRecovery()
	}
	return kf.save(platform.ConfigDirectory, keys)
}

// Reset deletes everything an install keeps on this device, for an install that cannot be opened and has nothing
// left to open it with.  The key file goes first, which alone puts the database and the network identity out of
// reach, and then everything else in the config directory except the log.  The Open that follows is a first run,
// with a new identity that no contact and none of the user's other devices knows.
//
// It works on an install that is not open.  A secret the install had in the keystore of the operating system is
// left there, where it opens nothing any more.
func Reset(platform Platform) error {
	// The instance lock, so that nothing is deleted under an engine that has the install open
	b := &Bounce{platform: platform}
	b.ensureOnlyOneInstance()
	defer b.releaseInstance()

	err := os.Remove(platform.ConfigDirectory + "/" + keyFileName)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	entries, err := os.ReadDir(platform.ConfigDirectory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		// The lock is released on the way out, and the log is open in this process and records the reset
		if entry.Name() == ".pid" || strings.HasPrefix(entry.Name(), "bounce-log") {
			continue
		}
		err = os.RemoveAll(platform.ConfigDirectory + "/" + entry.Name())
		if err != nil {
			return err
		}
	}
	log.Warn("this install was reset, and everything it kept on this device was deleted")

	// The directory is left the way the platform package hands it over
	return os.MkdirAll(platform.ConfigDirectory+"/blobs/", 0700)
}
