package chat

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/zeebo/blake3"
	"golang.org/x/crypto/argon2"
)

// Everything an install keeps at rest is encrypted under keys derived from one random master key.  The master key
// lives in the key file beside the database, in a primary slot that is wrapped by a secret held in the operating
// system's keystore, by the user's password, by both, or by nothing at all on a device that has neither.  An optional
// recovery slot holds a second copy wrapped by a recovery code.  The master key never encrypts data itself and never
// changes when a password or a recovery code does: those only rewrap it.
//
// The slots hold a key set rather than one key.  It is the master key alone, except while the master key is being
// rolled, when the key it replaces rides beside it until everything has been re-encrypted.

const keyFileName = "bounce.key"
const keyFileVersion = 1
const masterKeySize = 32
const keystoreSecretSize = 32

// The cost of stretching a new password with Argon2id.  The values are written into the key file beside the salt, so
// they can be raised later without stranding a file written under these.
const passwordSaltSize = 16
const passwordPasses = 3
const passwordMemoryKiB = 64 * 1024
const passwordLanes = 4

// The most a key file may ask of Argon2id.  The cost is read from the file before anything authenticates it, so a
// damaged or doctored file must not be able to ask for all of the device's memory.
const maximumPasswordPasses = 64
const maximumPasswordMemoryKiB = 1024 * 1024

// Every key is derived with BLAKE3's key derivation mode under a context of its own, so no two purposes can ever
// share a key.  The contexts are part of the file format and must never change.  Attachments, when they are
// encrypted, take a context of their own beside the database's.
const primarySlotContext = "bounce.chat 2026-11-23 key file primary slot"
const recoverySlotContext = "bounce.chat 2026-11-23 key file recovery slot"
const keystoreCheckContext = "bounce.chat 2026-11-23 keystore check value"
const recoveryCodeChecksumContext = "bounce.chat 2026-11-23 recovery code checksum"

var ErrKeyFileDamaged = errors.New("the key file cannot be read")
var ErrNoRecoveryCode = errors.New("this install has no recovery code")
var ErrWrongRecoveryCode = errors.New("the recovery code does not belong to this install")
var ErrMistypedRecoveryCode = errors.New("the recovery code has a typing mistake in it")

var errSlotNotOpened = errors.New("the key file slot does not open with this key")

func deriveKey(context string, material []byte) []byte {
	key := make([]byte, 32)
	blake3.DeriveKey(context, material, key)
	return key
}

type masterKeySet struct {
	master []byte

	// The master key being replaced, set only while a roll is unfinished
	previous []byte
}

func (keys masterKeySet) bytes() []byte {
	return append(append([]byte{}, keys.master...), keys.previous...)
}

func (keys masterKeySet) equal(other masterKeySet) bool {
	return bytes.Equal(keys.master, other.master) && bytes.Equal(keys.previous, other.previous)
}

func parseMasterKeySet(data []byte) (masterKeySet, error) {
	switch len(data) {
	case masterKeySize:
		return masterKeySet{master: data}, nil
	case 2 * masterKeySize:
		return masterKeySet{master: data[:masterKeySize], previous: data[masterKeySize:]}, nil
	}
	return masterKeySet{}, fmt.Errorf("%w: a key set of %d bytes", ErrKeyFileDamaged, len(data))
}

func sealMasterKeySet(keys masterKeySet, wrappingKey []byte, associatedData []byte) ([]byte, error) {
	block, err := aes.NewCipher(wrappingKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, err
	}
	return gcm.Seal(nil, nil, keys.bytes(), associatedData), nil
}

func openMasterKeySet(sealed []byte, wrappingKey []byte, associatedData []byte) (masterKeySet, error) {
	block, err := aes.NewCipher(wrappingKey)
	if err != nil {
		return masterKeySet{}, err
	}
	gcm, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return masterKeySet{}, err
	}
	opened, err := gcm.Open(nil, nil, sealed, associatedData)
	if err != nil {
		return masterKeySet{}, errSlotNotOpened
	}
	return parseMasterKeySet(opened)
}

// A keyFile is the file as it is stored.  Everything but the two slots is a plaintext header: it says how the primary
// slot is wrapped, which has to be readable before anything can be unwrapped.  Each slot is sealed with the header
// fields it depends on as associated data, so a header that was changed fails the unwrap instead of being acted on.
type keyFile struct {
	Version uint8

	// How the primary slot is wrapped.  With neither flag set the slot holds the key set as it is.
	KeystoreUsed bool
	PasswordUsed bool

	// The Argon2id salt and cost, set while a password is used
	PasswordSalt      []byte
	PasswordPasses    uint32
	PasswordMemoryKiB uint32
	PasswordLanes     uint8

	// A hash of the keystore secret, set while the keystore is used.  It tells a keystore that holds some other
	// secret apart from a wrong password.  The secret is 32 random bytes, so its hash gives nothing away.
	KeystoreCheck []byte

	Primary []byte

	// The recovery slot and the time its code was made, both empty when there is no recovery code
	Recovery          []byte
	RecoveryCreatedAt int64

	// The key the primary slot is wrapped under, which is never stored.  It is known once the slot has been opened or
	// the header has been set by wrapWith, and it is what save seals the slot with, so a file is always saved under
	// the key its own header derives.
	wrappingKey []byte
}

func keystoreCheckValue(keystoreSecret []byte) []byte {
	return deriveKey(keystoreCheckContext, keystoreSecret)
}

func (kf *keyFile) wrapped() bool {
	return kf.KeystoreUsed || kf.PasswordUsed
}

func (kf *keyFile) validate() error {
	if kf.Version != keyFileVersion {
		return fmt.Errorf("%w: version %d is not supported", ErrKeyFileDamaged, kf.Version)
	}
	if kf.PasswordUsed {
		if len(kf.PasswordSalt) < passwordSaltSize || len(kf.PasswordSalt) > 64 {
			return fmt.Errorf("%w: a password salt of %d bytes", ErrKeyFileDamaged, len(kf.PasswordSalt))
		}
		if kf.PasswordPasses == 0 || kf.PasswordPasses > maximumPasswordPasses || kf.PasswordLanes == 0 ||
			kf.PasswordMemoryKiB < 8*uint32(kf.PasswordLanes) || kf.PasswordMemoryKiB > maximumPasswordMemoryKiB {
			return fmt.Errorf("%w: the password cost is out of range", ErrKeyFileDamaged)
		}
	}
	if kf.KeystoreUsed && len(kf.KeystoreCheck) != 32 {
		return fmt.Errorf("%w: a keystore check value of %d bytes", ErrKeyFileDamaged, len(kf.KeystoreCheck))
	}
	if len(kf.Primary) == 0 {
		return fmt.Errorf("%w: the primary slot is empty", ErrKeyFileDamaged)
	}
	return nil
}

func (kf *keyFile) primaryAssociatedData() []byte {
	data := []byte(primarySlotContext)
	data = append(data, kf.Version)
	if kf.KeystoreUsed {
		data = append(data, 1)
	} else {
		data = append(data, 0)
	}
	if kf.PasswordUsed {
		data = append(data, 1)
	} else {
		data = append(data, 0)
	}
	data = binary.BigEndian.AppendUint32(data, uint32(len(kf.PasswordSalt)))
	data = append(data, kf.PasswordSalt...)
	data = binary.BigEndian.AppendUint32(data, kf.PasswordPasses)
	data = binary.BigEndian.AppendUint32(data, kf.PasswordMemoryKiB)
	data = append(data, kf.PasswordLanes)
	data = binary.BigEndian.AppendUint32(data, uint32(len(kf.KeystoreCheck)))
	data = append(data, kf.KeystoreCheck...)
	return data
}

// The recovery slot depends on nothing that a change of password or keystore touches, because it cannot be sealed
// again without its code, and the code is never kept
func (kf *keyFile) recoveryAssociatedData() []byte {
	data := []byte(recoverySlotContext)
	data = append(data, kf.Version)
	data = binary.BigEndian.AppendUint64(data, uint64(kf.RecoveryCreatedAt))
	return data
}

// primaryWrappingKey derives the key that wraps the primary slot from whichever of the keystore secret and the
// password the header says are used.  The keystore secret goes first and the stretched password after it, so with a
// keystore the file alone cannot be attacked by guessing passwords, however weak the password is.  It returns nil for
// an unwrapped file.
func (kf *keyFile) primaryWrappingKey(keystoreSecret []byte, password string) []byte {
	if !kf.wrapped() {
		return nil
	}

	material := []byte{}
	if kf.KeystoreUsed {
		material = append(material, keystoreSecret...)
	}
	if kf.PasswordUsed {
		stretched := argon2.IDKey([]byte(password), kf.PasswordSalt, kf.PasswordPasses, kf.PasswordMemoryKiB, kf.PasswordLanes, 32)
		material = append(material, stretched...)
	}
	return deriveKey(primarySlotContext, material)
}

// wrapWith sets the header for a primary slot wrapped by the given keystore secret and password, ready for save.  A
// nil secret or an empty password leaves that one out, and with neither the file is unwrapped.  A password is given a
// new salt every time, including when it has not changed.
func (kf *keyFile) wrapWith(keystoreSecret []byte, password string) error {
	kf.KeystoreUsed = keystoreSecret != nil
	kf.KeystoreCheck = nil
	if kf.KeystoreUsed {
		kf.KeystoreCheck = keystoreCheckValue(keystoreSecret)
	}

	kf.PasswordUsed = password != ""
	kf.PasswordSalt = nil
	kf.PasswordPasses = 0
	kf.PasswordMemoryKiB = 0
	kf.PasswordLanes = 0
	if kf.PasswordUsed {
		kf.PasswordSalt = make([]byte, passwordSaltSize)
		_, err := rand.Read(kf.PasswordSalt)
		if err != nil {
			return err
		}
		kf.PasswordPasses = passwordPasses
		kf.PasswordMemoryKiB = passwordMemoryKiB
		kf.PasswordLanes = passwordLanes
	}

	kf.wrappingKey = kf.primaryWrappingKey(keystoreSecret, password)
	return nil
}

// openPrimary returns the key set in the primary slot, given whichever of the keystore secret and the password the
// header says are used.  It returns errSlotNotOpened when they are not the ones the slot was sealed with.
func (kf *keyFile) openPrimary(keystoreSecret []byte, password string) (masterKeySet, error) {
	if !kf.wrapped() {
		return parseMasterKeySet(kf.Primary)
	}

	wrappingKey := kf.primaryWrappingKey(keystoreSecret, password)
	keys, err := openMasterKeySet(kf.Primary, wrappingKey, kf.primaryAssociatedData())
	if err != nil {
		return masterKeySet{}, err
	}
	kf.wrappingKey = wrappingKey
	return keys, nil
}

// sealRecovery replaces the recovery slot with one that opens with the given recovery code secret
func (kf *keyFile) sealRecovery(keys masterKeySet, recoverySecret []byte, createdAt int64) error {
	kf.RecoveryCreatedAt = createdAt
	sealed, err := sealMasterKeySet(keys, deriveKey(recoverySlotContext, recoverySecret), kf.recoveryAssociatedData())
	if err != nil {
		return err
	}
	kf.Recovery = sealed
	return nil
}

func (kf *keyFile) openRecovery(recoverySecret []byte) (masterKeySet, error) {
	if len(kf.Recovery) == 0 {
		return masterKeySet{}, ErrNoRecoveryCode
	}
	keys, err := openMasterKeySet(kf.Recovery, deriveKey(recoverySlotContext, recoverySecret), kf.recoveryAssociatedData())
	if errors.Is(err, errSlotNotOpened) {
		return masterKeySet{}, ErrWrongRecoveryCode
	}
	return keys, err
}

func (kf *keyFile) dropRecovery() {
	kf.Recovery = nil
	kf.RecoveryCreatedAt = 0
}

func readKeyFileAt(path string) (*keyFile, []byte, error) {
	stored, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrKeyFileDamaged, err)
	}

	kf := &keyFile{}
	err = msgpack.Unmarshal(stored, kf)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrKeyFileDamaged, err)
	}
	err = kf.validate()
	if err != nil {
		return nil, nil, err
	}
	return kf, stored, nil
}

// readKeyFile returns an error matching os.ErrNotExist when the install has no key file, and ErrKeyFileDamaged when
// it has one that cannot be used
func readKeyFile(configDirectory string) (*keyFile, error) {
	kf, _, err := readKeyFileAt(configDirectory + "/" + keyFileName)
	return kf, err
}

// save seals the key set into the primary slot and replaces the install's key file.  A wrapped file must have been
// opened or had its header set by wrapWith first, since that is where its wrapping key comes from.
//
// The new file is written beside the old one, synced, then read back and its primary slot opened again before it is
// renamed into place.  The old file is the only way to the user's data, so it is never given up for a file that has
// not been shown to open.
func (kf *keyFile) save(configDirectory string, keys masterKeySet) error {
	if kf.wrapped() {
		if kf.wrappingKey == nil {
			return errors.New("the key file has to be opened before it can be saved")
		}
		sealed, err := sealMasterKeySet(keys, kf.wrappingKey, kf.primaryAssociatedData())
		if err != nil {
			return err
		}
		kf.Primary = sealed
	} else {
		kf.Primary = keys.bytes()
	}

	err := kf.validate()
	if err != nil {
		return err
	}
	encoded, err := msgpack.Marshal(kf)
	if err != nil {
		return err
	}

	keyFilePath := configDirectory + "/" + keyFileName
	temporaryPath := keyFilePath + ".tmp"

	// A temporary file left by an interrupted save is replaced, never appended to or trusted
	err = os.Remove(temporaryPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(temporaryPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(encoded)
	if err != nil {
		file.Close()
		os.Remove(temporaryPath)
		return err
	}
	err = file.Sync()
	if err != nil {
		file.Close()
		os.Remove(temporaryPath)
		return err
	}
	err = file.Close()
	if err != nil {
		os.Remove(temporaryPath)
		return err
	}

	written, stored, err := readKeyFileAt(temporaryPath)
	if err != nil {
		os.Remove(temporaryPath)
		return fmt.Errorf("the new key file could not be read back: %w", err)
	}
	if !bytes.Equal(stored, encoded) {
		os.Remove(temporaryPath)
		return errors.New("the new key file read back differently from how it was written")
	}
	var reopened masterKeySet
	if written.wrapped() {
		reopened, err = openMasterKeySet(written.Primary, kf.wrappingKey, written.primaryAssociatedData())
	} else {
		reopened, err = parseMasterKeySet(written.Primary)
	}
	if err != nil {
		os.Remove(temporaryPath)
		return fmt.Errorf("the new key file could not be opened again: %w", err)
	}
	if !reopened.equal(keys) {
		os.Remove(temporaryPath)
		return errors.New("the new key file opened to a different key set")
	}

	err = os.Rename(temporaryPath, keyFilePath)
	if err != nil {
		os.Remove(temporaryPath)
		return err
	}

	// Sync the directory so the rename itself survives a power loss.  Not every platform can open a directory for
	// this, and the file is already in place, so a failure here is not a failure to save.
	directory, err := os.Open(configDirectory)
	if err == nil {
		directory.Sync()
		directory.Close()
	}
	return nil
}

// A recovery code is 16 random bytes and a 4 byte checksum of them, written as 32 characters in 8 groups of 4.  The
// alphabet is the digits and the capital letters without I, L, O and U, so no two characters look alike and a code
// read aloud or copied by hand survives.  The checksum is what lets a typing mistake be told apart from a code that
// belongs to another install.
const recoveryCodeSecretSize = 16
const recoveryCodeChecksumSize = 4

var recoveryCodeEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// The mistakes a person makes when copying a code: case, spacing, and the letters the alphabet leaves out because
// they look like digits
var recoveryCodeInput = strings.NewReplacer(" ", "", "-", "", "\t", "", "\r", "", "\n", "", "O", "0", "I", "1", "L", "1")

func recoveryCodeChecksum(recoverySecret []byte) []byte {
	return deriveKey(recoveryCodeChecksumContext, recoverySecret)[:recoveryCodeChecksumSize]
}

// newRecoveryCode returns a new code as it is shown to the user, and the secret it encodes
func newRecoveryCode() (string, []byte, error) {
	recoverySecret := make([]byte, recoveryCodeSecretSize)
	_, err := rand.Read(recoverySecret)
	if err != nil {
		return "", nil, err
	}

	encoded := recoveryCodeEncoding.EncodeToString(append(append([]byte{}, recoverySecret...), recoveryCodeChecksum(recoverySecret)...))
	groups := []string{}
	for i := 0; i < len(encoded); i += 4 {
		groups = append(groups, encoded[i:i+4])
	}
	return strings.Join(groups, "-"), recoverySecret, nil
}

// parseRecoveryCode returns the secret a code encodes, or ErrMistypedRecoveryCode when the text is not a code or its
// checksum does not match
func parseRecoveryCode(code string) ([]byte, error) {
	// The length is checked on the text, because the decoder lets a stray character past the last full group through
	characters := recoveryCodeInput.Replace(strings.ToUpper(code))
	if len(characters) != recoveryCodeEncoding.EncodedLen(recoveryCodeSecretSize+recoveryCodeChecksumSize) {
		return nil, ErrMistypedRecoveryCode
	}
	decoded, err := recoveryCodeEncoding.DecodeString(characters)
	if err != nil || len(decoded) != recoveryCodeSecretSize+recoveryCodeChecksumSize {
		return nil, ErrMistypedRecoveryCode
	}

	recoverySecret := decoded[:recoveryCodeSecretSize]
	if !bytes.Equal(decoded[recoveryCodeSecretSize:], recoveryCodeChecksum(recoverySecret)) {
		return nil, ErrMistypedRecoveryCode
	}
	return recoverySecret, nil
}
