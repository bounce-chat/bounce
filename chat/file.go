package chat

import (
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"github.com/zeebo/blake3"
)

//const embeddedFileLimit = 1024 * 1024 * 10 // 10MiB

//var ErrFileTooBig = errors.New("file is too large")

const fileChunkSize = 1024 * 1024 * 10 // 10MiB

type file struct {
	ID          uuid.UUID `gorm:"type:uuid;primary_key;"`
	Scope       int
	Destination uuid.UUID
	Author      uuid.UUID
	Hash        string
	Size        int
	//Wanted    bool `msgpack:"-"`
	//Completed bool `msgpack:"-"`
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
	// TODO
	return nil
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
	// TODO
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
	// TODO
	return nil
}

type chunk struct {
	ID           uuid.UUID `gorm:"type:uuid;primary_key;"`
	FileID       uuid.UUID //`msgpack:"-"`
	Hash         string
	Size         int
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
	// TODO
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

	return chunk{
		ID:   uuid.New(),
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
