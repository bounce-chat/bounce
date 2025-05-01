package chat

import (
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"github.com/zeebo/blake3"
	"gorm.io/gorm"
)

//const embeddedFileLimit = 1024 * 1024 * 10 // 10MiB

//var ErrFileTooBig = errors.New("file is too large")

const fileChunkSize = 1024 * 1024 * 10 // 10MiB

var fileMutex sync.Mutex

type file struct {
	ID          uuid.UUID `gorm:"type:uuid;primary_key;"`
	Scope       int
	Destination uuid.UUID
	Author      uuid.UUID
	Hash        string
	Size        int
	//Wanted    bool `msgpack:"-"`
	//Completed bool `msgpack:"-"`
	//Type      int
	Timestamp       int64
	BlurHash        string
	HashList        string
	Chunks          []chunk `msgpack:"-"`
	Signer          string  `msgpack:"-" gorm:"not null"`
	OriginalPayload []byte  `msgpack:"-" gorm:"not null"`
	Signature       []byte  `msgpack:"-" gorm:"not null"`
	payload         []byte
	payloadMutex    sync.Mutex
}

func (f *file) getID() uuid.UUID {
	return f.ID
}

func (f *file) getScope(myID uuid.UUID) int {
	return f.Scope
}

func (f *file) getDestination(myID uuid.UUID) uuid.UUID {
	return f.Destination
}

func (f *file) getType() uint16 {
	return typeFile
}

func (f *file) getPayload() []byte {
	f.payloadMutex.Lock()
	defer f.payloadMutex.Unlock()

	if len(f.payload) == 0 {
		bytes, err := msgpack.Marshal(signedContainer{
			Payload:   f.OriginalPayload,
			Signature: f.Signature,
			Signer:    f.Signer,
		})
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error marshalling file's signed container")
		}
		f.payload = bytes
	}

	return f.payload
}

func (f *file) getAuthor() uuid.UUID {
	return f.Author
}

func (f *file) getTimestamp() int64 {
	return f.Timestamp
}

func (b *bounce) handleFile(peer string, payload []byte, catchUp bool) broadcastable {
	fileMutex.Lock()
	defer fileMutex.Unlock()

	// TODO: make sure we know about the device?

	// Verify and unpack the signed container
	sc, err := b.unpackSignedContainer(payload)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unpacking signed container for file")
		return nil
	}
	var f file
	err = msgpack.Unmarshal(sc.Payload, &f)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling file")
		return nil
	}
	f.OriginalPayload = sc.Payload
	f.Signature = sc.Signature
	f.Signer = sc.Signer

	// TODO: make sure this wasn't created by a revoked device?

	// Make sure the device that signed this message belongs to the author
	if !b.signedByUser(sc, f.Author) {
		log.WithFields(log.Fields{
			"id":     f.ID,
			"group":  f.Destination,
			"signer": sc.Signer,
			"author": f.Author,
		}).Warn("received file signed by a different user than the author, ignoring")
		return nil
	}

	// If we have already seen this file, all we need to do is mark that this peer has the file and ack it
	var existingFile file
	err = b.database.Where("id = ?", f.ID).First(&existingFile).Error
	if err == nil {
		return &existingFile
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up file")
	}

	// Validate the file
	if len(f.HashList) == 0 {
		log.WithFields(log.Fields{
			"id":   f.ID,
			"peer": peer,
		}).Error("ignoring file with no hashes")
		return nil
	}

	// TODO: check that the purpose makes sense?  i.e. it's for a group image and the author is a member, etc

	// Create the empty chunks of the file
	for i, chunkHash := range strings.Split(f.HashList, ",") {
		chunkID, err := uuid.FromBytes([]byte(chunkHash)[:16])
		if err != nil {
			log.Fatal("cannot make uuid from bytes for chunk")
		}
		f.Chunks = append(f.Chunks, chunk{
			ID:    chunkID,
			Hash:  chunkHash,
			Index: i,
		})
	}

	// Save the file
	err = b.database.Create(&f).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving file")
	}

	return &f
}

type chunkOffer struct {
	ID              uuid.UUID `gorm:"type:uuid;primary_key;"`
	Scope           int
	Destination     uuid.UUID
	Author          uuid.UUID
	Hash            string
	Size            int
	Location        string
	Timestamp       int64
	Signer          string `msgpack:"-" gorm:"not null"`
	OriginalPayload []byte `msgpack:"-" gorm:"not null"`
	Signature       []byte `msgpack:"-" gorm:"not null"`
	payload         []byte
	payloadMutex    sync.Mutex
}

func (co *chunkOffer) getID() uuid.UUID {
	return co.ID
}

func (co *chunkOffer) getScope(myID uuid.UUID) int {
	return co.Scope
}

func (co *chunkOffer) getDestination(myID uuid.UUID) uuid.UUID {
	return co.Destination
}

func (co *chunkOffer) getType() uint16 {
	return typeChunkOffer
}

func (co *chunkOffer) getPayload() []byte {
	co.payloadMutex.Lock()
	defer co.payloadMutex.Unlock()

	if len(co.payload) == 0 {
		bytes, err := msgpack.Marshal(signedContainer{
			Payload:   co.OriginalPayload,
			Signature: co.Signature,
			Signer:    co.Signer,
		})
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error marshalling file's signed container")
		}
		co.payload = bytes
	}

	return co.payload
}

func (co *chunkOffer) getAuthor() uuid.UUID {
	return co.Author
}

func (co *chunkOffer) getTimestamp() int64 {
	return co.Timestamp
}

func (b *bounce) handleChunkOffer(peer string, payload []byte, catchUp bool) broadcastable {
	// if we already have this chunk, do not save this offer and return, acking and continuing to broadcast it
	// save the offer
	// if we do not have the chunk and want the chunk, send a chunk request TODO: or, sync with other requets for this same chunk that might be happening
	return nil
}

type chunkRequest struct {
	Hash         string
	payload      []byte
	payloadMutex sync.Mutex
}

func (cr *chunkRequest) getType() uint16 {
	return typeChunkRequest
}

func (cr *chunkRequest) getPayload() []byte {
	cr.payloadMutex.Lock()
	defer cr.payloadMutex.Unlock()

	if len(cr.payload) == 0 {
		bytes, err := msgpack.Marshal(cr)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal chunk")
		}
		cr.payload = bytes
	}
	return cr.payload
}

func (b *bounce) handleChunkRequest(peer string, payload []byte, catchUp bool) broadcastable {
	// see if we have the chunk
	// make sure this peer is allowed to have the chunk
	// directly send the chunk to this peer
	return nil
}

type chunk struct {
	ID           uuid.UUID `gorm:"type:uuid;primary_key;" msgpack:"-"`
	FileID       uuid.UUID `msgpack:"-"`
	Hash         string    `msgpack:"-"`
	Index        int       `msgpack:"-"`
	Size         int       `msgpack:"-"`
	Data         []byte
	payload      []byte
	payloadMutex sync.Mutex
}

func (c *chunk) getType() uint16 {
	return typeChunk
}

func (c *chunk) getPayload() []byte {
	c.payloadMutex.Lock()
	defer c.payloadMutex.Unlock()

	if len(c.payload) == 0 {
		bytes, err := msgpack.Marshal(c)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal chunk")
		}
		c.payload = bytes
	}
	return c.payload
}

func (b *bounce) handleChunk(peer string, payload []byte, catchUp bool) broadcastable {
	// Unmarshal the chunk
	var c chunk
	err := msgpack.Unmarshal(payload, &c)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling chunk")
		return nil
	}

	// Hash the data
	hash := blake3.Sum256(c.Data)

	// Find any chunks in the database that have this hash and are empty
	var emptyChunk chunk
	err = b.database.Where("hash = ? AND LENGTH(data) == ?", hashString(hash), 0).First(&emptyChunk).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// We already have this chunk data, or someone sent a chunk we didn't ask for
			return nil
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up chunk")
		}
	}

	// Update the data in the empty chunk
	err = b.database.Model(&emptyChunk).Update("data", c.Data).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("database error updating chunk data")
	}

	return nil
}

func (b *bounce) distributeFile(data []byte, scope int, destination uuid.UUID) (uuid.UUID, error) {
	fileID := uuid.New()
	hash := blake3.Sum256(data)

	if validImage(data) {
		// TODO: add blurhash
	}

	chunks, hashList := splitChunks(data)

	f := &file{
		ID:          fileID,
		Hash:        hashString(hash),
		Size:        len(data),
		Scope:       scope,
		Destination: destination,
		Timestamp:   time.Now().Unix(),
		Author:      b.currentUserID(),
		HashList:    hashList,
		Chunks:      chunks,
	}
	err := b.database.Create(f).Error
	if err != nil {
		return uuid.Nil, err
	}

	b.broadcast(f)

	// TODO: send chunkOffers to everyone who is in scope
	//for _, c := range f.Chunks {
	//}

	return fileID, nil
}

func splitChunks(data []byte) ([]chunk, string) {
	chunks := []chunk{}
	hashes := []string{}

	for {
		if len(data) < fileChunkSize {
			c := makeChunk(data)
			chunks = append(chunks, c)
			hashes = append(hashes, c.Hash)
			break
		}

		chunkData := data[:fileChunkSize]
		data = data[fileChunkSize:]

		c := makeChunk(chunkData)
		chunks = append(chunks, c)
		hashes = append(hashes, c.Hash)
	}

	return chunks, strings.Join(hashes, ",")
}

func makeChunk(data []byte) chunk {
	hash := blake3.Sum256(data)
	chunkID, err := uuid.FromBytes(hash[:16])
	if err != nil {
		log.Fatal("cannot make uuid from bytes for chunk")
	}

	return chunk{
		ID:   chunkID,
		Hash: hashString(hash),
		Size: len(data),
		Data: data,
	}
}

func hashString(hash [32]byte) string {
	return hex.EncodeToString(hash[:])
}

func validImage(data []byte) bool {
	// TODO
	return true
}
