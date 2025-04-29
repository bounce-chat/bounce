package chat

import "github.com/google/uuid"

const fileChunkSize = 1024 * 1024 * 10 // 10MiB

type fileChunk struct {
	ID   uuid.UUID
	Hash string
	Size int
	Data []byte
}

type fileReference struct {
	ID     uuid.UUID
	Hash   string
	Size   int
	Chunks []chunkOffer
}

type chunkOffer struct {
	ID              uuid.UUID
	FileReferenceID uuid.UUID
	Size            int
	Location        string
	Hash            string
}

// discover files we need
// discover who has all or parts of files, described as chunks / references
// query hosts for their chunks to assemble the files
// handle requests for file chunks

// when a file is completed, check if we need to set it as a group or profile picture
