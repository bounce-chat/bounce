package chat

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/alecthomas/assert/v2"
)

const plaintextTestMarker = "a-row-that-must-not-be-readable-on-disk"

// plaintextTestDatabase writes a database the way an install from before encryption has one: no key, and with a
// write-ahead log that was never folded back in when wal is set, as a process that was killed leaves it.  It returns
// the number of rows the database holds.
func plaintextTestDatabase(t *testing.T, databaseFile string, wal bool) int {
	t.Helper()
	source := t.TempDir() + "/source.db"
	dsn := source + "?_busy_timeout=10000"
	if wal {
		dsn += "&_journal_mode=WAL&_txlock=immediate"
	}
	database, err := sql.Open("sqlite3", dsn)
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

	// Copied while the connection is open, so the log is still beside the database
	for _, suffix := range []string{"", "-wal", "-shm"} {
		content, err := os.ReadFile(source + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		assert.NoError(t, err)
		assert.NoError(t, os.WriteFile(databaseFile+suffix, content, 0600))
	}
	assert.NoError(t, database.Close())

	if wal {
		info, err := os.Stat(databaseFile + "-wal")
		assert.NoError(t, err)
		assert.True(t, info.Size() > 0, "the test database has rows that are only in its log")
	}
	return rows
}

// assertDatabaseEncrypted checks that a database opens under the key with every row in it, and that nothing in its
// directory holds a row in the clear
func assertDatabaseEncrypted(t *testing.T, directory string, databaseFile string, databaseKey []byte, rows int) {
	t.Helper()
	state, err := readDatabaseState(databaseFile)
	assert.NoError(t, err)
	assert.Equal(t, databaseEncrypted, state)

	database, err := sql.Open("sqlite3", databaseFile+"?_key="+sqlcipherKey(databaseKey))
	assert.NoError(t, err)
	var count int
	assert.NoError(t, database.QueryRow("SELECT count(*) FROM messages WHERE text LIKE ?", plaintextTestMarker+"%").Scan(&count))
	assert.Equal(t, rows, count)
	var indexes int
	assert.NoError(t, database.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'messages_text'").Scan(&indexes))
	assert.Equal(t, 1, indexes)
	assert.NoError(t, database.Close())

	unkeyed, err := sql.Open("sqlite3", databaseFile)
	assert.NoError(t, err)
	assert.Error(t, unkeyed.QueryRow("SELECT count(*) FROM messages").Scan(&count), "the database opens without a key")
	unkeyed.Close()

	entries, err := os.ReadDir(directory)
	assert.NoError(t, err)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		content, err := os.ReadFile(directory + "/" + entry.Name())
		assert.NoError(t, err)
		assert.False(t, bytes.Contains(content, []byte(plaintextTestMarker)), "%s holds a row in the clear", entry.Name())
		assert.False(t, strings.Contains(entry.Name(), ".encrypting"), "%s was left behind", entry.Name())
	}
}

func TestUnlockOnAFirstRunGeneratesTheKeys(t *testing.T) {
	directory := t.TempDir()
	store := &testSecretStore{}
	platform := Platform{ConfigDirectory: directory, Secrets: store}

	keys, err := unlock(platform, directory+"/bounce.db", "")
	assert.NoError(t, err)
	assert.Equal(t, masterKeySize, len(keys.master))
	assert.Equal(t, 0, len(keys.previous))
	assert.NotEqual(t, keys.databaseKey(), keys.networkKey())
	assert.NotEqual(t, keys.master, keys.databaseKey())

	kf, err := readKeyFile(directory)
	assert.NoError(t, err)
	assert.True(t, kf.KeystoreUsed)
	assert.False(t, kf.PasswordUsed)
	assert.Equal(t, keystoreSecretSize, len(store.secret))
	assert.Equal(t, keystoreCheckValue(store.secret), kf.KeystoreCheck)

	// The second start opens what the first one made, with or without a database beside it
	again, err := unlock(platform, directory+"/bounce.db", "")
	assert.NoError(t, err)
	assert.True(t, again.equal(keys))
}

func TestUnlockWithoutAKeystoreLeavesTheKeyFileUnwrapped(t *testing.T) {
	stores := map[string]SecretStore{
		"no keystore":           nil,
		"an unreachable one":    &testSecretStore{unavailable: errors.New("the keyring is locked")},
		"one that loses things": &forgetfulSecretStore{},
	}
	for name, store := range stores {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			platform := Platform{ConfigDirectory: directory, Secrets: store}
			keys, err := unlock(platform, directory+"/bounce.db", "")
			assert.NoError(t, err)

			kf, err := readKeyFile(directory)
			assert.NoError(t, err)
			assert.False(t, kf.wrapped())

			again, err := unlock(platform, directory+"/bounce.db", "")
			assert.NoError(t, err)
			assert.True(t, again.equal(keys))
		})
	}
}

// A forgetfulSecretStore accepts a secret and then returns another, which is what the read back on a first run is for
type forgetfulSecretStore struct{}

func (s *forgetfulSecretStore) Get() ([]byte, error) {
	return randomTestBytes(keystoreSecretSize), nil
}

func (s *forgetfulSecretStore) Store(secret []byte) error {
	return nil
}

func TestUnlockEncryptsAnExistingDatabase(t *testing.T) {
	for name, wal := range map[string]bool{"with a write-ahead log": true, "with a rollback journal": false} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			databaseFile := directory + "/bounce.db"
			rows := plaintextTestDatabase(t, databaseFile, wal)
			state, err := readDatabaseState(databaseFile)
			assert.NoError(t, err)
			assert.Equal(t, databasePlaintext, state)

			platform := Platform{ConfigDirectory: directory, Secrets: &testSecretStore{}}
			keys, err := unlock(platform, databaseFile, "")
			assert.NoError(t, err)
			assertDatabaseEncrypted(t, directory, databaseFile, keys.databaseKey(), rows)

			// The next start has nothing left to do
			again, err := unlock(platform, databaseFile, "")
			assert.NoError(t, err)
			assert.True(t, again.equal(keys))
			assertDatabaseEncrypted(t, directory, databaseFile, keys.databaseKey(), rows)
		})
	}
}

// An interruption before the encrypted copy replaces the database leaves the key file beside a plaintext database and
// whatever had been written of the copy
func TestUnlockFinishesAnInterruptedEncryption(t *testing.T) {
	directory := t.TempDir()
	databaseFile := directory + "/bounce.db"
	platform := Platform{ConfigDirectory: directory, Secrets: &testSecretStore{}}
	keys, err := unlock(platform, databaseFile, "")
	assert.NoError(t, err)

	rows := plaintextTestDatabase(t, databaseFile, true)
	assert.NoError(t, os.WriteFile(databaseFile+".encrypting", []byte("half of an encrypted copy"), 0600))
	assert.NoError(t, os.WriteFile(databaseFile+".encrypting-journal", []byte("and its journal"), 0600))

	again, err := unlock(platform, databaseFile, "")
	assert.NoError(t, err)
	assert.True(t, again.equal(keys), "the keys made before the interruption are the ones used")
	assertDatabaseEncrypted(t, directory, databaseFile, keys.databaseKey(), rows)
}

func TestUnlockNeverGeneratesAKeyOverAnEncryptedDatabase(t *testing.T) {
	directory := t.TempDir()
	databaseFile := directory + "/bounce.db"
	store := &testSecretStore{}
	platform := Platform{ConfigDirectory: directory, Secrets: store}
	rows := plaintextTestDatabase(t, databaseFile, true)
	keys, err := unlock(platform, databaseFile, "")
	assert.NoError(t, err)
	secret := append([]byte{}, store.secret...)

	assert.NoError(t, os.Remove(directory+"/"+keyFileName))
	_, err = unlock(platform, databaseFile, "")
	assert.True(t, errors.Is(err, ErrKeyFileMissing))

	_, err = os.Stat(directory + "/" + keyFileName)
	assert.True(t, errors.Is(err, os.ErrNotExist), "no key file is made in the lost one's place")
	assert.Equal(t, secret, store.secret, "the keystore secret is left alone")
	assertDatabaseEncrypted(t, directory, databaseFile, keys.databaseKey(), rows)
}

func TestUnlockTellsALostKeystoreSecretFromAnUnreachableKeystore(t *testing.T) {
	directory := t.TempDir()
	databaseFile := directory + "/bounce.db"
	store := &testSecretStore{}
	platform := Platform{ConfigDirectory: directory, Secrets: store}
	keys, err := unlock(platform, databaseFile, "")
	assert.NoError(t, err)
	before, err := os.ReadFile(directory + "/" + keyFileName)
	assert.NoError(t, err)
	secret := append([]byte{}, store.secret...)

	store.unavailable = errors.New("the keyring is locked")
	_, err = unlock(platform, databaseFile, "")
	assert.True(t, errors.Is(err, ErrKeystoreUnavailable))
	assert.True(t, strings.Contains(err.Error(), "the keyring is locked"), "the reason is kept for the log")
	store.unavailable = nil

	store.secret = nil
	_, err = unlock(platform, databaseFile, "")
	assert.True(t, errors.Is(err, ErrKeystoreSecretMissing), "the keystore answered and has no secret")

	store.secret = randomTestBytes(keystoreSecretSize)
	_, err = unlock(platform, databaseFile, "")
	assert.True(t, errors.Is(err, ErrKeystoreSecretMissing), "the keystore holds another install's secret")

	_, err = unlock(Platform{ConfigDirectory: directory}, databaseFile, "")
	assert.True(t, errors.Is(err, ErrKeystoreSecretMissing), "the platform has no keystore at all")

	// None of those failures fell back to anything: the key file is as it was, and the right secret still opens it
	after, err := os.ReadFile(directory + "/" + keyFileName)
	assert.NoError(t, err)
	assert.Equal(t, before, after)
	store.secret = secret
	again, err := unlock(platform, databaseFile, "")
	assert.NoError(t, err)
	assert.True(t, again.equal(keys))
}

func TestUnlockAsksForThePasswordAndChecksIt(t *testing.T) {
	directory := t.TempDir()
	databaseFile := directory + "/bounce.db"
	store := &testSecretStore{}
	platform := Platform{ConfigDirectory: directory, Secrets: store}
	keys, err := unlock(platform, databaseFile, "")
	assert.NoError(t, err)

	kf, err := readKeyFile(directory)
	assert.NoError(t, err)
	assert.NoError(t, kf.wrapWith(store.secret, "correct horse"))
	assert.NoError(t, kf.save(directory, keys))

	_, err = unlock(platform, databaseFile, "")
	assert.True(t, errors.Is(err, ErrPasswordRequired))
	_, err = unlock(platform, databaseFile, "wrong horse")
	assert.True(t, errors.Is(err, ErrWrongPassword))
	again, err := unlock(platform, databaseFile, "correct horse")
	assert.NoError(t, err)
	assert.True(t, again.equal(keys))

	// A lost keystore secret is reported before the password is asked for, since no password could help
	secret := store.secret
	store.secret = nil
	_, err = unlock(platform, databaseFile, "")
	assert.True(t, errors.Is(err, ErrKeystoreSecretMissing))
	_, err = unlock(platform, databaseFile, "correct horse")
	assert.True(t, errors.Is(err, ErrKeystoreSecretMissing))
	store.secret = secret

	// A password alone, on a device with no keystore
	assert.NoError(t, kf.wrapWith(nil, "correct horse"))
	assert.NoError(t, kf.save(directory, keys))
	noKeystore := Platform{ConfigDirectory: directory}
	_, err = unlock(noKeystore, databaseFile, "")
	assert.True(t, errors.Is(err, ErrPasswordRequired))
	_, err = unlock(noKeystore, databaseFile, "wrong horse")
	assert.True(t, errors.Is(err, ErrWrongPassword))
	again, err = unlock(noKeystore, databaseFile, "correct horse")
	assert.NoError(t, err)
	assert.True(t, again.equal(keys))
}

func TestUnlockReportsAChangedKeyFileAsDamaged(t *testing.T) {
	directory := t.TempDir()
	databaseFile := directory + "/bounce.db"
	platform := Platform{ConfigDirectory: directory, Secrets: &testSecretStore{}}
	_, err := unlock(platform, databaseFile, "")
	assert.NoError(t, err)

	// With the keystore's secret right and no password involved, a slot that does not open was tampered with
	kf, err := readKeyFile(directory)
	assert.NoError(t, err)
	stored, err := os.ReadFile(directory + "/" + keyFileName)
	assert.NoError(t, err)
	position := bytes.Index(stored, kf.Primary)
	assert.True(t, position > 0)
	stored[position+len(kf.Primary)-1] ^= 1
	assert.NoError(t, os.WriteFile(directory+"/"+keyFileName, stored, 0600))

	_, err = unlock(platform, databaseFile, "")
	assert.True(t, errors.Is(err, ErrKeyFileDamaged))

	assert.NoError(t, os.WriteFile(directory+"/"+keyFileName, []byte("not a key file"), 0600))
	_, err = unlock(platform, databaseFile, "")
	assert.True(t, errors.Is(err, ErrKeyFileDamaged))
}

func TestOpenedEngineKeepsNothingReadableOnDisk(t *testing.T) {
	alice := newBounceUser("Alice Unmistakable")
	t.Cleanup(alice.Shutdown)

	state, err := readDatabaseState(alice.configDirectory + "/bounce.db")
	assert.NoError(t, err)
	assert.Equal(t, databaseEncrypted, state)
	kf, err := readKeyFile(alice.configDirectory)
	assert.NoError(t, err)
	assert.True(t, kf.KeystoreUsed)

	var cipherStatus string
	assert.NoError(t, alice.database.Raw("PRAGMA cipher_status").Scan(&cipherStatus).Error)
	assert.Equal(t, "1", cipherStatus)

	for _, name := range []string{"bounce.db", "bounce.db-wal", keyFileName} {
		content, err := os.ReadFile(alice.configDirectory + "/" + name)
		assert.NoError(t, err)
		assert.False(t, bytes.Contains(content, []byte("Alice Unmistakable")), "%s holds the profile name in the clear", name)
		assert.False(t, bytes.Contains(content, []byte("CREATE TABLE")), "%s holds the schema in the clear", name)
	}
}

func TestOpenReturnsWhatStoppedItAndCanBeCalledAgain(t *testing.T) {
	// Not t.TempDir(): an engine keeps its database and log open after Shutdown in a test binary, and Windows
	// cannot remove a directory that holds open files
	directory := os.TempDir() + "/bounce-test-" + NewID().String()
	assert.NoError(t, os.MkdirAll(directory+"/blobs", 0700))
	store := &testSecretStore{}
	platform := Platform{ConfigDirectory: directory, Secrets: store}
	keys, err := unlock(platform, directory+"/bounce.db", "")
	assert.NoError(t, err)
	kf, err := readKeyFile(directory)
	assert.NoError(t, err)
	assert.NoError(t, kf.wrapWith(store.secret, "correct horse"))
	assert.NoError(t, kf.save(directory, keys))

	running := runningEngines.Load()
	for password, expected := range map[string]error{"": ErrPasswordRequired, "wrong horse": ErrWrongPassword} {
		engine, err := Open(newTestUI(), newTestnet(), platform, password)
		assert.True(t, errors.Is(err, expected), "got %v", err)
		assert.Equal(t, nil, engine)
		_, err = os.Stat(directory + "/.pid")
		assert.True(t, errors.Is(err, os.ErrNotExist), "the instance lock is given back")
		assert.Equal(t, running, runningEngines.Load())
	}

	engine, err := Open(newTestUI(), newTestnet(), platform, "correct horse")
	assert.NoError(t, err)
	t.Cleanup(engine.Shutdown)
	pid, err := os.ReadFile(directory + "/.pid")
	assert.NoError(t, err)
	assert.Equal(t, strconv.Itoa(os.Getpid()), string(pid))
}

func TestInstanceLockTakesOverFromAProcessThatIsGone(t *testing.T) {
	// A process that has already exited, whose ID is therefore free
	gone := exec.Command(os.Args[0], "-test.run=^$")
	assert.NoError(t, gone.Run())
	stale := map[string]string{
		"a finished process": strconv.Itoa(gone.Process.Pid),
		"not a process ID":   "bounce",
		"an empty file":      "",
	}

	for name, contents := range stale {
		t.Run(name, func(t *testing.T) {
			b := &Bounce{configDirectory: t.TempDir()}
			assert.NoError(t, os.WriteFile(b.configDirectory+"/.pid", []byte(contents), 0600))
			b.ensureOnlyOneInstance()

			pid, err := os.ReadFile(b.configDirectory + "/.pid")
			assert.NoError(t, err)
			assert.Equal(t, strconv.Itoa(os.Getpid()), string(pid))

			b.releaseInstance()
			_, err = os.Stat(b.configDirectory + "/.pid")
			assert.True(t, errors.Is(err, os.ErrNotExist))
			b.releaseInstance()
		})
	}
}

// The encrypted copy replaces the only database the user has, so the check it has to pass first is tested on its own
func TestEncryptedCopyIsCheckedBeforeItReplacesTheDatabase(t *testing.T) {
	directory := t.TempDir()
	databaseFile := directory + "/bounce.db"
	plaintextTestDatabase(t, databaseFile, true)
	keys, err := unlock(Platform{ConfigDirectory: directory}, databaseFile, "")
	assert.NoError(t, err)

	// The test database holds one table and one index
	assert.NoError(t, checkEncryptedCopy(databaseFile, keys.databaseKey(), 2))
	assert.Error(t, checkEncryptedCopy(databaseFile, keys.databaseKey(), 3), "a copy that is missing a table")
	assert.Error(t, checkEncryptedCopy(databaseFile, randomTestBytes(masterKeySize), 2), "a copy under another key")

	content, err := os.ReadFile(databaseFile)
	assert.NoError(t, err)
	content[len(content)/2] ^= 1
	assert.NoError(t, os.WriteFile(databaseFile, content, 0600))
	assert.Error(t, checkEncryptedCopy(databaseFile, keys.databaseKey(), 2), "a copy with a damaged page")
}
