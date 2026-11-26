package chat

import (
	"crypto/aes"
	"crypto/cipher"
	crand "crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// What the engine keeps in its blobs directory is encrypted.  A blob is the file's chunks, each sealed on its own with
// AES-256-GCM under the file's local key, so any one of them can be read or written without the rest: a chunk is
// served to a peer, a download is written a chunk at a time in whatever order the chunks arrive, and a video is read
// from the middle.  The chunk's index is the nonce.  It is never used twice, because a key belongs to one file and a
// chunk of that file only ever has one content, the one its hash in the file's record names.  A sealed chunk is 16
// bytes longer than the chunk, so sealed chunk i starts at i times that longer size, and the blob is 16 bytes longer
// than the file for every chunk in it.
//
// The local key is made when the blob is, is kept in the file's record and never leaves the install.  It is not the
// key every recipient of the file holds, which seals chunks for encrypted devices.  A file kept anywhere else is the
// user's own, where the user put it, and is not encrypted.

const sealedChunkOverhead = 16
const sealedChunkSize = fileChunkSize + sealedChunkOverhead

// ErrFileInBlobs is returned in place of the location of a file the engine keeps in its blobs directory, where it is
// encrypted.  Such a file is read with GetFileData or OpenFile.
var ErrFileInBlobs = errors.New("file is kept encrypted in the blobs directory")

var errNoLocalKey = errors.New("blob has no local key")

// A FileReader reads a file the install has all of, from any offset, wherever the file is kept
type FileReader interface {
	io.ReaderAt
	io.Closer
	Size() int64
}

// isBlobPath is whether a file's location is inside the blobs directory, which is what makes it the engine's own and
// what makes it encrypted
func (p Platform) isBlobPath(path string) bool {
	return strings.HasPrefix(path, p.ConfigDirectory+"/blobs/")
}

func newLocalKey() []byte {
	key := make([]byte, 32)
	crand.Read(key)
	return key
}

func blobCipher(localKey []byte) (cipher.AEAD, error) {
	if len(localKey) != 32 {
		return nil, errNoLocalKey
	}
	block, err := aes.NewCipher(localKey)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func sealChunk(blob cipher.AEAD, index int, data []byte) []byte {
	nonce := make([]byte, blob.NonceSize())
	binary.LittleEndian.PutUint32(nonce, uint32(index))
	return blob.Seal(nil, nonce, data, nil)
}

// openChunk opens a sealed chunk onto the end of a buffer, which is returned with the chunk added
func openChunk(blob cipher.AEAD, index int, sealed []byte, onto []byte) ([]byte, error) {
	nonce := make([]byte, blob.NonceSize())
	binary.LittleEndian.PutUint32(nonce, uint32(index))
	return blob.Open(onto, nonce, sealed, nil)
}

// sealBlob seals a whole file held in memory, as it is written to the blobs directory
func sealBlob(localKey []byte, data []byte) ([]byte, error) {
	blob, err := blobCipher(localKey)
	if err != nil {
		return nil, err
	}
	sealed := make([]byte, 0, int64(len(data))+chunksFor(int64(len(data)))*sealedChunkOverhead)
	for index := 0; index*fileChunkSize < len(data); index++ {
		sealed = append(sealed, sealChunk(blob, index, data[index*fileChunkSize:min((index+1)*fileChunkSize, len(data))])...)
	}
	return sealed, nil
}

// readSealedChunk reads one chunk of a blob, which is as much of the file as the chunk at that index holds
func readSealedChunk(fh *os.File, localKey []byte, index int, length int64) ([]byte, error) {
	blob, err := blobCipher(localKey)
	if err != nil {
		return nil, err
	}
	sealed := make([]byte, length+sealedChunkOverhead)
	_, err = fh.ReadAt(sealed, int64(index)*sealedChunkSize)
	if err != nil {
		return nil, err
	}
	return openChunk(blob, index, sealed, nil)
}

// writeSealedChunk writes one chunk of a blob where the chunk at that index belongs
func writeSealedChunk(fh *os.File, localKey []byte, index int, data []byte) error {
	blob, err := blobCipher(localKey)
	if err != nil {
		return err
	}
	_, err = fh.WriteAt(sealChunk(blob, index, data), int64(index)*sealedChunkSize)
	return err
}

// A blobReader reads a blob as the file it holds.  It keeps the last chunk it opened, because whatever plays a video
// asks for it a small piece at a time and would otherwise have each chunk opened many times over.
type blobReader struct {
	file *os.File
	blob cipher.AEAD
	size int64

	heldChunkMutex sync.Mutex
	heldChunk      []byte
	heldIndex      int
	sealed         []byte
}

func (r *blobReader) Size() int64 {
	return r.size
}

func (r *blobReader) Close() error {
	return r.file.Close()
}

func (r *blobReader) ReadAt(p []byte, offset int64) (int, error) {
	if offset < 0 {
		return 0, errors.New("negative offset")
	}
	r.heldChunkMutex.Lock()
	defer r.heldChunkMutex.Unlock()

	read := 0
	for read < len(p) && offset+int64(read) < r.size {
		position := offset + int64(read)
		index := int(position / fileChunkSize)
		within := int(position % fileChunkSize)
		length := int(min(int64(fileChunkSize), r.size-int64(index)*fileChunkSize))

		if index != r.heldIndex {
			if r.sealed == nil {
				r.sealed = make([]byte, sealedChunkSize)
			}
			sealed := r.sealed[:length+sealedChunkOverhead]
			_, err := r.file.ReadAt(sealed, int64(index)*sealedChunkSize)
			if err == io.EOF {
				// The blob ends before the file does, which is not the end of the file
				return read, io.ErrUnexpectedEOF
			}
			if err != nil {
				return read, err
			}

			// A read that takes the whole chunk has it opened straight into place, which is every chunk when a
			// file is read in one go
			if within == 0 && len(p)-read >= length {
				_, err = openChunk(r.blob, index, sealed, p[read:read])
				if err != nil {
					return read, err
				}
				read += length
				continue
			}

			r.heldIndex = -1
			r.heldChunk, err = openChunk(r.blob, index, sealed, r.heldChunk[:0])
			if err != nil {
				return read, err
			}
			r.heldIndex = index
		}
		read += copy(p[read:], r.heldChunk[within:])
	}

	if read < len(p) {
		return read, io.EOF
	}
	return read, nil
}

// A plainReader reads a file kept where the user put it, which is not encrypted
type plainReader struct {
	file *os.File
	size int64
}

func (r *plainReader) Size() int64 {
	return r.size
}

func (r *plainReader) Close() error {
	return r.file.Close()
}

func (r *plainReader) ReadAt(p []byte, offset int64) (int, error) {
	return r.file.ReadAt(p, offset)
}

// readFileAt opens what is at a location as the contents of a file.  A location in the blobs directory holds the file
// sealed under its local key, and anywhere else holds it as it is.  The location is passed apart from the record
// because a download that is still under way is kept at another one.
func (b *Bounce) readFileAt(f file, location string) (FileReader, error) {
	fh, err := b.platform.Files.Open(location)
	if err != nil {
		return nil, err
	}
	if b.platform.isBlobPath(location) {
		blob, err := blobCipher(f.LocalKey)
		if err != nil {
			fh.Close()
			return nil, err
		}
		return &blobReader{file: fh, blob: blob, size: f.Size, heldIndex: -1}, nil
	}

	info, err := fh.Stat()
	if err != nil {
		fh.Close()
		return nil, err
	}
	return &plainReader{file: fh, size: info.Size()}, nil
}

// OpenFile opens a file the install has all of, to be read from any offset.  A file in the blobs directory is
// decrypted as it is read.
func (b *Bounce) OpenFile(fileID ID) (FileReader, error) {
	var f file
	err := b.database.Select("path", "size", "local_key").Where("id = ? AND downloaded = ?", fileID, true).Take(&f).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errFileNotFound
		}
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up file")
	}
	return b.readFileAt(f, f.Path)
}
