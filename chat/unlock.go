package chat

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	log "github.com/sirupsen/logrus"
)

// The reasons an install cannot be opened that a user interface is expected to do something about.  Open and Recover
// return them, sometimes wrapped with more detail, so they are matched with errors.Is.
var ErrPasswordRequired = errors.New("this install is protected by a password")
var ErrWrongPassword = errors.New("the password is wrong")
var ErrKeystoreSecretMissing = errors.New("the keystore does not hold this install's secret")
var ErrKeystoreUnavailable = errors.New("the keystore could not be reached")
var ErrKeyFileMissing = errors.New("the database is encrypted and its key file is missing")

const databaseKeyContext = "bounce.chat 2026-11-23 database key"
const networkKeyContext = "bounce.chat 2026-11-23 network identity key"

func (keys masterKeySet) databaseKey() []byte {
	return deriveKey(databaseKeyContext, keys.master)
}

func (keys masterKeySet) networkKey() []byte {
	return deriveKey(networkKeyContext, keys.master)
}

// sqlcipherKey writes a database key the way SQLCipher takes a raw key, which it then uses as it is instead of
// deriving one from a passphrase.  The result holds the key, so it must never reach a log line.
func sqlcipherKey(databaseKey []byte) string {
	return "x'" + hex.EncodeToString(databaseKey) + "'"
}

type databaseState int

const (
	databaseMissing databaseState = iota
	databasePlaintext
	databaseEncrypted
)

// Every SQLite database that is not encrypted starts with these bytes, and an encrypted one starts with a random salt
const plaintextDatabaseHeader = "SQLite format 3\x00"

func readDatabaseState(databaseFile string) (databaseState, error) {
	file, err := os.Open(databaseFile)
	if errors.Is(err, os.ErrNotExist) {
		return databaseMissing, nil
	}
	if err != nil {
		return databaseMissing, err
	}
	defer file.Close()

	header := make([]byte, len(plaintextDatabaseHeader))
	read, err := io.ReadFull(file, header)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return databaseMissing, err
	}
	// An empty file is a database that has yet to be created, to SQLite as well
	if read == 0 {
		return databaseMissing, nil
	}
	if err == nil && string(header) == plaintextDatabaseHeader {
		return databasePlaintext, nil
	}
	return databaseEncrypted, nil
}

// unlock returns the master keys of the install on the given platform, whose database is the given file.  On the
// first run it generates them, and for an install from before the database was encrypted it generates them and then
// encrypts the database.  What it does is decided by which of the key file and the database exist:
//
//	key file   database     what happens
//	missing    missing      a first run: the keys are generated
//	missing    plaintext    the keys are generated and the database is encrypted
//	missing    encrypted    ErrKeyFileMissing, because a new key would only hide that the old one is gone
//	present    encrypted    the key file is opened
//	present    missing      the key file is opened, and the caller creates a new database
//	present    plaintext    the key file is opened and the interrupted encryption is finished
//
// It is called with the instance lock held, so that two processes can never both decide this is a first run.
func unlock(platform Platform, databaseFile string, password string) (masterKeySet, error) {
	state, err := readDatabaseState(databaseFile)
	if err != nil {
		return masterKeySet{}, err
	}

	var keys masterKeySet
	kf, err := readKeyFile(platform.ConfigDirectory)
	if errors.Is(err, os.ErrNotExist) {
		if state == databaseEncrypted {
			return masterKeySet{}, ErrKeyFileMissing
		}
		keys, err = createKeyFile(platform)
	} else if err == nil {
		keys, err = openKeyFile(kf, platform, password)
	}
	if err != nil {
		return masterKeySet{}, err
	}

	if state == databasePlaintext {
		err = encryptDatabase(databaseFile, keys.databaseKey())
		if err != nil {
			return masterKeySet{}, fmt.Errorf("the database could not be encrypted: %w", err)
		}
	}
	return keys, nil
}

// newKeystoreSecret generates a secret, stores it in the keystore and reads it back.  It returns nil when the
// platform has no keystore or the keystore cannot be used right now, and the install then has to do without one.
// Reading the secret back before anything is wrapped with it is what guarantees that a key file never depends on a
// secret the keystore did not keep.
func newKeystoreSecret(secrets SecretStore) []byte {
	if secrets == nil {
		return nil
	}

	keystoreSecret := make([]byte, keystoreSecretSize)
	_, err := rand.Read(keystoreSecret)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error generating a keystore secret")
		return nil
	}
	err = secrets.Store(keystoreSecret)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Warn("the keystore did not accept a secret")
		return nil
	}
	stored, err := secrets.Get()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Warn("the keystore did not return the secret it was just given")
		return nil
	}
	if !bytes.Equal(stored, keystoreSecret) {
		log.Warn("the keystore returned a different secret from the one it was just given")
		return nil
	}
	return keystoreSecret
}

func createKeyFile(platform Platform) (masterKeySet, error) {
	keys := masterKeySet{master: make([]byte, masterKeySize)}
	_, err := rand.Read(keys.master)
	if err != nil {
		return masterKeySet{}, err
	}

	keystoreSecret := newKeystoreSecret(platform.Secrets)
	kf := &keyFile{Version: keyFileVersion}
	err = kf.wrapWith(keystoreSecret, "")
	if err != nil {
		return masterKeySet{}, err
	}
	err = kf.save(platform.ConfigDirectory, keys)
	if err != nil {
		return masterKeySet{}, err
	}
	if !kf.wrapped() {
		log.Warn("no keystore could be used, so the data in this install is not protected on this device")
	}
	return keys, nil
}

// keystoreSecretFor returns the keystore secret a key file is wrapped with.  A keystore that could not be asked is
// ErrKeystoreUnavailable, which is worth trying again.  A keystore that answered with no secret, or with a secret
// other than the one the key file was wrapped with, is ErrKeystoreSecretMissing, which only a recovery code gets past.
func keystoreSecretFor(kf *keyFile, secrets SecretStore) ([]byte, error) {
	if secrets == nil {
		return nil, ErrKeystoreSecretMissing
	}

	keystoreSecret, err := secrets.Get()
	if errors.Is(err, ErrSecretNotFound) {
		return nil, ErrKeystoreSecretMissing
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeystoreUnavailable, err)
	}
	if !bytes.Equal(keystoreCheckValue(keystoreSecret), kf.KeystoreCheck) {
		return nil, ErrKeystoreSecretMissing
	}
	return keystoreSecret, nil
}

// openKeyFile opens the primary slot of an existing key file.  The keystore is asked before the password is, so a
// user is never asked for a password that could not open the install anyway.
func openKeyFile(kf *keyFile, platform Platform, password string) (masterKeySet, error) {
	var keystoreSecret []byte
	if kf.KeystoreUsed {
		var err error
		keystoreSecret, err = keystoreSecretFor(kf, platform.Secrets)
		if err != nil {
			return masterKeySet{}, err
		}
	}
	if kf.PasswordUsed && password == "" {
		return masterKeySet{}, ErrPasswordRequired
	}

	keys, err := kf.openPrimary(keystoreSecret, password)
	if errors.Is(err, errSlotNotOpened) {
		// The keystore secret was checked above, so with a password in use the password is what was wrong.
		// Without one, nothing the user typed is involved and the file itself has been changed.
		if kf.PasswordUsed {
			return masterKeySet{}, ErrWrongPassword
		}
		return masterKeySet{}, fmt.Errorf("%w: the primary slot does not open", ErrKeyFileDamaged)
	}
	if err != nil {
		return masterKeySet{}, err
	}

	if !kf.wrapped() {
		log.Warn("the data in this install is not protected on this device: it has no keystore and no password")
	}
	return keys, nil
}

// encryptDatabase replaces a plaintext database with an encrypted copy of itself.  The copy is made beside the
// database, opened again and checked, and only then renamed over it, so until that last step the plaintext database
// is untouched and an interruption costs nothing but doing it again.
func encryptDatabase(databaseFile string, databaseKey []byte) error {
	log.Info("encrypting the database, which is done once and can take a moment")

	encryptedFile := databaseFile + ".encrypting"
	for _, leftover := range []string{encryptedFile, encryptedFile + "-journal", encryptedFile + "-wal", encryptedFile + "-shm"} {
		err := os.Remove(leftover)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	plaintext, err := sql.Open("sqlite3", databaseFile+"?_busy_timeout=10000")
	if err != nil {
		return err
	}
	plaintext.SetMaxOpenConns(1)

	var objects int
	err = plaintext.QueryRow("SELECT count(*) FROM sqlite_master").Scan(&objects)
	if err != nil {
		plaintext.Close()
		return err
	}
	_, err = plaintext.Exec("ATTACH DATABASE ? AS encrypted KEY ?", encryptedFile, sqlcipherKey(databaseKey))
	if err != nil {
		plaintext.Close()
		return err
	}
	_, err = plaintext.Exec("SELECT sqlcipher_export('encrypted')")
	if err != nil {
		plaintext.Close()
		return err
	}
	_, err = plaintext.Exec("DETACH DATABASE encrypted")
	if err != nil {
		plaintext.Close()
		return err
	}
	// Fold the write-ahead log into the plaintext database, so that it is whole on its own if this is interrupted
	// after the log has been removed below
	_, err = plaintext.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	if err != nil {
		plaintext.Close()
		return err
	}
	err = plaintext.Close()
	if err != nil {
		return err
	}

	err = checkEncryptedCopy(encryptedFile, databaseKey, objects)
	if err != nil {
		os.Remove(encryptedFile)
		return err
	}

	// Anything left of the plaintext database's log would sit beside the encrypted database in the clear
	for _, leftover := range []string{databaseFile + "-wal", databaseFile + "-shm"} {
		err = os.Remove(leftover)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	err = os.Rename(encryptedFile, databaseFile)
	if err != nil {
		return err
	}
	log.Info("the database is now encrypted")
	return nil
}

// checkEncryptedCopy opens a freshly encrypted copy of a database on its own and confirms that it is encrypted under
// the key, that every page of it authenticates, and that it holds as many tables and indexes as the original
func checkEncryptedCopy(encryptedFile string, databaseKey []byte, objects int) error {
	encrypted, err := sql.Open("sqlite3", encryptedFile+"?_busy_timeout=10000&_key="+sqlcipherKey(databaseKey))
	if err != nil {
		return err
	}
	encrypted.SetMaxOpenConns(1)

	var cipherStatus string
	err = encrypted.QueryRow("PRAGMA cipher_status").Scan(&cipherStatus)
	if err != nil {
		encrypted.Close()
		return err
	}
	if cipherStatus != "1" {
		encrypted.Close()
		return errors.New("the encrypted copy of the database is not encrypted")
	}

	// The check returns one row for every page that fails, and none for a sound database
	failures, err := encrypted.Query("PRAGMA cipher_integrity_check")
	if err != nil {
		encrypted.Close()
		return err
	}
	damaged := failures.Next()
	err = failures.Err()
	failures.Close()
	if err != nil {
		encrypted.Close()
		return err
	}
	if damaged {
		encrypted.Close()
		return errors.New("the encrypted copy of the database is damaged")
	}

	var copied int
	err = encrypted.QueryRow("SELECT count(*) FROM sqlite_master").Scan(&copied)
	if err != nil {
		encrypted.Close()
		return err
	}
	if copied != objects {
		encrypted.Close()
		return fmt.Errorf("the encrypted copy of the database holds %d tables and indexes, not %d", copied, objects)
	}
	return encrypted.Close()
}
