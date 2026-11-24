package chat

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"

	log "github.com/sirupsen/logrus"
)

// addKeystore wraps the key file of an install that does without the keystore by a new keystore secret, when the
// platform has a keystore and it works now.  It is how an install that started on a device with a locked or missing
// keyring becomes protected once the keyring is there, with nothing asked of the user.  It returns the key file and
// the key set to carry on with, which are the ones it was given when nothing changed.
//
// An install that was unwrapped has had its master key on disk in the clear, and a copy of the key file taken then
// opens the database with nothing else needed.  So leaving that state also replaces the master key: the key set that
// is saved and returned holds a new one, with the old one beside it as the previous key until rollMasterKey has
// re-encrypted everything.  An install that had a password keeps its master key, because its key file was never
// readable on its own.
//
// Nothing here stops the install from opening.  When the keystore cannot be used, or the new key file cannot be
// saved, the install stays as it was and the next start tries again.
func addKeystore(kf *keyFile, platform Platform, password string, keys masterKeySet) (*keyFile, masterKeySet) {
	if kf.KeystoreUsed || platform.Secrets == nil {
		return kf, keys
	}
	// A first run has just asked the keystore and been turned down, and asking again would only show a locked
	// keyring's prompt a second time
	if kf.keystoreTried {
		return kf, keys
	}
	// A key set has room for one previous key, so an unwrapped install whose last roll is unfinished waits for that
	// roll before it starts another
	if !kf.wrapped() && keys.previous != nil {
		return kf, keys
	}

	keystoreSecret := newKeystoreSecret(platform.Secrets)
	if keystoreSecret == nil {
		return kf, keys
	}

	upgradedKeys := keys
	if !kf.wrapped() {
		upgradedKeys = masterKeySet{master: make([]byte, masterKeySize), previous: keys.master}
		_, err := rand.Read(upgradedKeys.master)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error generating a master key")
			return kf, keys
		}
	}
	if !kf.PasswordUsed {
		password = ""
	}

	// The change is made to a copy, so that a save that fails leaves the opened key file describing what is on disk
	upgraded := *kf
	err := upgraded.wrapWith(keystoreSecret, password)
	if err == nil {
		err = upgraded.save(platform.ConfigDirectory, upgradedKeys)
	}
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("the key file could not be wrapped by the keystore, which will be tried again at the next start")
		return kf, keys
	}
	log.Info("this install is now protected by this device's keystore")
	return &upgraded, upgradedKeys
}

// rollMasterKey finishes replacing the master key.  It re-encrypts the database and has the transport seal its
// identity again, both under the keys derived from the new master key, and then saves the key file without the
// previous one.  Open runs it whenever the key set holds a previous key, while nothing else has the database open, so
// a roll that was interrupted or that failed is picked up at the next start from wherever it stopped: each step
// first finds out whether it has been done already.
//
// It returns the keys the database and the network identity are under when it is done.  A step that fails leaves the
// roll unfinished, and whatever was not re-encrypted is still under the key derived from the previous master key, so
// that is the key returned for it and the engine runs as it would have without the roll.
func rollMasterKey(kf *keyFile, platform Platform, network Network, databaseFile string, keys masterKeySet) ([]byte, []byte) {
	rolled := masterKeySet{master: keys.master}
	previous := masterKeySet{master: keys.previous}

	err := rekeyDatabase(databaseFile, previous.databaseKey(), rolled.databaseKey())
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("the database could not be moved to the new master key, which will be tried again at the next start")
		return previous.databaseKey(), previous.networkKey()
	}

	err = network.Rekey(platform.ConfigDirectory, previous.networkKey(), rolled.networkKey())
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("the network identity could not be moved to the new master key, which will be tried again at the next start")
		return rolled.databaseKey(), previous.networkKey()
	}

	// Nothing is under the previous master key any more, so it can go
	err = kf.save(platform.ConfigDirectory, rolled)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("the key file could not be saved without the previous master key, which will be tried again at the next start")
	}
	return rolled.databaseKey(), rolled.networkKey()
}

// rekeyDatabase moves a database from one key to another, and does nothing to a database that is already under the
// new key or does not exist yet.  It must have the database to itself: a connection opened under the old key stops
// working the moment the key changes.
//
// SQLCipher re-encrypts every page inside one transaction, so the database is under the old key until that commits
// and under the new one after, and an interruption leaves one or the other.  In write-ahead mode the re-encrypted
// pages are in the log at that point and the database file itself still opens under the old key, which is why the
// log is folded in before the roll is counted as done.  It needs as much free disk as the database is large.
func rekeyDatabase(databaseFile string, previousKey []byte, key []byte) error {
	state, err := readDatabaseState(databaseFile)
	if err != nil {
		return err
	}
	if state == databaseMissing {
		return nil
	}

	var objects int
	rekeyed, err := sql.Open("sqlite3", databaseFile+"?_busy_timeout=10000&_key="+sqlcipherKey(key))
	if err != nil {
		return err
	}
	rekeyed.SetMaxOpenConns(1)
	err = rekeyed.QueryRow("SELECT count(*) FROM sqlite_master").Scan(&objects)
	if err == nil {
		// Already under the new key.  A roll that stopped right after the rekey left it in the log only.
		_, err = rekeyed.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
		if err != nil {
			rekeyed.Close()
			return err
		}
		return rekeyed.Close()
	}
	rekeyed.Close()

	database, err := sql.Open("sqlite3", databaseFile+"?_busy_timeout=10000&_key="+sqlcipherKey(previousKey))
	if err != nil {
		return err
	}
	database.SetMaxOpenConns(1)
	err = database.QueryRow("SELECT count(*) FROM sqlite_master").Scan(&objects)
	if err != nil {
		database.Close()
		return fmt.Errorf("the database opens under neither master key: %w", err)
	}

	// A pragma takes no bound parameters, so the key is written into the statement, which goes to the driver and
	// nowhere else.  A rekey that worked answers "ok", and anything else is treated as one that did not.
	var answer string
	err = database.QueryRow(`PRAGMA rekey = "` + sqlcipherKey(key) + `"`).Scan(&answer)
	if err != nil {
		database.Close()
		return err
	}
	if answer != "ok" {
		database.Close()
		return errors.New("the database did not confirm its new key")
	}
	_, err = database.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	if err != nil {
		database.Close()
		return err
	}
	return database.Close()
}
