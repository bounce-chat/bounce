package chat

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/assert/v2"
)

// readableFile is two and a half chunks of something anyone could recognise if it were left as it is
func readableFile() []byte {
	line := []byte("a line of the file that anyone could read\n")
	return bytes.Repeat(line, (2*fileChunkSize+fileChunkSize/2)/len(line))
}

// awaitFile waits until b has all of a file it is fetching on its own, and returns its record
func awaitFile(t *testing.T, b *Bounce, fileID ID) file {
	t.Helper()
	var f file
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b.database.First(&f, "id = ?", fileID).Error == nil && f.Downloaded {
			return f
		}
		b.makeAnyChunkRequests()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the file never arrived")
	return f
}

func TestWhatIsKeptInTheBlobsDirectoryIsEncrypted(t *testing.T) {
	me, alice, _, groupID := createUsersAndGroups(t)
	data := readableFile()

	messageID, mine := sendFile(t, me, groupID, "small enough to embed", OutgoingAttachment{
		Name: "notes.txt", Size: int64(len(data)), Reader: io.NopCloser(bytes.NewReader(data)),
	})
	receiveFile(t, alice, messageID, mine.ID)
	theirs := awaitFile(t, alice, mine.ID)

	// The one who sent it and the one who was sent it each keep it sealed, and each reads back what was sent
	for _, holder := range []struct {
		engine *Bounce
		record file
	}{{me, mine}, {alice, theirs}} {
		assert.True(t, holder.engine.platform.isBlobPath(holder.record.Path))
		onDisk, err := os.ReadFile(holder.record.Path)
		assert.NoError(t, err)
		assert.Equal(t, len(data)+3*sealedChunkOverhead, len(onDisk), "each of the three chunks is sealed on its own")
		assert.False(t, bytes.Contains(onDisk, []byte("a line of the file")))
		read, err := holder.engine.GetFileData(mine.ID)
		assert.NoError(t, err)
		assert.True(t, bytes.Equal(data, read))
	}

	// The key that seals it is made by each install for itself and is no part of what is sent.  It is not the key
	// that everyone who was sent the file holds, which is for encrypted devices.
	assert.Equal(t, 32, len(mine.LocalKey))
	assert.Equal(t, 32, len(theirs.LocalKey))
	assert.NotEqual(t, mine.LocalKey, theirs.LocalKey)
	assert.False(t, bytes.Contains(mine.OriginalPayload, mine.LocalKey))
	assert.Equal(t, mine.EncryptedDeviceKey, theirs.EncryptedDeviceKey)
	assert.NotEqual(t, mine.EncryptedDeviceKey, mine.LocalKey)
}

func TestABlobIsSealedAChunkAtATimeAsItArrives(t *testing.T) {
	me, alice, _, groupID := createUsersAndGroups(t)
	data := readableFile()
	messageID, mine := sendFile(t, me, groupID, "arrives in pieces", OutgoingAttachment{
		Name: "notes.txt", Size: int64(len(data)), Reader: io.NopCloser(bytes.NewReader(data)),
	})
	receiveFile(t, alice, messageID, mine.ID)
	theirs := awaitFile(t, alice, mine.ID)

	// Back to having none of it, to put the chunks in by hand and out of order
	assert.NoError(t, os.Remove(theirs.Path))
	assert.NoError(t, alice.database.Model(&file{}).Where("id = ?", theirs.ID).Update("downloaded", false).Error)
	assert.NoError(t, alice.database.Model(&chunk{}).Where("file_id = ?", theirs.ID).Update("downloaded", false).Error)
	var chunks []chunk
	assert.NoError(t, alice.database.Order("`index`").Find(&chunks, "file_id = ?", theirs.ID).Error)
	assert.Equal(t, 3, len(chunks))
	contents := func(index int) []byte {
		return data[index*fileChunkSize : min((index+1)*fileChunkSize, len(data))]
	}

	// A chunk that is not the length its place calls for would leave the next one in the wrong place
	short := chunks[0]
	short.Data = contents(0)[:fileChunkSize-1]
	alice.writeChunkToDisk(short)
	_, err := os.Stat(theirs.Path + downloadExtension)
	assert.True(t, os.IsNotExist(err))

	// The middle chunk arrives first.  It is sealed where it belongs and can be served from there at once.
	middle := chunks[1]
	middle.Data = contents(1)
	alice.writeChunkToDisk(middle)
	partial, err := os.ReadFile(theirs.Path + downloadExtension)
	assert.NoError(t, err)
	assert.Equal(t, 2*sealedChunkSize, len(partial))
	assert.False(t, bytes.Contains(partial, []byte("a line of the file")))
	assert.NoError(t, alice.database.First(&theirs, "id = ?", theirs.ID).Error)
	served, err := alice.getFileChunkData(theirs, chunks[1].Hash)
	assert.NoError(t, err)
	assert.True(t, bytes.Equal(contents(1), served))
	_, err = alice.GetFileData(theirs.ID)
	assert.Equal(t, errFileNotFound, err)

	// The rest arrives, the whole file is checked against its hash, and it is there to be read
	for _, index := range []int{2, 0} {
		c := chunks[index]
		c.Data = contents(index)
		alice.writeChunkToDisk(c)
	}
	assert.NoError(t, alice.database.First(&theirs, "id = ?", theirs.ID).Error)
	assert.True(t, theirs.Downloaded)
	read, err := alice.GetFileData(theirs.ID)
	assert.NoError(t, err)
	assert.True(t, bytes.Equal(data, read))
}

func TestABlobThatWasChangedOnDiskIsNotRead(t *testing.T) {
	me, _, _, groupID := createUsersAndGroups(t)
	data := readableFile()
	_, f := sendFile(t, me, groupID, "tampered with", OutgoingAttachment{
		Name: "notes.txt", Size: int64(len(data)), Reader: io.NopCloser(bytes.NewReader(data)),
	})
	hashes := strings.Split(f.HashList, ",")
	sealed, err := os.ReadFile(f.Path)
	assert.NoError(t, err)

	// One bit of the second chunk
	changed := bytes.Clone(sealed)
	changed[sealedChunkSize+100] ^= 1
	assert.NoError(t, os.WriteFile(f.Path, changed, 0600))
	_, err = me.GetFileData(f.ID)
	assert.Error(t, err)
	_, err = me.getFileChunkData(f, hashes[1])
	assert.Equal(t, errChunkDataNotFound, err)
	first, err := me.getFileChunkData(f, hashes[0])
	assert.NoError(t, err, "the chunks around it are sealed on their own and still open")
	assert.True(t, bytes.Equal(data[:fileChunkSize], first))

	// Two chunks changing places: each is whole, and neither is where it was sealed to be
	swapped := bytes.Clone(sealed)
	copy(swapped[:sealedChunkSize], sealed[sealedChunkSize:2*sealedChunkSize])
	copy(swapped[sealedChunkSize:], sealed[:sealedChunkSize])
	assert.NoError(t, os.WriteFile(f.Path, swapped, 0600))
	_, err = me.GetFileData(f.ID)
	assert.Error(t, err)

	// Cut short
	assert.NoError(t, os.WriteFile(f.Path, sealed[:len(sealed)-1], 0600))
	_, err = me.GetFileData(f.ID)
	assert.Error(t, err)

	// A record that has lost its key opens nothing
	assert.NoError(t, os.WriteFile(f.Path, sealed, 0600))
	read, err := me.GetFileData(f.ID)
	assert.NoError(t, err)
	assert.True(t, bytes.Equal(data, read))
	assert.NoError(t, me.database.Model(&file{}).Where("id = ?", f.ID).Update("local_key", []byte{}).Error)
	_, err = me.GetFileData(f.ID)
	assert.Equal(t, errNoLocalKey, err)
}

func TestAFileIsReadFromAnyOffsetWhereverItIsKept(t *testing.T) {
	me, _, _, groupID := createUsersAndGroups(t)
	small := readableFile()
	_, embedded := sendFile(t, me, groupID, "kept in blobs", OutgoingAttachment{
		Name: "notes.txt", Size: int64(len(small)), Reader: io.NopCloser(bytes.NewReader(small)),
	})
	source, large := largeSourceFile(t)
	_, seeded := sendFile(t, me, groupID, "kept by the user", OutgoingAttachment{Name: "large.bin", Size: int64(len(large)), Source: source})

	for _, kept := range []struct {
		record file
		data   []byte
	}{{embedded, small}, {seeded, large}} {
		reader, err := me.OpenFile(kept.record.ID)
		assert.NoError(t, err)
		assert.Equal(t, int64(len(kept.data)), reader.Size())

		// Across the line between two chunks, which is two sealed chunks in a blob
		across := make([]byte, 100)
		read, err := reader.ReadAt(across, fileChunkSize-50)
		assert.NoError(t, err)
		assert.Equal(t, 100, read)
		assert.True(t, bytes.Equal(kept.data[fileChunkSize-50:fileChunkSize+50], across))

		// The way a video is played: a small piece at a time, jumping about
		piece := make([]byte, 4096)
		for _, offset := range []int64{0, 4096, 2*fileChunkSize + 7, fileChunkSize / 2, 2 * fileChunkSize} {
			read, err = reader.ReadAt(piece, offset)
			assert.NoError(t, err)
			assert.True(t, bytes.Equal(kept.data[offset:offset+4096], piece[:read]))
		}

		// The end of the file is the end of what is read, and past it there is nothing
		read, err = reader.ReadAt(piece, int64(len(kept.data))-10)
		assert.Equal(t, io.EOF, err)
		assert.Equal(t, 10, read)
		assert.True(t, bytes.Equal(kept.data[len(kept.data)-10:], piece[:10]))
		read, err = reader.ReadAt(piece, int64(len(kept.data)))
		assert.Equal(t, io.EOF, err)
		assert.Equal(t, 0, read)
		assert.NoError(t, reader.Close())
	}

	// A file the user keeps has a place a UI can open for itself.  One in the blobs directory does not.
	location, err := me.GetFilePath(seeded.ID)
	assert.NoError(t, err)
	assert.Equal(t, source, location)
	_, err = me.GetFilePath(embedded.ID)
	assert.Equal(t, ErrFileInBlobs, err)
	_, err = me.OpenFile(NewID())
	assert.Equal(t, errFileNotFound, err)
}

func TestAFileSavedOutOfTheBlobsDirectoryIsWrittenAsItIs(t *testing.T) {
	me, _, _, groupID := createUsersAndGroups(t)
	data := readableFile()
	_, f := sendFile(t, me, groupID, "saved elsewhere", OutgoingAttachment{
		Name: "notes.txt", Size: int64(len(data)), Reader: io.NopCloser(bytes.NewReader(data)),
	})

	// The blobs directory is not somewhere a download can be sent
	inside := me.platform.ConfigDirectory + "/blobs/" + NewID().String()
	me.DownloadFileToDisk(f.ID, inside)
	_, err := os.Stat(inside)
	assert.True(t, os.IsNotExist(err))
	var unchanged file
	assert.NoError(t, me.database.First(&unchanged, "id = ?", f.ID).Error)
	assert.Equal(t, f.Path, unchanged.Path)

	// Anywhere else is the user's, and what lands there is the file itself
	destination := scratchDirectory(t) + "/notes.txt"
	me.DownloadFileToDisk(f.ID, destination)
	saved, err := os.ReadFile(destination)
	assert.NoError(t, err)
	assert.True(t, bytes.Equal(data, saved))
}

func TestAFileThatEndsOnAChunkBoundaryHasNoEmptyChunkAfterIt(t *testing.T) {
	me, alice, _, groupID := createUsersAndGroups(t)
	data := bytes.Repeat([]byte("exactly two chunks long\n"), 2*fileChunkSize/24)
	data = append(data, make([]byte, 2*fileChunkSize-len(data))...)
	assert.Equal(t, 2*fileChunkSize, len(data))

	chunks, hashList, _ := splitChunks(NilID, data, make([]byte, 32))
	assert.Equal(t, 2, len(chunks))
	assert.Equal(t, 2, len(strings.Split(hashList, ",")))

	// Sent with a chunk too many, it would be turned away by whoever it was sent to
	messageID, mine := sendFile(t, me, groupID, "two chunks", OutgoingAttachment{
		Name: "even.bin", Size: int64(len(data)), Reader: io.NopCloser(bytes.NewReader(data)),
	})
	receiveFile(t, alice, messageID, mine.ID)
	awaitFile(t, alice, mine.ID)
	read, err := alice.GetFileData(mine.ID)
	assert.NoError(t, err)
	assert.True(t, bytes.Equal(data, read))
}

// The layout of a blob on disk is fixed: a change to the cipher, the nonce or where a chunk sits leaves every blob
// already written unreadable.  If this fails, the change broke existing installs and the fix is in the code.
func TestABlobStillOpensAsItWasWritten(t *testing.T) {
	localKey := make([]byte, 32)
	for i := range localKey {
		localKey[i] = byte(i)
	}
	data := make([]byte, 2*fileChunkSize+5)
	for i := range data {
		data[i] = byte(i*7 + i/251)
	}

	sealed, err := sealBlob(localKey, data)
	assert.NoError(t, err)
	assert.Equal(t, len(data)+3*sealedChunkOverhead, len(sealed))
	written := sha256.Sum256(sealed)
	assert.Equal(t, sealedBlobFixture, hex.EncodeToString(written[:]))

	path := scratchDirectory(t) + "/blob"
	assert.NoError(t, os.WriteFile(path, sealed, 0600))
	fh, err := os.Open(path)
	assert.NoError(t, err)
	blob, err := blobCipher(localKey)
	assert.NoError(t, err)
	reader := &blobReader{file: fh, blob: blob, size: int64(len(data)), heldIndex: -1}
	read := make([]byte, len(data))
	_, err = reader.ReadAt(read, 0)
	assert.NoError(t, err)
	assert.True(t, bytes.Equal(data, read))
	assert.NoError(t, reader.Close())
}

// The SHA-256 of a blob of 2 MiB and 5 bytes, each byte i being i*7 + i/251, sealed under the key 00 01 02 .. 1f.  It
// was also worked out apart from this code, from the layout alone.
const sealedBlobFixture = "1cf0c3eaf5dcd8ebbe548612f3e68691e26d6e6c51fbd2d7fd651f03ffa6fb6b"
