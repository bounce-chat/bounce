package chat

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/alecthomas/assert/v2"
)

func randomTestBytes(size int) []byte {
	data := make([]byte, size)
	rand.Read(data)
	return data
}

// savedTestKeyFile saves a new key file wrapped by the given keystore secret and password, either of which may be
// absent, and returns the key set it holds
func savedTestKeyFile(t *testing.T, directory string, keystoreSecret []byte, password string) masterKeySet {
	t.Helper()
	keys := masterKeySet{master: randomTestBytes(masterKeySize)}
	kf := &keyFile{Version: keyFileVersion}
	assert.NoError(t, kf.wrapWith(keystoreSecret, password))
	assert.NoError(t, kf.save(directory, keys))
	return keys
}

func TestKeyFileRoundTripsInEveryState(t *testing.T) {
	keystoreSecret := randomTestBytes(keystoreSecretSize)
	states := []struct {
		name           string
		keystoreSecret []byte
		password       string
	}{
		{"unwrapped", nil, ""},
		{"keystore", keystoreSecret, ""},
		{"password", nil, "correct horse"},
		{"keystore and password", keystoreSecret, "correct horse"},
	}

	for _, state := range states {
		t.Run(state.name, func(t *testing.T) {
			directory := t.TempDir()
			keys := savedTestKeyFile(t, directory, state.keystoreSecret, state.password)

			kf, err := readKeyFile(directory)
			assert.NoError(t, err)
			assert.Equal(t, state.keystoreSecret != nil, kf.KeystoreUsed)
			assert.Equal(t, state.password != "", kf.PasswordUsed)

			opened, err := kf.openPrimary(state.keystoreSecret, state.password)
			assert.NoError(t, err)
			assert.True(t, opened.equal(keys))

			// Only an unwrapped file may hold the master key in the clear
			stored, err := os.ReadFile(directory + "/" + keyFileName)
			assert.NoError(t, err)
			assert.Equal(t, !kf.wrapped(), bytes.Contains(stored, keys.master))

			if runtime.GOOS != "windows" {
				info, err := os.Stat(directory + "/" + keyFileName)
				assert.NoError(t, err)
				assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
			}
			_, err = os.Stat(directory + "/" + keyFileName + ".tmp")
			assert.True(t, errors.Is(err, os.ErrNotExist), "the temporary file is not left behind")
		})
	}
}

func TestKeyFileRefusesTheWrongCredentials(t *testing.T) {
	keystoreSecret := randomTestBytes(keystoreSecretSize)
	otherSecret := randomTestBytes(keystoreSecretSize)
	directory := t.TempDir()
	savedTestKeyFile(t, directory, keystoreSecret, "correct horse")
	kf, err := readKeyFile(directory)
	assert.NoError(t, err)

	_, err = kf.openPrimary(keystoreSecret, "wrong horse")
	assert.True(t, errors.Is(err, errSlotNotOpened), "a wrong password does not open the slot")

	_, err = kf.openPrimary(otherSecret, "correct horse")
	assert.True(t, errors.Is(err, errSlotNotOpened), "a different keystore secret does not open the slot")

	// The check value is what tells those two failures apart before any unwrap is tried
	assert.Equal(t, keystoreCheckValue(keystoreSecret), kf.KeystoreCheck)
	assert.NotEqual(t, keystoreCheckValue(otherSecret), kf.KeystoreCheck)
}

func TestKeyFileHeaderIsBoundToThePrimarySlot(t *testing.T) {
	keystoreSecret := randomTestBytes(keystoreSecretSize)
	directory := t.TempDir()
	savedTestKeyFile(t, directory, keystoreSecret, "correct horse")

	changes := map[string]func(kf *keyFile){
		"version":         func(kf *keyFile) { kf.Version++ },
		"password passes": func(kf *keyFile) { kf.PasswordPasses-- },
		"password memory": func(kf *keyFile) { kf.PasswordMemoryKiB /= 2 },
		"password lanes":  func(kf *keyFile) { kf.PasswordLanes-- },
		"keystore check":  func(kf *keyFile) { kf.KeystoreCheck[0] ^= 1 },
		"password salt":   func(kf *keyFile) { kf.PasswordSalt[0] ^= 1 },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			kf, err := readKeyFile(directory)
			assert.NoError(t, err)
			wrappingKey := kf.primaryWrappingKey(keystoreSecret, "correct horse")
			_, err = openMasterKeySet(kf.Primary, wrappingKey, kf.primaryAssociatedData())
			assert.NoError(t, err)

			// The key is the one derived from the untouched header, so only the binding can refuse it
			change(kf)
			_, err = openMasterKeySet(kf.Primary, wrappingKey, kf.primaryAssociatedData())
			assert.True(t, errors.Is(err, errSlotNotOpened))
		})
	}

	// A header claiming less protection than the slot was sealed with cannot be used to skip a credential
	for name, change := range map[string]func(kf *keyFile){
		"password dropped": func(kf *keyFile) { kf.PasswordUsed = false },
		"keystore dropped": func(kf *keyFile) { kf.KeystoreUsed = false },
	} {
		t.Run(name, func(t *testing.T) {
			kf, err := readKeyFile(directory)
			assert.NoError(t, err)
			change(kf)
			_, err = kf.openPrimary(keystoreSecret, "correct horse")
			assert.True(t, errors.Is(err, errSlotNotOpened))
		})
	}
}

func TestKeyFileKeepsThePreviousKeyDuringARoll(t *testing.T) {
	keystoreSecret := randomTestBytes(keystoreSecretSize)
	directory := t.TempDir()
	keys := masterKeySet{master: randomTestBytes(masterKeySize), previous: randomTestBytes(masterKeySize)}
	kf := &keyFile{Version: keyFileVersion}
	assert.NoError(t, kf.wrapWith(keystoreSecret, ""))
	assert.NoError(t, kf.save(directory, keys))

	kf, err := readKeyFile(directory)
	assert.NoError(t, err)
	opened, err := kf.openPrimary(keystoreSecret, "")
	assert.NoError(t, err)
	assert.Equal(t, keys.master, opened.master)
	assert.Equal(t, keys.previous, opened.previous)
}

func TestKeyFileThatCannotBeReadIsDamaged(t *testing.T) {
	directory := t.TempDir()
	_, err := readKeyFile(directory)
	assert.True(t, errors.Is(err, os.ErrNotExist), "a missing key file is not a damaged one")

	savedTestKeyFile(t, directory, randomTestBytes(keystoreSecretSize), "correct horse")
	good, err := os.ReadFile(directory + "/" + keyFileName)
	assert.NoError(t, err)

	reencoded := func(change func(kf *keyFile)) []byte {
		kf := &keyFile{}
		assert.NoError(t, msgpack.Unmarshal(good, kf))
		change(kf)
		encoded, err := msgpack.Marshal(kf)
		assert.NoError(t, err)
		return encoded
	}
	damaged := map[string][]byte{
		"empty":             {},
		"truncated":         good[:len(good)/2],
		"not a key file":    []byte("SQLite format 3\x00"),
		"an empty map":      {0x80},
		"a later version":   reencoded(func(kf *keyFile) { kf.Version = keyFileVersion + 1 }),
		"no primary slot":   reencoded(func(kf *keyFile) { kf.Primary = nil }),
		"no salt":           reencoded(func(kf *keyFile) { kf.PasswordSalt = nil }),
		"no keystore check": reencoded(func(kf *keyFile) { kf.KeystoreCheck = nil }),
		// The cost is read before anything authenticates the file, so it is bounded rather than obeyed
		"all the memory": reencoded(func(kf *keyFile) { kf.PasswordMemoryKiB = 1 << 31 }),
		"endless passes": reencoded(func(kf *keyFile) { kf.PasswordPasses = 1 << 20 }),
		"no lanes":       reencoded(func(kf *keyFile) { kf.PasswordLanes = 0 }),
	}
	for name, content := range damaged {
		t.Run(name, func(t *testing.T) {
			assert.NoError(t, os.WriteFile(directory+"/"+keyFileName, content, 0600))
			_, err := readKeyFile(directory)
			assert.True(t, errors.Is(err, ErrKeyFileDamaged), "got %v", err)
		})
	}

	// An unwrapped file whose slot is not a key set is found when the slot is opened
	assert.NoError(t, os.WriteFile(directory+"/"+keyFileName, reencoded(func(kf *keyFile) {
		kf.KeystoreUsed = false
		kf.PasswordUsed = false
	}), 0600))
	kf, err := readKeyFile(directory)
	assert.NoError(t, err)
	_, err = kf.openPrimary(nil, "")
	assert.True(t, errors.Is(err, ErrKeyFileDamaged))
}

func TestKeyFileSaveKeepsTheOldFileWhenTheNewOneFails(t *testing.T) {
	keystoreSecret := randomTestBytes(keystoreSecretSize)
	directory := t.TempDir()
	keys := savedTestKeyFile(t, directory, keystoreSecret, "")
	before, err := os.ReadFile(directory + "/" + keyFileName)
	assert.NoError(t, err)
	replacement := masterKeySet{master: randomTestBytes(masterKeySize)}

	// A wrapped file that was only read has no wrapping key to be saved under
	kf, err := readKeyFile(directory)
	assert.NoError(t, err)
	assert.Error(t, kf.save(directory, replacement))

	// A header that would not be readable again is refused before anything is written
	kf, err = readKeyFile(directory)
	assert.NoError(t, err)
	_, err = kf.openPrimary(keystoreSecret, "")
	assert.NoError(t, err)
	kf.Version = keyFileVersion + 1
	assert.Error(t, kf.save(directory, replacement))

	// The temporary file cannot be written
	assert.NoError(t, os.Mkdir(directory+"/"+keyFileName+".tmp", 0700))
	assert.NoError(t, os.WriteFile(directory+"/"+keyFileName+".tmp/occupied", []byte{}, 0600))
	kf, err = readKeyFile(directory)
	assert.NoError(t, err)
	_, err = kf.openPrimary(keystoreSecret, "")
	assert.NoError(t, err)
	assert.Error(t, kf.save(directory, replacement))

	after, err := os.ReadFile(directory + "/" + keyFileName)
	assert.NoError(t, err)
	assert.Equal(t, before, after)
	kf, err = readKeyFile(directory)
	assert.NoError(t, err)
	opened, err := kf.openPrimary(keystoreSecret, "")
	assert.NoError(t, err)
	assert.True(t, opened.equal(keys))
}

// The wrapping key always follows the header, so changing how a file is wrapped and saving it cannot leave a slot
// sealed under the key of the old header
func TestKeyFileIsSavedUnderTheKeyItsHeaderDerives(t *testing.T) {
	keystoreSecret := randomTestBytes(keystoreSecretSize)
	directory := t.TempDir()
	keys := savedTestKeyFile(t, directory, keystoreSecret, "")

	kf, err := readKeyFile(directory)
	assert.NoError(t, err)
	_, err = kf.openPrimary(keystoreSecret, "")
	assert.NoError(t, err)
	assert.NoError(t, kf.wrapWith(keystoreSecret, "correct horse"))
	assert.NoError(t, kf.save(directory, keys))

	kf, err = readKeyFile(directory)
	assert.NoError(t, err)
	_, err = kf.openPrimary(keystoreSecret, "")
	assert.True(t, errors.Is(err, errSlotNotOpened), "the password is now needed")
	opened, err := kf.openPrimary(keystoreSecret, "correct horse")
	assert.NoError(t, err)
	assert.True(t, opened.equal(keys))

	// A file that was opened can be saved again as it is, which is how a recovery slot is added
	assert.NoError(t, kf.save(directory, keys))
	kf, err = readKeyFile(directory)
	assert.NoError(t, err)
	opened, err = kf.openPrimary(keystoreSecret, "correct horse")
	assert.NoError(t, err)
	assert.True(t, opened.equal(keys))
}

func TestKeyFileSaveReplacesALeftoverTemporaryFile(t *testing.T) {
	directory := t.TempDir()
	assert.NoError(t, os.WriteFile(directory+"/"+keyFileName+".tmp", []byte("left by an interrupted save"), 0644))

	keys := savedTestKeyFile(t, directory, nil, "")
	kf, err := readKeyFile(directory)
	assert.NoError(t, err)
	opened, err := kf.openPrimary(nil, "")
	assert.NoError(t, err)
	assert.True(t, opened.equal(keys))
	if runtime.GOOS != "windows" {
		info, err := os.Stat(directory + "/" + keyFileName)
		assert.NoError(t, err)
		assert.Equal(t, os.FileMode(0600), info.Mode().Perm(), "the leftover file's mode is not inherited")
	}
}

func TestRecoverySlotOpensWithItsCodeOnly(t *testing.T) {
	keystoreSecret := randomTestBytes(keystoreSecretSize)
	directory := t.TempDir()
	keys := savedTestKeyFile(t, directory, keystoreSecret, "correct horse")

	kf, err := readKeyFile(directory)
	assert.NoError(t, err)
	_, err = kf.openPrimary(keystoreSecret, "correct horse")
	assert.NoError(t, err)
	_, err = kf.openRecovery(randomTestBytes(recoveryCodeSecretSize))
	assert.True(t, errors.Is(err, ErrNoRecoveryCode))

	code, recoverySecret, err := newRecoveryCode()
	assert.NoError(t, err)
	assert.NoError(t, kf.sealRecovery(keys, recoverySecret, 1795000000))
	assert.NoError(t, kf.save(directory, keys))

	kf, err = readKeyFile(directory)
	assert.NoError(t, err)
	assert.Equal(t, int64(1795000000), kf.RecoveryCreatedAt)
	parsed, err := parseRecoveryCode(code)
	assert.NoError(t, err)
	recovered, err := kf.openRecovery(parsed)
	assert.NoError(t, err)
	assert.True(t, recovered.equal(keys))

	_, otherSecret, err := newRecoveryCode()
	assert.NoError(t, err)
	_, err = kf.openRecovery(otherSecret)
	assert.True(t, errors.Is(err, ErrWrongRecoveryCode))

	// The slot is bound to its own creation time
	kf.RecoveryCreatedAt++
	_, err = kf.openRecovery(parsed)
	assert.True(t, errors.Is(err, ErrWrongRecoveryCode))
}

// A recovery slot cannot be sealed again without its code, so nothing done to the primary slot may disturb it
func TestRecoverySlotSurvivesRewrappingThePrimarySlot(t *testing.T) {
	keystoreSecret := randomTestBytes(keystoreSecretSize)
	directory := t.TempDir()
	keys := savedTestKeyFile(t, directory, keystoreSecret, "")

	kf, err := readKeyFile(directory)
	assert.NoError(t, err)
	_, err = kf.openPrimary(keystoreSecret, "")
	assert.NoError(t, err)
	_, recoverySecret, err := newRecoveryCode()
	assert.NoError(t, err)
	assert.NoError(t, kf.sealRecovery(keys, recoverySecret, 1795000000))
	assert.NoError(t, kf.save(directory, keys))

	rewraps := []struct {
		keystoreSecret []byte
		password       string
	}{
		{keystoreSecret, "correct horse"},
		{keystoreSecret, "battery staple"},
		{randomTestBytes(keystoreSecretSize), "battery staple"},
		{nil, "battery staple"},
		{keystoreSecret, ""},
	}
	for _, rewrap := range rewraps {
		kf, err = readKeyFile(directory)
		assert.NoError(t, err)
		assert.NoError(t, kf.wrapWith(rewrap.keystoreSecret, rewrap.password))
		assert.NoError(t, kf.save(directory, keys))

		kf, err = readKeyFile(directory)
		assert.NoError(t, err)
		opened, err := kf.openPrimary(rewrap.keystoreSecret, rewrap.password)
		assert.NoError(t, err)
		assert.True(t, opened.equal(keys))
		recovered, err := kf.openRecovery(recoverySecret)
		assert.NoError(t, err)
		assert.True(t, recovered.equal(keys))
	}

	kf.dropRecovery()
	assert.NoError(t, kf.save(directory, keys))
	kf, err = readKeyFile(directory)
	assert.NoError(t, err)
	_, err = kf.openRecovery(recoverySecret)
	assert.True(t, errors.Is(err, ErrNoRecoveryCode))
}

func TestRecoveryCodeShape(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		code, recoverySecret, err := newRecoveryCode()
		assert.NoError(t, err)
		assert.Equal(t, recoveryCodeSecretSize, len(recoverySecret))
		assert.False(t, seen[code])
		seen[code] = true

		groups := strings.Split(code, "-")
		assert.Equal(t, 8, len(groups))
		for _, group := range groups {
			assert.Equal(t, 4, len(group))
			for _, character := range group {
				assert.True(t, strings.ContainsRune("0123456789ABCDEFGHJKMNPQRSTVWXYZ", character), "%q in %s", character, code)
			}
		}

		parsed, err := parseRecoveryCode(code)
		assert.NoError(t, err)
		assert.Equal(t, recoverySecret, parsed)
	}
}

func TestRecoveryCodeForgivesHowItWasCopied(t *testing.T) {
	code, recoverySecret, err := newRecoveryCode()
	assert.NoError(t, err)

	lookalikes := strings.NewReplacer("0", "O", "1", "l").Replace(code)
	copies := map[string]string{
		"lower case":           strings.ToLower(code),
		"no separators":        strings.ReplaceAll(code, "-", ""),
		"spaces":               strings.ReplaceAll(code, "-", " "),
		"two rows":             "  " + code[:19] + "\n" + code[20:] + "\r\n",
		"letters for digits":   lookalikes,
		"capital I for a one":  strings.ReplaceAll(code, "1", "I"),
		"mixed case and space": strings.ToLower(strings.ReplaceAll(code, "-", "  ")),
	}
	for name, copied := range copies {
		t.Run(name, func(t *testing.T) {
			parsed, err := parseRecoveryCode(copied)
			assert.NoError(t, err, "%q", copied)
			assert.Equal(t, recoverySecret, parsed)
		})
	}
}

func TestRecoveryCodeCatchesTypingMistakes(t *testing.T) {
	code, _, err := newRecoveryCode()
	assert.NoError(t, err)

	mistakes := []string{
		"",
		"not a recovery code",
		code[:len(code)-1],
		code + "0",
		strings.Replace(code, "-", "U", 1),
		code[:10] + string(code[11]) + string(code[10]) + code[12:],
	}
	// Every single character replaced by its neighbour in the alphabet
	alphabet := "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	for i, character := range code {
		if character == '-' {
			continue
		}
		replacement := alphabet[(strings.IndexRune(alphabet, character)+1)%len(alphabet)]
		mistakes = append(mistakes, code[:i]+string(replacement)+code[i+1:])
	}

	for _, mistake := range mistakes {
		if mistake == code {
			continue
		}
		_, err := parseRecoveryCode(mistake)
		assert.True(t, errors.Is(err, ErrMistypedRecoveryCode), "%q was accepted", mistake)
	}
}

func hexTestBytes(t *testing.T, encoded string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(encoded)
	assert.NoError(t, err)
	return decoded
}

// Every other test here writes and reads with the same code, so a change to how keys are stored or derived passes
// them all.  This one opens two key files as they were written on 2026-11-23, which is how every install since has
// them on disk: a change that fails it leaves those installs unable to open.  Both files were also opened by an
// implementation that shares no code with this one, so they hold the documented format and not only this code's.
func TestStoredKeysStillOpenAsTheyWereWritten(t *testing.T) {
	master := hexTestBytes(t, "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	keystoreSecret := hexTestBytes(t, "202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f")

	// Wrapped by the keystore and a password, with a recovery slot
	directory := t.TempDir()
	assert.NoError(t, os.WriteFile(directory+"/"+keyFileName, hexTestBytes(t, ""+
		"8ba756657273696f6ecc01ac4b657973746f726555736564c3ac50617373776f726455736564c3ac50617373776f726453616c74c410"+
		"626f756e63652d746573742d73616c74ae50617373776f7264506173736573ce00000003b150617373776f72644d656d6f72794b6942"+
		"ce00010000ad50617373776f72644c616e6573cc04ad4b657973746f7265436865636bc420e49c2e1a16d60c1c68ad498790412edf21"+
		"047922520437b89f92afa7f38c00dea75072696d617279c43c2cdd38cca1169ad9315b3536100809c758f58f6639510d9237a565a34d"+
		"a2109dbbb0c7073dc0b113ac645bcba33803e4ba88fad54d3259f864240e6aa85265636f76657279c43c8c2a21e76d8a931db3bab848"+
		"da2faf593a5e0d7abc7413f7b2e9ecb028b12364a86afce3a2a52cafe2f9348dbf79d4e4fbe6c1a8e5fa64496c9d73e7b15265636f76"+
		"657279437265617465644174d3000000006afd86c0"), 0600))
	platform := Platform{ConfigDirectory: directory, Secrets: &testSecretStore{secret: keystoreSecret}}
	keys, err := unlock(platform, directory+"/bounce.db", "correct horse battery staple")
	assert.NoError(t, err)
	assert.Equal(t, master, keys.master)
	assert.Equal(t, 0, len(keys.previous))

	kf, err := readKeyFile(directory)
	assert.NoError(t, err)
	recoverySecret, err := parseRecoveryCode("810M-4GT4-8N34-EJ29-995M-RKAE-9ZHC-HV5P")
	assert.NoError(t, err)
	recovered, err := kf.openRecovery(recoverySecret)
	assert.NoError(t, err)
	assert.Equal(t, master, recovered.master)
	assert.Equal(t, int64(1795000000), kf.RecoveryCreatedAt)

	// Unwrapped, as a device with no keystore has it
	directory = t.TempDir()
	assert.NoError(t, os.WriteFile(directory+"/"+keyFileName, hexTestBytes(t, ""+
		"8ba756657273696f6ecc01ac4b657973746f726555736564c2ac50617373776f726455736564c2ac50617373776f726453616c74c0ae"+
		"50617373776f7264506173736573ce00000000b150617373776f72644d656d6f72794b6942ce00000000ad50617373776f72644c616e"+
		"6573cc00ad4b657973746f7265436865636bc0a75072696d617279c420000102030405060708090a0b0c0d0e0f101112131415161718"+
		"191a1b1c1d1e1fa85265636f76657279c0b15265636f76657279437265617465644174d30000000000000000"), 0600))
	keys, err = unlock(Platform{ConfigDirectory: directory}, directory+"/bounce.db", "")
	assert.NoError(t, err)
	assert.Equal(t, master, keys.master)

	// The keys the database and the network identity are encrypted under
	assert.Equal(t, "f1a88cf5cbfe7694acfb1d58efeae596b4aea953afd9ff9f76f81a098dcc0ab9", hex.EncodeToString(keys.databaseKey()))
	assert.Equal(t, "756cb6d97cbb38e4324c5bb3bf4f5de4a76e1b90e7f57653a4ca6fce38fa702b", hex.EncodeToString(keys.networkKey()))
}
