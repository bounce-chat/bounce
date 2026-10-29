package network

import (
	"crypto/ed25519"
	"os"
	"testing"

	arti "github.com/bounce-chat/go-arti"
	log "github.com/sirupsen/logrus"
)

// A fresh install has to be able to create a profile straight away, which means signing and knowing our own address
// before Tor has bootstrapped, let alone published the service.  So the identity is generated when the config is
// loaded, not handed back by Tor later.

func TestAFreshInstallHasAnIdentityBeforeTorStarts(t *testing.T) {
	configDirectory := t.TempDir()
	tor := &TorNetwork{}
	tor.Load(configDirectory)

	// The private key is on disk, private to the user, in the 64-byte expanded form; the public key is derived from it
	private, err := os.ReadFile(configDirectory + "/tor/keys/private_key")
	if err != nil {
		t.Fatalf("no private key written: %v", err)
	}
	if len(private) != arti.PrivateKeySize {
		t.Fatalf("private key is %d bytes, want %d", len(private), arti.PrivateKeySize)
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
	again := &TorNetwork{}
	again.Load(configDirectory)
	if again.Address() != address {
		t.Fatalf("a second load produced a different address")
	}
}

// exitsFatally runs fn with logrus's exit replaced by a panic, and reports whether fn reached a fatal log.  The
// network package registers no exit handler, so this is safe here.
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
	tor := &TorNetwork{}
	if !exitsFatally(t, func() { tor.Load(configDirectory) }) {
		t.Fatal("a private key file of the wrong size was accepted")
	}
}

func TestTheStoredPublicKeyFileIsNotTrusted(t *testing.T) {
	// Only the private key is the identity; whatever a public_key file from an older layout says is ignored, and
	// the address always comes from the private key
	configDirectory := t.TempDir()
	tor := &TorNetwork{}
	tor.Load(configDirectory)
	address := tor.Address()

	if err := os.WriteFile(configDirectory+"/tor/keys/public_key", make([]byte, 32), 0600); err != nil {
		t.Fatal(err)
	}
	again := &TorNetwork{}
	again.Load(configDirectory)
	if again.Address() != address {
		t.Fatalf("a stray public_key file changed the address")
	}
}
