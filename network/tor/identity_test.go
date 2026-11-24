package tor

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"os"
	"runtime"
	"testing"

	arti "github.com/bounce-chat/go-arti"
	log "github.com/sirupsen/logrus"
)

// A fresh install has to be able to create a profile straight away, which means signing and knowing our own address
// before Tor has bootstrapped, let alone published the service.  So the identity is generated when the config is
// loaded, not handed back by Tor later.

// The engine hands the transport a key to keep its identity under.  Any 32 bytes serve for a test.
var testIdentityFileKey = bytes.Repeat([]byte{0x42}, 32)

func TestAFreshInstallHasAnIdentityBeforeTorStarts(t *testing.T) {
	configDirectory := t.TempDir()
	tor := &Network{}
	tor.Load(configDirectory, testIdentityFileKey)

	// The private key is on disk, private to the user and sealed under the engine's key.  Opened, it is the 64-byte
	// expanded form; the public key is derived from it.
	sealed, err := os.ReadFile(configDirectory + "/tor/keys/private_key")
	if err != nil {
		t.Fatalf("no private key written: %v", err)
	}
	private, err := openSealedPrivateKey(sealed, testIdentityFileKey)
	if err != nil {
		t.Fatalf("the stored private key does not open with the engine's key: %v", err)
	}
	if len(private) != arti.PrivateKeySize {
		t.Fatalf("private key is %d bytes, want %d", len(private), arti.PrivateKeySize)
	}
	if bytes.Contains(sealed, private[:16]) || bytes.Contains(sealed, private[32:48]) {
		t.Fatalf("the private key is on disk in the clear")
	}
	info, _ := os.Stat(configDirectory + "/tor/keys/private_key")
	if info.Mode().Perm() != 0600 {
		t.Fatalf("private key mode is %o, want 0600", info.Mode().Perm())
	}
	public, err := arti.PublicKeyFromPrivate(private)
	if err != nil {
		t.Fatalf("stored private key does not derive a public key: %v", err)
	}

	// The address is known while offline, and signatures made now verify against it with plain ed25519, which is
	// what every peer uses to check them
	address := tor.Address()
	if len(address) != 56 {
		t.Fatalf("address %q is not an onion service ID", address)
	}
	fromAddress, err := arti.PublicKeyFromOnionID(address)
	if err != nil || !fromAddress.Equal(ed25519.PublicKey(public)) {
		t.Fatalf("address does not match the stored public key")
	}
	message := []byte("created a profile before tor was up")
	signature := tor.Sign(message)
	if signature == nil || !ed25519.Verify(public, message, signature) {
		t.Fatalf("signature made before tor started does not verify")
	}
	if !tor.VerifySignature(address, message, signature) {
		t.Fatalf("the transport does not verify its own signature")
	}

	// Loading the same directory again is the same identity, not a new one
	again := &Network{}
	again.Load(configDirectory, testIdentityFileKey)
	if again.Address() != address {
		t.Fatalf("a second load produced a different address")
	}
}

// An install from before identities were encrypted has the 64-byte key in the clear.  It keeps its address, and the
// file is sealed the first time it is loaded with a key.
func TestAnIdentityStoredInTheClearIsSealedAndKept(t *testing.T) {
	configDirectory := t.TempDir()
	if err := os.MkdirAll(configDirectory+"/tor/keys", 0700); err != nil {
		t.Fatal(err)
	}
	public, private := generateHiddenServiceKey()
	privateKeyFile := configDirectory + "/tor/keys/private_key"
	if err := os.WriteFile(privateKeyFile, private, 0600); err != nil {
		t.Fatal(err)
	}

	tor := &Network{}
	tor.Load(configDirectory, testIdentityFileKey)
	if !bytes.Equal(tor.publicKey, public) {
		t.Fatalf("loading the plaintext key changed the identity")
	}
	address := tor.Address()

	stored, err := os.ReadFile(privateKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) == arti.PrivateKeySize || bytes.Contains(stored, private[:16]) {
		t.Fatalf("the private key is still on disk in the clear")
	}
	opened, err := openSealedPrivateKey(stored, testIdentityFileKey)
	if err != nil || !bytes.Equal(opened, private) {
		t.Fatalf("the sealed file does not hold the same private key: %v", err)
	}
	if _, err := os.Stat(privateKeyFile + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("the temporary file was left behind")
	}
	info, _ := os.Stat(privateKeyFile)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("private key mode is %o, want 0600", info.Mode().Perm())
	}

	again := &Network{}
	again.Load(configDirectory, testIdentityFileKey)
	if again.Address() != address {
		t.Fatalf("the sealed key loads as a different identity")
	}
}

func TestAnIdentitySealedUnderAnotherKeyRefusesToStart(t *testing.T) {
	// A key file that does not belong to this identity must not lead to a new identity being made in its place
	configDirectory := t.TempDir()
	tor := &Network{}
	tor.Load(configDirectory, testIdentityFileKey)
	before, err := os.ReadFile(configDirectory + "/tor/keys/private_key")
	if err != nil {
		t.Fatal(err)
	}

	other := &Network{}
	if !exitsFatally(t, func() { other.Load(configDirectory, bytes.Repeat([]byte{0x17}, 32)) }) {
		t.Fatal("an identity sealed under another key was accepted")
	}
	after, err := os.ReadFile(configDirectory + "/tor/keys/private_key")
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("the identity file was changed by a failed load")
	}
}

// exitsFatally runs fn with logrus's exit replaced by a panic, and reports whether fn reached a fatal log.  The
// tor package registers no exit handler, so this is safe here.
func exitsFatally(t *testing.T, fn func()) (fatal bool) {
	t.Helper()
	previous := log.StandardLogger().ExitFunc
	log.StandardLogger().ExitFunc = func(int) { panic("fatal") }
	defer func() {
		log.StandardLogger().ExitFunc = previous
		if r := recover(); r != nil {
			fatal = true
		}
	}()
	fn()
	return false
}

func TestAPrivateKeyOfTheWrongSizeRefusesToStart(t *testing.T) {
	// The private key file is the user's identity.  A damaged one must not be silently replaced or padded, which
	// would change their address or sign with garbage; the app stops and says which file is wrong.
	configDirectory := t.TempDir()
	if err := os.MkdirAll(configDirectory+"/tor/keys", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configDirectory+"/tor/keys/private_key", []byte("truncated"), 0600); err != nil {
		t.Fatal(err)
	}
	tor := &Network{}
	if !exitsFatally(t, func() { tor.Load(configDirectory, testIdentityFileKey) }) {
		t.Fatal("a private key file of the wrong size was accepted")
	}
}

func TestTheStoredPublicKeyFileIsNotTrusted(t *testing.T) {
	// Only the private key is the identity; whatever a public_key file from an older layout says is ignored, and
	// the address always comes from the private key
	configDirectory := t.TempDir()
	tor := &Network{}
	tor.Load(configDirectory, testIdentityFileKey)
	address := tor.Address()

	if err := os.WriteFile(configDirectory+"/tor/keys/public_key", make([]byte, 32), 0600); err != nil {
		t.Fatal(err)
	}
	again := &Network{}
	again.Load(configDirectory, testIdentityFileKey)
	if again.Address() != address {
		t.Fatalf("a stray public_key file changed the address")
	}
}

// The sealed identity file is a format installs depend on: this one was sealed on 2026-11-23 by an implementation
// that shares no code with this package, the way the file is documented.  A change that fails this leaves every
// install unable to load its identity.
func TestAnIdentitySealedBeforeStillOpens(t *testing.T) {
	sealed, err := hex.DecodeString("a0a1a2a3a4a5a6a7a8a9aaab2db2ed91a0b048f13dbc00eccd0f72f8eb1c44a67f17f073b23f31dee02f5cdd" +
		"738b5ede3d2152f4e75601e7995c898a7380e4d289336364d8766ee1555aa04594851ab44e494cfbd67d0c959680c1ae")
	if err != nil {
		t.Fatal(err)
	}
	private := make([]byte, arti.PrivateKeySize)
	for i := range private {
		private[i] = byte(0x80 + i)
	}

	opened, err := openSealedPrivateKey(sealed, testIdentityFileKey)
	if err != nil || !bytes.Equal(opened, private) {
		t.Fatalf("a private key sealed earlier no longer opens: %v", err)
	}
}
