package chat

import (
	"encoding/hex"
	"strings"

	"github.com/google/uuid"
	"github.com/zeebo/blake3"
)

//const embeddedFileLimit = 1024 * 1024 * 10 // 10MiB

//var ErrFileTooBig = errors.New("file is too large")

// discover files we need
// discover who has all or parts of files, described as chunks / references
// query hosts for their chunks to assemble the files
// handle requests for file chunks

// when a file is completed, check if we need to set it as a group or profile picture

const fileChunkSize = 1024 * 1024 * 10 // 10MiB

type file struct {
	ID          uuid.UUID `gorm:"type:uuid;primary_key;"`
	Hash        string
	Size        int
	Scope       uuid.UUID
	Destination uuid.UUID
	//Wanted    bool
	//Completed bool
	//BlurHash string
	HashList string  // ordered, comma-separated list of hashes
	Chunks   []chunk `msgpack:"-"`
}

type chunk struct {
	ID     uuid.UUID `gorm:"type:uuid;primary_key;"`
	FileID uuid.UUID
	Hash   string
	Size   int
	Data   []byte
}

type chunkOffer struct {
	ID       uuid.UUID `gorm:"type:uuid;primary_key;"`
	Hash     string
	Size     int
	Location string
}

type chunkRequest struct {
	Hash string
}

func (b *bounce) storeFile(data []byte) (uuid.UUID, error) {
	fileID := uuid.New()
	hash := blake3.Sum256(data)

	chunks, hashList := splitChunks(data)

	err := b.database.Create(&file{
		ID:   fileID,
		Hash: hashString(hash),
		Size: len(data),
		//Scope
		//Destination
		HashList: hashList,
		Chunks:   chunks,
	}).Error
	if err != nil {
		return uuid.Nil, err
	}

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
