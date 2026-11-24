package chat

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/alecthomas/assert/v2"
)

// keyedTestDatabase writes an encrypted database under the given key the way a running install has one: in
// write-ahead mode, with everything folded into the file.  It returns the number of rows the database holds.
func keyedTestDatabase(t *testing.T, databaseFile string, databaseKey []byte) int {
	t.Helper()
	database, err := sql.Open("sqlite3", databaseFile+"?_journal_mode=WAL&_busy_timeout=10000&_txlock=immediate&_key="+sqlcipherKey(databaseKey))
	assert.NoError(t, err)
	database.SetMaxOpenConns(1)

	_, err = database.Exec("CREATE TABLE messages (id INTEGER PRIMARY KEY, text TEXT)")
	assert.NoError(t, err)
	_, err = database.Exec("CREATE INDEX messages_text ON messages (text)")
	assert.NoError(t, err)
	rows := 500
	for i := 0; i < rows; i++ {
		_, err = database.Exec("INSERT INTO messages (text) VALUES (?)", fmt.Sprintf("%s-%d", plaintextTestMarker, i))
		assert.NoError(t, err)
	}
	assert.NoError(t, database.Close())
	return rows
}

func databaseOpensUnder(databaseFile string, databaseKey []byte) bool {
	database, err := sql.Open("sqlite3", databaseFile+"?_key="+sqlcipherKey(databaseKey))
	if err != nil {
		return false
	}
	defer database.Close()
	var objects int
	return database.QueryRow("SELECT count(*) FROM sqlite_master").Scan(&objects) == nil
}

// A rekeyTestnet is the test network with a Rekey that records what it was asked for and can be made to fail
type rekeyTestnet struct {
	*testnet
	failure     error
	calls       int
	previousKey []byte
	key         []byte
}

func (n *rekeyTestnet) Rekey(_ string, previousKey []byte, key []byte) error {
	n.calls++
	n.previousKey = previousKey
	n.key = key
	return n.failure
}

// A countingSecretStore refuses every secret and counts how often it was asked to keep one
type countingSecretStore struct {
	asked int
}

func (s *countingSecretStore) Get() ([]byte, error) {
	return nil, ErrSecretNotFound
}

func (s *countingSecretStore) Store(secret []byte) error {
	s.asked++
	return errors.New("the keyring is locked")
}

// rollingTestInstall makes an install whose roll has been started and nothing more: its key file holds a new master
// key beside the previous one, and its database is still under the previous one.  The key file comes back opened.
func rollingTestInstall(t *testing.T) (Platform, *keyFile, masterKeySet, int) {
	t.Helper()
	platform := Platform{ConfigDirectory: t.TempDir(), Secrets: &testSecretStore{}}
	keys := masterKeySet{master: randomTestBytes(masterKeySize), previous: randomTestBytes(masterKeySize)}
	rows := keyedTestDatabase(t, platform.ConfigDirectory+"/bounce.db", masterKeySet{master: keys.previous}.databaseKey())

	keystoreSecret := newKeystoreSecret(platform.Secrets)
	kf := &keyFile{Version: keyFileVersion}
	assert.NoError(t, kf.wrapWith(keystoreSecret, ""))
	assert.NoError(t, kf.save(platform.ConfigDirectory, keys))
	return platform, kf, keys, rows
}

// assertRollFinished checks that an install is on the new master key alone: the database opens under its key and no
// longer under the previous one, and the key file has let the previous one go
func assertRollFinished(t *testing.T, platform Platform, keys masterKeySet, rows int) {
	t.Helper()
	databaseFile := platform.ConfigDirectory + "/bounce.db"
	rolled := masterKeySet{master: keys.master}
	assertDatabaseEncrypted(t, platform.ConfigDirectory, databaseFile, rolled.databaseKey(), rows)
	assert.False(t, databaseOpensUnder(databaseFile, masterKeySet{master: keys.previous}.databaseKey()), "the previous key still opens the database")

	_, saved, err := unlock(platform, databaseFile, "")
	assert.NoError(t, err)
	assert.True(t, saved.equal(rolled), "the key file still holds the previous master key")
}

func TestAnUnprotectedInstallMovesOntoTheKeystoreWithANewMasterKey(t *testing.T) {
	directory := t.TempDir()
	databaseFile := directory + "/bounce.db"
	_, unprotected, err := unlock(Platform{ConfigDirectory: directory}, databaseFile, "")
	assert.NoError(t, err)
	rows := keyedTestDatabase(t, databaseFile, unprotected.databaseKey())

	// The next start finds a keystore that works
	store := &testSecretStore{}
	platform := Platform{ConfigDirectory: directory, Secrets: store}
	kf, keys, err := unlock(platform, databaseFile, "")
	assert.NoError(t, err)
	assert.False(t, kf.wrapped())
	kf, keys = addKeystore(kf, platform, "", keys)
	assert.True(t, kf.KeystoreUsed)
	assert.False(t, kf.PasswordUsed)
	assert.Equal(t, unprotected.master, keys.previous, "the key the data is under is kept until it has been replaced")
	assert.Equal(t, masterKeySize, len(keys.master))
	assert.NotEqual(t, unprotected.master, keys.master)

	// What was saved is what was returned, and the old master key is no longer readable in the file
	stored, err := os.ReadFile(directory + "/" + keyFileName)
	assert.NoError(t, err)
	assert.False(t, bytes.Contains(stored, unprotected.master))
	_, saved, err := unlock(platform, databaseFile, "")
	assert.NoError(t, err)
	assert.True(t, saved.equal(keys))
	assert.Equal(t, keystoreCheckValue(store.secret), kf.KeystoreCheck)

	network := &rekeyTestnet{testnet: newTestnet()}
	databaseKey, networkKey := rollMasterKey(kf, platform, network, databaseFile, keys)
	rolled := masterKeySet{master: keys.master}
	assert.Equal(t, rolled.databaseKey(), databaseKey)
	assert.Equal(t, rolled.networkKey(), networkKey)
	assert.Equal(t, 1, network.calls)
	assert.Equal(t, unprotected.networkKey(), network.previousKey)
	assert.Equal(t, rolled.networkKey(), network.key)
	assertRollFinished(t, platform, keys, rows)
}

func TestAPasswordProtectedInstallGainsTheKeystoreAndKeepsItsMasterKey(t *testing.T) {
	directory := t.TempDir()
	databaseFile := directory + "/bounce.db"
	keys := savedTestKeyFile(t, directory, nil, "correct horse")
	noKeystore := &Bounce{platform: Platform{ConfigDirectory: directory}}
	code, err := noKeystore.CreateRecoveryCode("correct horse")
	assert.NoError(t, err)

	store := &testSecretStore{}
	platform := Platform{ConfigDirectory: directory, Secrets: store}
	kf, opened, err := unlock(platform, databaseFile, "correct horse")
	assert.NoError(t, err)
	kf, upgraded := addKeystore(kf, platform, "correct horse", opened)
	assert.True(t, kf.KeystoreUsed)
	assert.True(t, kf.PasswordUsed)
	assert.True(t, upgraded.equal(keys), "a key file that needed a password was never readable alone, so its master key stays")

	// Both are needed from now on
	_, _, err = unlock(platform, databaseFile, "")
	assert.True(t, errors.Is(err, ErrPasswordRequired))
	_, again, err := unlock(platform, databaseFile, "correct horse")
	assert.NoError(t, err)
	assert.True(t, again.equal(keys))
	_, _, err = unlock(Platform{ConfigDirectory: directory}, databaseFile, "correct horse")
	assert.True(t, errors.Is(err, ErrKeystoreSecretMissing))

	// The recovery code made before still opens it
	recoverySecret, err := parseRecoveryCode(code)
	assert.NoError(t, err)
	recovered, err := kf.openRecovery(recoverySecret)
	assert.NoError(t, err)
	assert.True(t, recovered.equal(keys))
}

func TestAnInstallIsLeftAsItIsWhenTheKeystoreCannotBeAdded(t *testing.T) {
	stores := map[string]SecretStore{
		"no keystore":           nil,
		"an unreachable one":    &testSecretStore{unavailable: errors.New("the keyring is locked")},
		"one that loses things": &forgetfulSecretStore{},
	}
	for name, store := range stores {
		t.Run(name, func(t *testing.T) {
			platform := Platform{ConfigDirectory: t.TempDir(), Secrets: store}
			_, _, err := unlock(platform, platform.ConfigDirectory+"/bounce.db", "")
			assert.NoError(t, err)
			before, err := os.ReadFile(platform.ConfigDirectory + "/" + keyFileName)
			assert.NoError(t, err)

			// A later start, which is when the keystore is tried again
			kf, keys, err := unlock(platform, platform.ConfigDirectory+"/bounce.db", "")
			assert.NoError(t, err)
			after, afterKeys := addKeystore(kf, platform, "", keys)
			assert.True(t, after == kf)
			assert.True(t, afterKeys.equal(keys))
			assert.False(t, after.wrapped())
			stored, err := os.ReadFile(platform.ConfigDirectory + "/" + keyFileName)
			assert.NoError(t, err)
			assert.Equal(t, before, stored)
		})
	}

	t.Run("a first run, which has just asked", func(t *testing.T) {
		store := &countingSecretStore{}
		platform := Platform{ConfigDirectory: t.TempDir(), Secrets: store}
		kf, keys, err := unlock(platform, platform.ConfigDirectory+"/bounce.db", "")
		assert.NoError(t, err)
		assert.Equal(t, 1, store.asked)
		addKeystore(kf, platform, "", keys)
		assert.Equal(t, 1, store.asked, "a keystore that has just refused is not asked again in the same start")

		kf, keys, err = unlock(platform, platform.ConfigDirectory+"/bounce.db", "")
		assert.NoError(t, err)
		addKeystore(kf, platform, "", keys)
		assert.Equal(t, 2, store.asked, "the next start asks again")
	})

	t.Run("an install that already uses it", func(t *testing.T) {
		store := &testSecretStore{}
		platform := Platform{ConfigDirectory: t.TempDir(), Secrets: store}
		kf, keys, err := unlock(platform, platform.ConfigDirectory+"/bounce.db", "")
		assert.NoError(t, err)
		secret := append([]byte{}, store.secret...)

		after, afterKeys := addKeystore(kf, platform, "", keys)
		assert.True(t, after == kf)
		assert.True(t, afterKeys.equal(keys))
		assert.Equal(t, secret, store.secret, "the secret the key file depends on is not replaced")
	})

	t.Run("a key file that cannot be saved", func(t *testing.T) {
		directory := t.TempDir()
		_, _, err := unlock(Platform{ConfigDirectory: directory}, directory+"/bounce.db", "")
		assert.NoError(t, err)
		before, err := os.ReadFile(directory + "/" + keyFileName)
		assert.NoError(t, err)
		assert.NoError(t, os.Mkdir(directory+"/"+keyFileName+".tmp", 0700))
		assert.NoError(t, os.WriteFile(directory+"/"+keyFileName+".tmp/occupied", []byte{}, 0600))

		store := &testSecretStore{}
		platform := Platform{ConfigDirectory: directory, Secrets: store}
		kf, keys, err := unlock(platform, directory+"/bounce.db", "")
		assert.NoError(t, err)
		after, afterKeys := addKeystore(kf, platform, "", keys)
		assert.Equal(t, keystoreSecretSize, len(store.secret), "the keystore worked, and it was the save that failed")
		assert.True(t, after == kf)
		assert.False(t, after.wrapped(), "the opened key file still describes what is on disk")
		assert.True(t, afterKeys.equal(keys), "no roll is started for a key file that was not saved")
		stored, err := os.ReadFile(directory + "/" + keyFileName)
		assert.NoError(t, err)
		assert.Equal(t, before, stored)
	})

	t.Run("an unprotected install whose last roll is unfinished", func(t *testing.T) {
		directory := t.TempDir()
		keys := masterKeySet{master: randomTestBytes(masterKeySize), previous: randomTestBytes(masterKeySize)}
		kf := &keyFile{Version: keyFileVersion}
		assert.NoError(t, kf.wrapWith(nil, ""))
		assert.NoError(t, kf.save(directory, keys))

		store := &testSecretStore{}
		after, afterKeys := addKeystore(kf, Platform{ConfigDirectory: directory, Secrets: store}, "", keys)
		assert.True(t, after == kf)
		assert.True(t, afterKeys.equal(keys))
		assert.Equal(t, 0, len(store.secret), "the keystore is not touched until the roll in progress is done")
	})
}

// A roll can be interrupted after any of its steps, and the key file holds both master keys until the last one.  The
// next start has to finish it from wherever it stopped, without redoing what would now fail.
func TestRollIsPickedUpFromWhereverItStopped(t *testing.T) {
	t.Run("before anything was done", func(t *testing.T) {
		platform, kf, keys, rows := rollingTestInstall(t)
		network := &rekeyTestnet{testnet: newTestnet()}
		rollMasterKey(kf, platform, network, platform.ConfigDirectory+"/bounce.db", keys)
		assert.Equal(t, 1, network.calls)
		assertRollFinished(t, platform, keys, rows)
	})

	t.Run("after the database", func(t *testing.T) {
		platform, kf, keys, rows := rollingTestInstall(t)
		databaseFile := platform.ConfigDirectory + "/bounce.db"
		assert.NoError(t, rekeyDatabase(databaseFile, masterKeySet{master: keys.previous}.databaseKey(), masterKeySet{master: keys.master}.databaseKey()))

		network := &rekeyTestnet{testnet: newTestnet()}
		databaseKey, _ := rollMasterKey(kf, platform, network, databaseFile, keys)
		assert.Equal(t, masterKeySet{master: keys.master}.databaseKey(), databaseKey)
		assert.Equal(t, 1, network.calls)
		assertRollFinished(t, platform, keys, rows)
	})

	// The rekey commits into the write-ahead log, and until the log is folded in the database file alone is still
	// under the previous key
	t.Run("after the rekey, before its log was folded in", func(t *testing.T) {
		platform, kf, keys, rows := rollingTestInstall(t)
		databaseFile := platform.ConfigDirectory + "/bounce.db"
		previousKey := masterKeySet{master: keys.previous}.databaseKey()
		key := masterKeySet{master: keys.master}.databaseKey()

		database, err := sql.Open("sqlite3", databaseFile+"?_busy_timeout=10000&_key="+sqlcipherKey(previousKey))
		assert.NoError(t, err)
		database.SetMaxOpenConns(1)
		var answer string
		assert.NoError(t, database.QueryRow(`PRAGMA rekey = "`+sqlcipherKey(key)+`"`).Scan(&answer))
		assert.Equal(t, "ok", answer)

		// Copied while the connection is open, as a process that was killed here leaves the files
		interrupted := Platform{ConfigDirectory: t.TempDir(), Secrets: platform.Secrets}
		for _, name := range []string{"bounce.db", "bounce.db-wal", "bounce.db-shm", keyFileName} {
			content, err := os.ReadFile(platform.ConfigDirectory + "/" + name)
			assert.NoError(t, err)
			assert.NoError(t, os.WriteFile(interrupted.ConfigDirectory+"/"+name, content, 0600))
		}
		assert.NoError(t, database.Close())
		wal, err := os.Stat(interrupted.ConfigDirectory + "/bounce.db-wal")
		assert.NoError(t, err)
		assert.True(t, wal.Size() > 0, "the interrupted install has its rekeyed pages in the log only")

		network := &rekeyTestnet{testnet: newTestnet()}
		rollMasterKey(kf, interrupted, network, interrupted.ConfigDirectory+"/bounce.db", keys)
		assertRollFinished(t, interrupted, keys, rows)
	})

	t.Run("after the network identity", func(t *testing.T) {
		platform, kf, keys, rows := rollingTestInstall(t)
		databaseFile := platform.ConfigDirectory + "/bounce.db"
		assert.NoError(t, rekeyDatabase(databaseFile, masterKeySet{master: keys.previous}.databaseKey(), masterKeySet{master: keys.master}.databaseKey()))

		// The transport is asked again, which it has to put up with, and nothing is redone to the database
		network := &rekeyTestnet{testnet: newTestnet()}
		rollMasterKey(kf, platform, network, databaseFile, keys)
		assert.Equal(t, 1, network.calls)
		assertRollFinished(t, platform, keys, rows)
	})

	t.Run("with no database yet", func(t *testing.T) {
		platform := Platform{ConfigDirectory: t.TempDir()}
		keys := masterKeySet{master: randomTestBytes(masterKeySize), previous: randomTestBytes(masterKeySize)}
		kf := &keyFile{Version: keyFileVersion}
		assert.NoError(t, kf.wrapWith(nil, "correct horse"))
		assert.NoError(t, kf.save(platform.ConfigDirectory, keys))

		databaseKey, _ := rollMasterKey(kf, platform, &rekeyTestnet{testnet: newTestnet()}, platform.ConfigDirectory+"/bounce.db", keys)
		assert.Equal(t, masterKeySet{master: keys.master}.databaseKey(), databaseKey)
		_, saved, err := unlock(platform, platform.ConfigDirectory+"/bounce.db", "correct horse")
		assert.NoError(t, err)
		assert.True(t, saved.equal(masterKeySet{master: keys.master}))
	})
}

// A step that fails must leave the install running on whatever its files are really under, and the roll to be tried
// again
func TestRollThatFailsIsLeftUnfinished(t *testing.T) {
	t.Run("the database", func(t *testing.T) {
		platform, kf, keys, _ := rollingTestInstall(t)
		databaseFile := platform.ConfigDirectory + "/bounce.db"
		previous := masterKeySet{master: keys.previous}

		// A database under neither key cannot be moved, which stands in for a rekey that failed
		good, err := os.ReadFile(databaseFile)
		assert.NoError(t, err)
		assert.NoError(t, os.Remove(databaseFile))
		keyedTestDatabase(t, databaseFile, randomTestBytes(masterKeySize))
		unusable, err := os.ReadFile(databaseFile)
		assert.NoError(t, err)

		network := &rekeyTestnet{testnet: newTestnet()}
		databaseKey, networkKey := rollMasterKey(kf, platform, network, databaseFile, keys)
		assert.Equal(t, previous.databaseKey(), databaseKey)
		assert.Equal(t, previous.networkKey(), networkKey)
		assert.Equal(t, 0, network.calls, "the network identity is not moved ahead of the database")
		after, err := os.ReadFile(databaseFile)
		assert.NoError(t, err)
		assert.Equal(t, unusable, after)
		_, saved, err := unlock(platform, databaseFile, "")
		assert.NoError(t, err)
		assert.True(t, saved.equal(keys), "the key file keeps both master keys")

		// With the database it expects back in place, the next start finishes
		assert.NoError(t, os.WriteFile(databaseFile, good, 0600))
		kf, saved, err = unlock(platform, databaseFile, "")
		assert.NoError(t, err)
		rollMasterKey(kf, platform, network, databaseFile, saved)
		assert.True(t, databaseOpensUnder(databaseFile, masterKeySet{master: keys.master}.databaseKey()))
	})

	t.Run("the network identity", func(t *testing.T) {
		platform, kf, keys, rows := rollingTestInstall(t)
		databaseFile := platform.ConfigDirectory + "/bounce.db"
		rolled := masterKeySet{master: keys.master}
		previous := masterKeySet{master: keys.previous}

		network := &rekeyTestnet{testnet: newTestnet(), failure: errors.New("the disk is full")}
		databaseKey, networkKey := rollMasterKey(kf, platform, network, databaseFile, keys)
		assert.Equal(t, rolled.databaseKey(), databaseKey, "the database was moved and is opened under its new key")
		assert.Equal(t, previous.networkKey(), networkKey, "the identity was not and is loaded under its old one")
		kf, saved, err := unlock(platform, databaseFile, "")
		assert.NoError(t, err)
		assert.True(t, saved.equal(keys), "the key file keeps both master keys")

		network.failure = nil
		databaseKey, networkKey = rollMasterKey(kf, platform, network, databaseFile, saved)
		assert.Equal(t, rolled.databaseKey(), databaseKey)
		assert.Equal(t, rolled.networkKey(), networkKey)
		assertRollFinished(t, platform, keys, rows)
	})

	t.Run("the key file", func(t *testing.T) {
		platform, kf, keys, rows := rollingTestInstall(t)
		databaseFile := platform.ConfigDirectory + "/bounce.db"
		rolled := masterKeySet{master: keys.master}
		occupied := platform.ConfigDirectory + "/" + keyFileName + ".tmp"
		assert.NoError(t, os.Mkdir(occupied, 0700))
		assert.NoError(t, os.WriteFile(occupied+"/occupied", []byte{}, 0600))

		// Everything is under the new key by then, so those are the keys to run on
		network := &rekeyTestnet{testnet: newTestnet()}
		databaseKey, networkKey := rollMasterKey(kf, platform, network, databaseFile, keys)
		assert.Equal(t, rolled.databaseKey(), databaseKey)
		assert.Equal(t, rolled.networkKey(), networkKey)
		kf, saved, err := unlock(platform, databaseFile, "")
		assert.NoError(t, err)
		assert.True(t, saved.equal(keys), "the key file on disk still holds both")

		assert.NoError(t, os.RemoveAll(occupied))
		rollMasterKey(kf, platform, network, databaseFile, saved)
		assertRollFinished(t, platform, keys, rows)
	})
}

func TestRekeyDatabaseLeavesAloneWhatIsNotItsToMove(t *testing.T) {
	directory := t.TempDir()
	databaseFile := directory + "/bounce.db"
	previousKey := randomTestBytes(masterKeySize)
	key := randomTestBytes(masterKeySize)

	assert.NoError(t, rekeyDatabase(databaseFile, previousKey, key), "a database that does not exist yet")
	_, err := os.Stat(databaseFile)
	assert.True(t, errors.Is(err, os.ErrNotExist), "and it is not created")

	rows := keyedTestDatabase(t, databaseFile, previousKey)
	assert.NoError(t, rekeyDatabase(databaseFile, previousKey, key))
	assertDatabaseEncrypted(t, directory, databaseFile, key, rows)
	assert.False(t, databaseOpensUnder(databaseFile, previousKey))

	// Asked again, with the database already where it should be
	before, err := os.ReadFile(databaseFile)
	assert.NoError(t, err)
	assert.NoError(t, rekeyDatabase(databaseFile, previousKey, key))
	after, err := os.ReadFile(databaseFile)
	assert.NoError(t, err)
	assert.Equal(t, before, after)

	assert.Error(t, rekeyDatabase(databaseFile, randomTestBytes(masterKeySize), randomTestBytes(masterKeySize)), "a database under neither key")
	after, err = os.ReadFile(databaseFile)
	assert.NoError(t, err)
	assert.Equal(t, before, after)
}

// The first password on an unprotected install is set while the engine has the database open, so the master key it
// schedules is only put to use at the next start
func TestAFirstPasswordReplacesTheMasterKeyAtTheNextStart(t *testing.T) {
	directory := t.TempDir()
	databaseFile := directory + "/bounce.db"
	platform := Platform{ConfigDirectory: directory}
	_, unprotected, err := unlock(platform, databaseFile, "")
	assert.NoError(t, err)
	rows := keyedTestDatabase(t, databaseFile, unprotected.databaseKey())
	b := &Bounce{platform: platform}
	assert.NoError(t, b.SetPassword("", "correct horse"))
	assert.True(t, databaseOpensUnder(databaseFile, unprotected.databaseKey()), "nothing is encrypted again while the engine runs")

	// The next start
	kf, keys, err := unlock(platform, databaseFile, "correct horse")
	assert.NoError(t, err)
	assert.Equal(t, unprotected.master, keys.previous)
	kf, keys = addKeystore(kf, platform, "correct horse", keys)
	rollMasterKey(kf, platform, &rekeyTestnet{testnet: newTestnet()}, databaseFile, keys)

	rolled := masterKeySet{master: keys.master}
	assertDatabaseEncrypted(t, directory, databaseFile, rolled.databaseKey(), rows)
	assert.False(t, databaseOpensUnder(databaseFile, unprotected.databaseKey()), "the key that was on disk in the clear opens nothing now")
	_, saved, err := unlock(platform, databaseFile, "correct horse")
	assert.NoError(t, err)
	assert.True(t, saved.equal(rolled))
}

// An install that is unprotected again while its roll is unfinished has nowhere to keep a third master key, so a
// password has to wait for the next start
func TestASecondRollWaitsForTheFirst(t *testing.T) {
	directory := t.TempDir()
	databaseFile := directory + "/bounce.db"
	platform := Platform{ConfigDirectory: directory}
	_, _, err := unlock(platform, databaseFile, "")
	assert.NoError(t, err)
	b := &Bounce{platform: platform}

	assert.NoError(t, b.SetPassword("", "correct horse"))
	assert.NoError(t, b.SetPassword("correct horse", ""))
	before, err := os.ReadFile(directory + "/" + keyFileName)
	assert.NoError(t, err)
	assert.True(t, errors.Is(b.SetPassword("", "battery staple"), ErrRollUnfinished))
	after, err := os.ReadFile(directory + "/" + keyFileName)
	assert.NoError(t, err)
	assert.Equal(t, before, after)

	// The next start finishes the roll, and then the password can be set
	kf, keys, err := unlock(platform, databaseFile, "")
	assert.NoError(t, err)
	rollMasterKey(kf, platform, &rekeyTestnet{testnet: newTestnet()}, databaseFile, keys)
	assert.NoError(t, b.SetPassword("", "battery staple"))
	_, keys, err = unlock(platform, databaseFile, "battery staple")
	assert.NoError(t, err)
	assert.Equal(t, masterKeySize, len(keys.previous))
}

func TestOpenReplacesTheMasterKeyBeforeItOpensTheDatabase(t *testing.T) {
	// Not t.TempDir(): an engine keeps its database and log open after Shutdown in a test binary, and Windows
	// cannot remove a directory that holds open files
	directory := os.TempDir() + "/bounce-test-" + NewID().String()
	assert.NoError(t, os.MkdirAll(directory+"/blobs", 0700))
	databaseFile := directory + "/bounce.db"
	_, unprotected, err := unlock(Platform{ConfigDirectory: directory}, databaseFile, "")
	assert.NoError(t, err)
	rows := keyedTestDatabase(t, databaseFile, unprotected.databaseKey())

	// This start finds a keystore that works
	store := &testSecretStore{}
	network := &rekeyTestnet{testnet: newTestnet()}
	engine, err := Open(newTestUI(), network, Platform{ConfigDirectory: directory, Secrets: store}, "")
	assert.NoError(t, err)
	t.Cleanup(engine.Shutdown)
	b := engine.(*Bounce)

	status, err := b.SecurityStatus()
	assert.NoError(t, err)
	assert.Equal(t, SecurityStatus{KeystoreUsed: true}, status)
	kf, err := readKeyFile(directory)
	assert.NoError(t, err)
	keys, err := kf.openPrimary(store.secret, "")
	assert.NoError(t, err)
	assert.Equal(t, 0, len(keys.previous), "the roll was finished in the same start")
	assert.NotEqual(t, unprotected.master, keys.master)
	assert.Equal(t, 1, network.calls)
	assert.Equal(t, unprotected.networkKey(), network.previousKey)
	assert.Equal(t, keys.networkKey(), network.key)

	// The engine is running on the database it had, under the new key
	var count int
	assert.NoError(t, b.database.Raw("SELECT count(*) FROM messages WHERE text LIKE ?", plaintextTestMarker+"%").Scan(&count).Error)
	assert.Equal(t, rows, count)
	assert.False(t, databaseOpensUnder(databaseFile, unprotected.databaseKey()))
}
