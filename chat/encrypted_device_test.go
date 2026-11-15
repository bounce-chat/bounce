package chat

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/alecthomas/assert/v2"
)

// readUntil reads frames from a connection until one of the wanted type arrives, and returns its payload
func readUntil(t *testing.T, conn net.Conn, wanted uint16) []byte {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		frameType, payload, err := readFrame(conn, true)
		if err != nil {
			t.Fatalf("no frame of type %d arrived: %v", wanted, err)
		}
		if frameType == wanted {
			return payload
		}
	}
}

func opens(key []byte, ciphertext []byte, additionalData []byte) bool {
	block, err := aes.NewCipher(key)
	if err != nil {
		return false
	}
	gcm, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return false
	}
	_, err = gcm.Open(nil, []byte{}, ciphertext, additionalData)
	return err == nil
}

// Anyone can pose a reference offer challenge, choosing both the public key the user agrees with and the bytes the
// user seals.  The answer is sealed under a key derived for that purpose alone, so posing one with a contact's public
// key gets nothing sealed under the agreement itself or under the keys that wrap the conversation with that contact.
func TestAChallengeIsAnsweredUnderAKeyOfItsOwn(t *testing.T) {
	me, alice, _, _ := createUsersAndGroups(t)
	meUser := mustCurrentUser(me)
	aliceUser := mustCurrentUser(alice)

	stranger := newTestnet()
	conn, err := stranger.Dial(me.network.Address())
	assert.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	chosen := make([]byte, 32)
	rand.Read(chosen)
	challenge := &encryptedReferenceOfferChallenge{Key: aliceUser.PublicECDHKey, Challenge: chosen}
	assert.NoError(t, writeFrame(conn, typeEncryptedReferenceOfferChallenge, challenge.getPayload()))

	var response encryptedReferenceOfferResponse
	assert.NoError(t, msgpack.Unmarshal(readUntil(t, conn, typeEncryptedReferenceOfferResponse), &response))
	session := slices.Concat([]byte(stranger.Address()), []byte(me.network.Address()))

	agreement, err := ecdhSharedSecret(meUser.PrivateECDHKey, aliceUser.PublicECDHKey)
	assert.NoError(t, err)
	conversation, err := me.dmThreadSecret(meUser.PrivateECDHKey, aliceUser.PublicECDHKey)
	assert.NoError(t, err)
	assert.False(t, opens(agreement, response.Response, session), "the answer was sealed under the bare agreement between two user keys")
	assert.False(t, opens(conversation, response.Response, session))
	assert.False(t, opens(mailboxWrapKey(conversation, currentMailboxEpoch()), response.Response, session))
	assert.True(t, opens(referenceOfferChallengeKey(agreement), response.Response, session))

	// A host, which holds the private half of the key it posed, derives the same key from its side
	hostKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	assert.NoError(t, err)
	fromUser, err := ecdhSharedSecret(meUser.PrivateECDHKey, hostKey.PublicKey().Bytes())
	assert.NoError(t, err)
	fromHost, err := ecdhSharedSecret(hostKey.Bytes(), meUser.PublicECDHKey)
	assert.NoError(t, err)
	assert.Equal(t, referenceOfferChallengeKey(fromUser), referenceOfferChallengeKey(fromHost))
}
