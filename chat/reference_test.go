package chat

import (
	"crypto/aes"
	"crypto/cipher"
	crand "crypto/rand"
	"os"
	"testing"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/alecthomas/assert/v2"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/zeebo/blake3"
)

func TestChunkOffersInReferenceFollowOverlapScope(t *testing.T) {
	b, alice, _, _ := createUsersAndGroups(t)

	// Create a new file for my profile picture
	fileID := uuid.New()
	data := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	hash := blake3.Sum256(data)
	key := make([]byte, 32)
	crand.Read(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error creating aes cipher from key")
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error creating gcm from block")
	}
	nonce := make([]byte, aesgcm.NonceSize())
	crand.Read(nonce)
	chunks, hashList, encryptedHashList := splitChunks(fileID, data, key, nonce)
	f := &file{
		ID:                fileID,
		Type:              fileTypeUserImage,
		AttachedTo:        b.currentUserID(),
		Path:              os.TempDir() + "/bounce-file-" + uuid.New().String(),
		Hash:              hashString(hash),
		Size:              int64(len(data)),
		ChunkSize:         fileChunkSize,
		Wanted:            true,
		Downloaded:        true,
		Scope:             scopeGlobal,
		Destination:       b.currentUserID(),
		Timestamp:         time.Now().Unix(),
		Author:            b.currentUserID(),
		HashList:          hashList,
		EncryptedHashList: encryptedHashList,
		Chunks:            chunks,
		Key:               key,
		Nonce:             nonce,
	}
	f.OriginalPayload, err = msgpack.Marshal(f)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling file")
	}
	sc := b.createSignedContainer(f.OriginalPayload)
	f.Signature = sc.Signature
	f.Signer = sc.Signer
	err = b.database.Create(f).Error

	// Create a chunk offer from myself for this new file
	c := chunks[0]
	bco := &chunkOffer{
		ID:          uuid.New(),
		Author:      b.currentUserID(),
		FileID:      c.FileID,
		Hash:        c.Hash,
		Location:    b.network.Address(),
		Timestamp:   time.Now().Unix(),
		Scope:       f.Scope,
		Destination: f.Destination,
	}
	bco.OriginalPayload, err = msgpack.Marshal(bco)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling chunk offer")
	}
	bsc := b.createSignedContainer(bco.OriginalPayload)
	bco.Signature = bsc.Signature
	bco.Signer = bsc.Signer
	err = b.database.Create(bco).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving new chunk offer")
	}

	// Create a chunk offer from Alice, so that I know Alice has the chunk
	aco := &chunkOffer{
		ID:          uuid.New(),
		Author:      alice.currentUserID(),
		FileID:      c.FileID,
		Hash:        c.Hash,
		Location:    alice.network.Address(),
		Timestamp:   time.Now().Unix(),
		Scope:       f.Scope,
		Destination: f.Destination,
	}
	aco.OriginalPayload, err = msgpack.Marshal(aco)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling chunk offer")
	}
	asc := alice.createSignedContainer(aco.OriginalPayload)
	aco.Signature = asc.Signature
	aco.Signer = asc.Signer
	err = b.database.Create(aco).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving new chunk offer")
	}

	// Add a new friend for just me, whose name is Carol
	carol := newBounceUser("Carol")
	carolUser, ok := carol.currentUser()
	assert.True(t, ok)
	var cu user
	cb, _ := msgpack.Marshal(carolUser)
	msgpack.Unmarshal(cb, &cu)
	assert.NoError(t, b.database.Create(&cu).Error)

	// Create a reference offer for Carol.  This should offer my profile picture via my chunk offers, but it should
	// not include Alice's chunk offers, since Carol and Alice do not know each other as far as I know.
	ro := b.getReferenceOfferFor(carol.network.Address())
	hasMyChunkOffer := false
	hasAliceChunkOffer := false
	for _, fr := range ro.References {
		if fr.Type == typeChunkOffer {
			if fr.FrameID == bco.ID {
				hasMyChunkOffer = true
			}
			if fr.FrameID == aco.ID {
				hasAliceChunkOffer = true
			}
		}
	}
	assert.True(t, hasMyChunkOffer)
	assert.False(t, hasAliceChunkOffer)
}
