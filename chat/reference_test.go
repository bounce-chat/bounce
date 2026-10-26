package chat

import (
	"bytes"
	crand "crypto/rand"
	"io"
	"os"
	"testing"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/alecthomas/assert/v2"
	log "github.com/sirupsen/logrus"
	"github.com/zeebo/blake3"
)

func TestChunkOffersInReferenceFollowOverlapScope(t *testing.T) {
	b, alice, _, _ := createUsersAndGroups(t)

	// Create a new file for my profile picture
	fileID := NewID()
	data := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	hash := blake3.Sum256(data)
	key := make([]byte, 32)
	crand.Read(key)
	var err error
	chunks, hashList, encryptedHashList := splitChunks(fileID, data, key)
	f := &file{
		ID:                fileID,
		Type:              fileTypeUserImage,
		AttachedTo:        b.currentUserID(),
		Path:              os.TempDir() + "/bounce-file-" + NewID().String(),
		Hash:              hashString(hash),
		Size:              int64(len(data)),
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
	}
	f.OriginalPayload, err = msgpack.Marshal(f)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling file")
	}
	sc := b.createSignedContainer(typeFile, f.OriginalPayload)
	f.Signature = sc.Signature
	f.Signer = sc.Signer
	err = b.database.Create(f).Error

	// Create a chunk offer from myself for this new file
	c := chunks[0]
	bco := &chunkOffer{
		ID:          NewID(),
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
	bsc := b.createSignedContainer(typeChunkOffer, bco.OriginalPayload)
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
		ID:          NewID(),
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
	asc := alice.createSignedContainer(typeChunkOffer, aco.OriginalPayload)
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

// Chunk offers are offered by the scope of their file.  The offer queries compare ID columns, which are blobs, so
// the IDs they are compared against have to be bound as IDs; bound as hex strings nothing matches, group attachments
// are never offered to anyone who missed the live broadcast, and nothing is ever excluded as delivered.
func TestChunkOffersAreOfferedByScopeAndExcludedOnceDelivered(t *testing.T) {
	me, alice, _, groupID := createUsersAndGroups(t)

	// A real group attachment, with the chunk offer the engine creates for it
	data := []byte("group attachment payload")
	assert.NoError(t, me.SendGroupMessage(groupID, "group attachment", []OutgoingAttachment{{
		Name:   "f.bin",
		Size:   int64(len(data)),
		Reader: io.NopCloser(bytes.NewReader(data)),
	}}))
	var gm groupMessage
	assert.NoError(t, me.database.Preload("FileAttachments").First(&gm, "text = ?", "group attachment").Error)
	assert.Equal(t, 1, len(gm.FileAttachments))
	var co chunkOffer
	assert.NoError(t, me.database.First(&co, "file_id = ? AND location = ?", gm.FileAttachments[0].FileID, me.network.Address()).Error)
	assert.Equal(t, scopeGroup, co.Scope)

	offered := func(address string, userID ID) bool {
		for _, ref := range me.getChunkOffersToOffer(address, userID) {
			if ref.FrameID == co.ID {
				return true
			}
		}
		return false
	}

	// A co-member's device that missed the live broadcast is offered it, and not once it has been delivered there.
	// Alice's connected device already has it (the live broadcast was acked), so use another device of hers.
	aliceOffline := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	assert.True(t, offered(aliceOffline, alice.currentUserID()))
	me.markDeliveredTo(&co, aliceOffline)
	assert.False(t, offered(aliceOffline, alice.currentUserID()))

	// Our own sync device is offered it too, including when nothing has been delivered to that device yet
	syncDevice := "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"
	assert.True(t, offered(syncDevice, me.currentUserID()))
	me.markDeliveredTo(&co, syncDevice)
	assert.False(t, offered(syncDevice, me.currentUserID()))
}
