package chat

import (
	"bytes"
	"encoding/hex"
	"errors"
	"image"
	"io"
	"io/ioutil"
	"math/rand"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"github.com/zeebo/blake3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var writingChunk = map[string]bool{}
var writingChunkMutex sync.Mutex
var fileDataDownloaded = map[uuid.UUID]int64{}

const EmbeddedFileLimit = 1024 * 1024 * 20 // 20MiB
const fileChunkSize = 1024 * 1024          // 1MiB
const expectedChunkDeliverySeconds = 10    // 100KiB/s

const downloadExtension = ".bouncedownload"

const fileTypeGroupImage = 0
const fileTypeUserImage = 1
const fileTypeMessageAttachment = 2

var fileMutex sync.Mutex
var chunkOfferMutex sync.Mutex
var chunkMutex sync.Mutex
var chunkRequestMutex sync.Mutex

var ErrFileTooBig = errors.New("file is too large")
var errFileNotFound = errors.New("file not found")
var errChunkDataNotFound = errors.New("chunk data not found")
var errInvalidImage = errors.New("invalid image data")

type file struct {
	ID              uuid.UUID `gorm:"type:uuid;primary_key;"`
	Name            string
	Type            int
	AttachedTo      uuid.UUID
	Hash            string
	Size            int64
	ChunkSize       int
	HashList        string
	Path            string `msgpack:"-" gorm:"not null"`
	Wanted          bool   `msgpack:"-"`
	Downloaded      bool   `msgpack:"-"`
	Scope           int
	Destination     uuid.UUID
	Author          uuid.UUID
	Timestamp       int64
	Chunks          []chunk `msgpack:"-"`
	Signer          string  `msgpack:"-" gorm:"not null"`
	OriginalPayload []byte  `msgpack:"-" gorm:"not null"`
	Signature       []byte  `msgpack:"-" gorm:"not null"`
	payload         []byte
	payloadMutex    sync.Mutex
}

func (f *file) AfterDelete(tx *gorm.DB) error {
	if f.ID == uuid.Nil {
		return nil
	}
	err := tx.Clauses(clause.Returning{}).Where("file_id = ?", f.ID).Delete(&chunk{}).Error
	if err != nil {
		return err
	}
	err = tx.Clauses(clause.Returning{}).Where("file_id = ?", f.ID).Delete(&chunkOffer{}).Error
	if err != nil {
		return err
	}
	return nil
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

func (b *Bounce) handleFile(peer string, payload []byte, catchUp bool) broadcastable {
	fileMutex.Lock()
	defer fileMutex.Unlock()

	// Look up the device that sent it
	_, exists := b.getDeviceFromAddress(peer)
	if !exists {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("ignoring a file sent from an unknown device")
		return nil
	}

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

	// Make sure that the user that created this signed container is the actor
	if !b.signedByUser(sc, f.Author) {
		log.WithFields(log.Fields{
			"peer":           peer,
			"author":         f.Author,
			"signing_device": sc.Signer,
		}).Warn("ignoring file that could not be signature validated")
		return nil
	}

	// Ignore anything from a blocked user
	if blockedAuthor(&f) {
		log.WithFields(log.Fields{
			"id":     f.ID,
			"author": f.getAuthor(),
		}).Warn("ignoring file from blocked user")

		if peerDev, ok := b.getDeviceFromAddress(peer); ok {
			if !blockedUser(peerDev.UserID) {
				go b.sendAck(peer, typeFile, f.ID)
			}
		}
		return nil
	}

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

	// Make sure the signing device was not revoked before creating this
	var signerDevice device
	err = b.database.Select("revoked_at").Where("address = ?", f.Signer).First(&signerDevice).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"address": f.Signer,
			}).Error("signer device not found for file")
			return nil
		} else {
			log.WithFields(log.Fields{
				"address": f.Signer,
				"error":   err.Error(),
			}).Fatal("database error looking up signing device")
		}
	}
	if signerDevice.RevokedAt != 0 && signerDevice.RevokedAt < f.Timestamp {
		log.WithFields(log.Fields{
			"id":     f.ID,
			"signer": f.Signer,
		}).Warn("ignoring file signed by revoked device")
		go b.sendAck(peer, typeFile, f.ID)
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

	// Create the empty chunks of the file
	for i, chunkHash := range strings.Split(f.HashList, ",") {
		chunkID, err := uuid.FromBytes([]byte(chunkHash)[:16])
		if err != nil {
			log.Fatal("cannot make uuid from bytes for chunk")
		}
		c := chunk{
			ID:    xor(f.ID, chunkID),
			Hash:  chunkHash,
			Index: i,
		}

		f.Chunks = append(f.Chunks, c)
	}

	// We don't want to auto-download message attachments from threads that aren't regularly read
	if f.Type == fileTypeGroupImage || f.Type == fileTypeUserImage {
		f.Wanted = true
	}
	if f.Type == fileTypeMessageAttachment && f.embedded() {
		f.Wanted = true
	}
	if f.embedded() {
		f.Path = b.configDirectory + "/blobs/" + f.ID.String()
	}

	// Save the new file
	err = b.database.Create(&f).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving file")
	}

	// Copy data from any existing chunks where possible
	for _, c := range f.Chunks {
		data, err := b.getChunkData(c.Hash)
		if err == nil {
			c.Data = data
			b.writeChunkToDisk(c)
		}
	}

	// Request any missing chunks
	b.makeNextChunkRequests()

	return &f
}

func (f *file) embedded() bool {
	return f.Size <= EmbeddedFileLimit
}

type chunkOffer struct {
	ID              uuid.UUID `gorm:"type:uuid;primary_key;"`
	Author          uuid.UUID
	FileID          uuid.UUID
	Hash            string
	Location        string
	Timestamp       int64
	Scope           int
	Destination     uuid.UUID
	LastRequestTime int64  `msgpack:"-"`
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

func (b *Bounce) handleChunkOffer(peer string, payload []byte, catchUp bool) broadcastable {
	chunkOfferMutex.Lock()
	defer chunkOfferMutex.Unlock()

	// Unpack the offer
	sc, err := b.unpackSignedContainer(payload)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unpacking signed container for chunk offer")
		return nil
	}
	var co chunkOffer
	err = msgpack.Unmarshal(sc.Payload, &co)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling chunk offer")
		return nil
	}
	co.OriginalPayload = sc.Payload
	co.Signature = sc.Signature
	co.Signer = sc.Signer

	// Make sure that the user that created this signed container is the actor
	if !b.signedByUser(sc, co.Author) {
		log.WithFields(log.Fields{
			"peer":           peer,
			"author":         co.Author,
			"signing_device": sc.Signer,
		}).Warn("ignoring chunk offer that could not be signature validated")
		return nil
	}

	// Ignore anything from a blocked user
	if blockedAuthor(&co) {
		log.WithFields(log.Fields{
			"id":     co.ID,
			"author": co.getAuthor(),
		}).Warn("ignoring chunk offer from blocked user")

		if peerDev, ok := b.getDeviceFromAddress(peer); ok {
			if !blockedUser(peerDev.UserID) {
				go b.sendAck(peer, typeChunkOffer, co.ID)
			}
		}
		return nil
	}

	// Check if we already have this offer
	var existingChunkOffer chunkOffer
	err = b.database.Where("id = ?", co.ID).First(&existingChunkOffer).Error
	if err == nil {
		return &existingChunkOffer
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up chunk offer")
	}

	// Save the offer
	err = b.database.Create(&co).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving chunk offer")
	}

	// Find all chunks that have this hash, make requests if we're missing any of them
	var chunks []chunk
	err = b.database.Select("id", "downloaded").Where("hash = ?", co.Hash).Find(&chunks).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up chunks")
	}
	if len(chunks) == 0 {
		// Sometimes chunk offers come in on the wire before the files do, so we might
		// get offers before we know about the files they are for
		return &co
	}
	allDownloaded := true
	for _, c := range chunks {
		if !c.Downloaded {
			allDownloaded = false
			break
		}
	}
	if !allDownloaded {
		b.makeNextChunkRequests()
	}
	return &co
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

func (b *Bounce) handleChunkRequest(peer string, payload []byte, catchUp bool) broadcastable {
	// Unmarshal the request
	var cr chunkRequest
	err := msgpack.Unmarshal(payload, &cr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling chunk request")
		return nil
	}

	// Make sure we aren't currently writing or queued to write this data to this peer already
	writingChunkMutex.Lock()
	if _, alreadyWriting := writingChunk[peer+cr.Hash]; alreadyWriting {
		writingChunkMutex.Unlock()
		return nil
	} else {
		writingChunk[peer+cr.Hash] = true
		writingChunkMutex.Unlock()
		defer func() {
			writingChunkMutex.Lock()
			delete(writingChunk, peer+cr.Hash)
			writingChunkMutex.Unlock()
		}()
	}

	// Make sure the peer has permission to access a file that contains this chunk
	if !b.peerCanHaveChunk(peer, cr.Hash) {
		log.WithFields(log.Fields{
			"peer": peer,
			"hash": cr.Hash,
		}).Warn("peer requested chunk they are not allowed to have")
		return nil
	}

	chunkData, err := b.getChunkData(cr.Hash)
	if err == nil {
		b.sendDirect(peer, &chunk{Data: chunkData})
	} else {
		log.WithFields(log.Fields{
			"error": err.Error(),
			"hash":  cr.Hash,
			"peer":  peer,
		}).Warn("error getting requested chunk")
	}
	return nil
}
func (b *Bounce) getChunkData(hash string) ([]byte, error) {
	// Find all the possible chunks with the data that is being requested
	var cs []chunk
	err := b.database.Find(&cs, "hash = ? AND downloaded = ?", hash, true).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up chunks")
	}

	for _, c := range cs {
		// Find the file that this chunk is a part of
		var f file
		err = b.database.Select("chunk_size", "path", "size", "downloaded").Where("id = ?", c.FileID).First(&f).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"file_id":  c.FileID,
				"chunk_id": c.ID,
			}).Warn("peer sent request for file chunk with unknown file")
			return []byte{}, errFileNotFound
		} else if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up file")
		}

		// Load the chunk data from disk
		filename := f.Path
		if !f.embedded() && !f.Downloaded {
			filename += downloadExtension
		}
		fh, err := os.Open(filename)
		if err != nil {
			log.WithFields(log.Fields{
				"file_id": c.FileID,
				"path":    f.Path,
				"error":   err.Error(),
			}).Warn("error opening file containing chunk data")
			continue
		}

		start := int64(c.Index * f.ChunkSize)
		sought, err := fh.Seek(start, 0)
		if err != nil {
			log.WithFields(log.Fields{
				"path":  f.Path,
				"error": err.Error(),
			}).Warn("error seeking file containing chunk data")
			continue
		}
		if sought != start {
			log.WithFields(log.Fields{
				"path":   f.Path,
				"start":  start,
				"sought": sought,
			}).Warn("seeking file containing chunk data did not seek to correct location")
			continue
		}

		data := make([]byte, f.ChunkSize)
		n, err := fh.Read(data)
		if err != nil && err != io.EOF {
			log.WithFields(log.Fields{
				"path":  f.Path,
				"error": err.Error(),
			}).Warn("error reading file containing chunk data")
			continue
		}

		c.Data = data[:n]

		// Make sure that the hash of the data matches what has been requested, if so send it over and break
		if hash == hashString(blake3.Sum256(c.Data)) {
			return c.Data, nil
		} else {
			log.WithFields(log.Fields{
				"chunk": c.ID,
				"hash":  hash,
				"file":  f.ID,
			}).Warn("chunk data does not match hash")
		}
	}

	return []byte{}, errChunkDataNotFound
}

func (b *Bounce) peerCanHaveChunk(peer, hash string) bool {
	// Get all the files that have a chunk with this hash
	var chunks []chunk
	err := b.database.Select("file_id").Where("hash = ?", hash).Find(&chunks).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up chunks")
	}

	for _, c := range chunks {
		var f file
		err = b.database.First(&f, "id = ?", c.FileID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"file_id": c.FileID,
				}).Warn("chunk belongs to file that does not exist")
				continue
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error looking up file")
			}
		}

		addrs := b.getBroadcastScope(&f, false)
		for _, addr := range addrs {
			if addr == peer {
				return true
			}
		}
	}

	return false
}

type chunk struct {
	ID           uuid.UUID `gorm:"type:uuid;primary_key;" msgpack:"-"`
	FileID       uuid.UUID `msgpack:"-"`
	Hash         string    `msgpack:"-"`
	Index        int       `msgpack:"-"`
	Downloaded   bool      `msgpack:"-"`
	Data         []byte    `gorm:"-"`
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

func (b *Bounce) handleChunk(peer string, payload []byte, catchUp bool) broadcastable {
	chunkMutex.Lock()
	defer chunkMutex.Unlock()
	chunkRequestMutex.Lock()

	// Unmarshal the chunk
	var incomingChunk chunk
	err := msgpack.Unmarshal(payload, &incomingChunk)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling chunk")
		chunkRequestMutex.Unlock()
		return nil
	}

	// Hash the data
	hash := blake3.Sum256(incomingChunk.Data)

	// Find any chunks in the database that have this hash and are empty
	var targetChunks []chunk
	err = b.database.Where("hash = ? AND downloaded = ?", hashString(hash), false).Find(&targetChunks).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up chunks")
	}
	for _, c := range targetChunks {
		c.Data = incomingChunk.Data

		b.writeChunkToDisk(c)
	}

	chunkRequestMutex.Unlock()
	b.makeNextChunkRequests()
	return nil
}

func (b *Bounce) writeChunkToDisk(c chunk) {
	// Find the file that contains this chunk
	var f file
	err := b.database.Select("chunk_size", "path", "size", "hash", "wanted").Where("id = ?", c.FileID).First(&f).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"file_id": c.FileID,
		}).Warn("file not found for chunk")
		return
	} else if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up file")
	}
	if !f.Wanted {
		return
	}

	// Write the data to disk
	path := f.Path
	if !f.embedded() {
		path += downloadExtension
	}
	fh, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		log.WithFields(log.Fields{
			"path":  f.Path,
			"error": err.Error(),
		}).Warn("error opening file for writing chunk data")
		return
	}

	start := int64(c.Index * f.ChunkSize)
	sought, err := fh.Seek(start, 0)
	if err != nil {
		log.WithFields(log.Fields{
			"path":  f.Path,
			"error": err.Error(),
		}).Warn("error seeking file for writing chunk data")
		return
	}
	if sought != start {
		log.WithFields(log.Fields{
			"path":   f.Path,
			"start":  start,
			"sought": sought,
		}).Warn("seeking file for writing chunk data did not seek to correct location")
		return
	}

	_, err = fh.Write(c.Data)
	if err != nil {
		log.WithFields(log.Fields{
			"path":  f.Path,
			"error": err.Error(),
		}).Warn("error writing file containing chunk data")
		return
	}

	if !f.embedded() {
		progress, _ := fileDataDownloaded[c.FileID]
		progress += int64(len(c.Data))
		fileDataDownloaded[c.FileID] = progress
		b.ui.FileDownloadProgress(c.FileID, float64(progress)/float64(f.Size))
	}

	// Update the download status
	err = b.database.Model(&c).Update("downloaded", true).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("database error updating chunk data")
	}

	// Mark the file as downloaded if this was the last chunk to get
	var otherEmptyChunks []chunk
	err = b.database.Where("file_id = ? AND downloaded = ?", c.FileID, false).Find(&otherEmptyChunks).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error checking for remaining empty chunks")
	}
	if len(otherEmptyChunks) == 0 {
		err = b.database.Table("files").Where("id = ?", c.FileID).Update("downloaded", true).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error setting file as downloaded")
		}
		b.ui.FileCompleted(c.FileID)

		if !f.embedded() {
			err = os.Rename(f.Path+downloadExtension, f.Path)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error renaming completed file")
			}
		}
		_, hash := checkFileHash(f.Path)
		if hash != f.Hash {
			log.WithFields(log.Fields{
				"expected": f.Hash,
				"actual":   hash,
			}).Error("completed file does not have expected hash")
		}
	}

	// Offer this chunk
	b.offerChunk(c)
}

func (b *Bounce) makeNextChunkRequests() {
	chunkRequestMutex.Lock()
	defer chunkRequestMutex.Unlock()

	var unfinishedFiles []file
	err := b.database.Preload(clause.Associations).Where("wanted = ? AND downloaded = ?", true, false).Find(&unfinishedFiles).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up unfinished files")
	}

	alreadyHitPeer := map[string]bool{}
	recheck := false
	for _, f := range unfinishedFiles {
		for _, c := range f.Chunks {
			if c.Downloaded {
				continue
			}
			// Find all the offers where we just requested this chunk a few seconds ago, or where we have requested this chunk before from a peer that is
			// actively delivering chunks right now.  If either exists, we don't need to make any additional requests for this chunk.
			activeOffers := []chunkOffer{}
			err = b.database.Where("hash = ? AND last_request_time > ?", c.Hash, time.Now().Unix()-expectedChunkDeliverySeconds).Find(&activeOffers).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error looking up actively used chunk offers")
			}
			if len(activeOffers) > 0 {
				continue
			}

			// If there are no actively used offers for this chunk we choose a new offer to try, one that hasn't been tried before
			unusedOffers := []chunkOffer{}
			err = b.database.Where("hash = ? AND last_request_time = ?", c.Hash, 0).Find(&unusedOffers).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error looking up unused chunk offers")
			}
			availableUnusedLocations := []string{}
			for _, offer := range unusedOffers {
				_, alreadyHit := alreadyHitPeer[offer.Location]
				rd := b.getRemoteDevice(offer.Location)
				if rd.connectedSockets.Load() > 0 && !alreadyHit {
					availableUnusedLocations = append(availableUnusedLocations, offer.Location)
				}
			}
			if len(availableUnusedLocations) > 0 {
				r := rand.New(rand.NewSource(time.Now().UnixNano()))
				location := availableUnusedLocations[r.Intn(len(availableUnusedLocations))]
				go b.sendDirect(location, &chunkRequest{Hash: c.Hash})
				alreadyHitPeer[location] = true
				err = b.database.Table("chunk_offers").Where("hash = ? AND location = ?", c.Hash, location).Update("last_request_time", time.Now().Unix()).Error
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
						"hash":  c.Hash,
						"peer":  location,
					}).Fatal("database error updating last request time on chunk")
				}
				recheck = true
				continue
			}

			// Lastly, if our only option is to re-request the chunk from a peer that we have already tried, we try again
			previouslyTriedOffers := []chunkOffer{}
			err = b.database.Where("hash = ? AND last_request_time != ?", c.Hash, 0).Find(&previouslyTriedOffers).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error looking up previously tried chunk offers")
			}
			availableRetryLocations := []string{}
			for _, offer := range previouslyTriedOffers {
				_, alreadyHit := alreadyHitPeer[offer.Location]
				rd := b.getRemoteDevice(offer.Location)
				if rd.connectedSockets.Load() > 0 && !alreadyHit {
					availableRetryLocations = append(availableRetryLocations, offer.Location)
				}
			}
			if len(availableRetryLocations) > 0 {
				r := rand.New(rand.NewSource(time.Now().UnixNano()))
				location := availableRetryLocations[r.Intn(len(availableRetryLocations))]

				go b.sendDirect(location, &chunkRequest{Hash: c.Hash})
				alreadyHitPeer[location] = true
				err = b.database.Table("chunk_offers").Where("hash = ? AND location = ?", c.Hash, location).Update("last_request_time", time.Now().Unix()).Error
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
						"hash":  c.Hash,
						"peer":  location,
					}).Fatal("database error updating last request time on chunk")
				}
				recheck = true
			}
		}
	}

	if recheck {
		go func() {
			time.Sleep(expectedChunkDeliverySeconds + 1*time.Second)
			b.makeNextChunkRequests()
		}()
	}
}

func (b *Bounce) embedFile(fileID uuid.UUID, data []byte, scope int, destination uuid.UUID, fileType int, attachment uuid.UUID) error {
	if len(data) > EmbeddedFileLimit {
		return ErrFileTooBig
	}

	hash := blake3.Sum256(data)

	chunks, hashList := splitChunks(fileID, data)

	f := &file{
		ID:          fileID,
		Type:        fileType,
		AttachedTo:  attachment,
		Path:        b.configDirectory + "/blobs/" + fileID.String(),
		Hash:        hashString(hash),
		Size:        int64(len(data)),
		ChunkSize:   fileChunkSize,
		Wanted:      true,
		Downloaded:  true,
		Scope:       scope,
		Destination: destination,
		Timestamp:   time.Now().Unix(),
		Author:      b.currentUserID(),
		HashList:    hashList,
		Chunks:      chunks,
	}
	var err error
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
	if err != nil {
		return err
	}
	os.WriteFile(b.configDirectory+"/blobs/"+fileID.String(), data, 0644)

	b.broadcast(f)

	for _, c := range f.Chunks {
		b.offerChunk(c)
	}

	return nil
}

func (b *Bounce) seedFile(fileID uuid.UUID, path string, scope int, destination uuid.UUID, fileType int, attachment uuid.UUID) error {
	fh, err := os.Open(path)
	if err != nil {
		return err
	}

	info, err := fh.Stat()
	if err != nil {
		return err
	}

	f := &file{
		ID:          fileID,
		Type:        fileType,
		Path:        path,
		AttachedTo:  attachment,
		Size:        info.Size(),
		ChunkSize:   fileChunkSize,
		Wanted:      true,
		Downloaded:  true,
		Scope:       scope,
		Destination: destination,
		Timestamp:   time.Now().Unix(),
		Author:      b.currentUserID(),
	}

	chunks := []chunk{}
	hashList := []string{}
	hasher := blake3.New()

	read := int64(0)
	i := 0
	for read < info.Size() {
		chunkData := make([]byte, fileChunkSize)
		n, err := fh.Read(chunkData)
		read += int64(n)
		if err != nil {
			if !(err == io.EOF && read == info.Size()) {
				return err
			}
		}
		hasher.Write(chunkData)

		chunkHash := blake3.Sum256(chunkData)
		chunkHashString := hashString(chunkHash)
		chunkID, err := uuid.FromBytes(chunkHash[:16])
		if err != nil {
			return err
		}

		chunks = append(
			chunks,
			chunk{
				ID:    xor(fileID, chunkID),
				Hash:  chunkHashString,
				Index: i,
			},
		)
		hashList = append(
			hashList,
			chunkHashString,
		)

		i++
	}

	allDataHash := make([]byte, 32)
	hasher.Digest().Read(allDataHash)
	f.Hash = hashString([32]byte(allDataHash[:32]))
	f.HashList = strings.Join(hashList, ",")
	f.Chunks = chunks
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
	if err != nil {
		return err
	}

	b.broadcast(f)

	for _, c := range f.Chunks {
		b.offerChunk(c)
	}

	return nil
}

func (b *Bounce) DownloadFileToDisk(fileID uuid.UUID, destination string) {
	// Find the file we want to download
	var f file
	err := b.database.Where("id = ?", fileID).First(&f).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"file_id": fileID,
		}).Error("cannot download unknown file")
		return
	} else if err != nil {
		log.WithFields(log.Fields{
			"file_id": fileID,
		}).Fatal("database error looking up file")
	}

	// Check if it is already on disk anywhere
	destinationExists, destinationHash := checkFileHash(destination)
	sourceExists, sourceHash := checkFileHash(f.Path)
	if destinationExists && destinationHash == f.Hash {
		// If we have the file already at the requested destination, mark it as done in the database and return
		log.WithFields(log.Fields{
			"path": destination,
		}).Info("file requested for download already exists")
		err = b.database.Table("chunks").Where("file_id = ?", fileID).Update("downloaded", true).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("database error updating chunks")
		}
		err = b.database.Table("files").Where("id = ?", fileID).Update("downloaded", true).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("database error updating file")
		}
		err = b.database.Table("files").Where("id = ?", fileID).Update("path", destination).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("database error updating file")
		}
		return
	} else if sourceExists && sourceHash == f.Hash {
		// The file doesn't exist at the destination, but it is still on disk at the last location we had it,
		// so we create a copy at the new destination and update the database
		sourceHandler, err := os.Open(f.Path)
		if err != nil {
			log.WithFields(log.Fields{
				"path":  f.Path,
				"error": err.Error(),
			}).Error("error opening file")
			return
		}

		destinationHandler, err := os.Create(destination)
		if err != nil {
			log.WithFields(log.Fields{
				"path":  destination,
				"error": err.Error(),
			}).Error("error opening file")
			return
		}

		_, err = io.Copy(destinationHandler, sourceHandler)
		sourceHandler.Close()
		destinationHandler.Close()
		if err != nil {
			log.WithFields(log.Fields{
				"source":      f.Path,
				"destination": destination,
				"error":       err.Error(),
			}).Error("error copying data into new destination")
			return
		}

		err = b.database.Table("chunks").Where("file_id = ?", fileID).Update("downloaded", true).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("database error updating chunks")
		}
		err = b.database.Table("files").Where("id = ?", fileID).Update("downloaded", true).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("database error updating file")
		}
		err = b.database.Table("files").Where("id = ?", fileID).Update("path", destination).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("database error updating file")
		}

		return
	}

	// Make sure we're able to create the destination file
	testHandler, err := os.Create(destination)
	if err != nil {
		log.WithFields(log.Fields{
			"path":  destination,
			"error": err.Error(),
		}).Error("error creating destination file")
		return
	} else {
		testHandler.Close()
	}

	// Set up the database for downloading
	err = b.database.Table("chunks").Where("file_id = ?", fileID).Update("downloaded", false).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("database error updating chunks")
	}
	err = b.database.Table("files").Where("id = ?", fileID).Update("downloaded", false).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("database error updating file")
	}
	err = b.database.Table("files").Where("id = ?", fileID).Update("path", destination).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("database error updating file")
	}
	err = b.database.Table("files").Where("id = ?", fileID).Update("wanted", true).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("database error updating file")
	}

	// Start requesting chunks
	fileDataDownloaded[fileID] = 0
	b.ui.FileDownloadProgress(fileID, 0)
	b.makeNextChunkRequests()
}

func checkFileHash(path string) (bool, string) {
	fh, err := os.Open(path)
	if err != nil {
		return false, ""
	}

	info, err := fh.Stat()
	if err != nil {
		return false, ""
	}

	hasher := blake3.New()
	read := int64(0)
	for read < info.Size() {
		chunkData := make([]byte, fileChunkSize)
		n, err := fh.Read(chunkData)
		read += int64(n)
		if err != nil {
			if !(err == io.EOF && read == info.Size()) {
				return false, ""
			}
		}
		hasher.Write(chunkData[:n])
	}

	allDataHash := make([]byte, 32)
	hasher.Digest().Read(allDataHash)
	return true, hashString([32]byte(allDataHash[:32]))
}

func (b *Bounce) CancelDownload(fileID uuid.UUID) {
	err := b.database.Table("files").Where("id = ?", fileID).Update("wanted", false).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("database error updating file")
	}
	err = b.database.Table("files").Where("id = ?", fileID).Update("downloaded", false).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("database error updating file")
	}
	err = b.database.Table("chunks").Where("id = ?", fileID).Update("downloaded", false).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("database error updating file")
	}

	var f file
	err = b.database.Select("path").Where("id = ?", fileID).First(&f).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
			"path":  f.Path + downloadExtension,
		}).Warn("error looking up partially downloaded file for deletion")
		return
	}
	err = os.Remove(f.Path + downloadExtension)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
			"path":  f.Path + downloadExtension,
		}).Warn("error deleting partially downloaded file")
	}
}

func (b *Bounce) offerChunk(c chunk) {
	// Get the scope and destination from the chunk's file
	var f file
	err := b.database.Select("scope", "destination").Where("id = ?", c.FileID).First(&f).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"chunk_id": c.ID,
				"hash":     c.Hash,
				"file_id":  c.FileID,
			}).Error("file not found when trying to offer chunk, cannot attach scope and destination to offer")
			return
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up file")
		}
	}

	// Create the chunk offer
	co := &chunkOffer{
		ID:          uuid.New(),
		Author:      b.currentUserID(),
		FileID:      c.FileID,
		Hash:        c.Hash,
		Location:    b.network.Address(),
		Timestamp:   time.Now().Unix(),
		Scope:       f.Scope,
		Destination: f.Destination,
	}

	// Sign it
	co.OriginalPayload, err = msgpack.Marshal(co)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling chunk offer")
	}
	sc := b.createSignedContainer(co.OriginalPayload)
	co.Signature = sc.Signature
	co.Signer = sc.Signer

	// Save and broadcast it
	err = b.database.Create(co).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving new chunk offer")
	}
	b.broadcast(co)
}

func (b *Bounce) GetFileData(fileID uuid.UUID) ([]byte, error) {
	var f file
	err := b.database.Preload(clause.Associations).Where("id = ? AND downloaded = ?", fileID, true).First(&f).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return []byte{}, errFileNotFound
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up file")
		}
	}

	return ioutil.ReadFile(b.configDirectory + "/blobs/" + fileID.String())
}

func (b *Bounce) FileDownloaded(fileID uuid.UUID) bool {
	var f file
	err := b.database.Select("downloaded").Where("id = ?", fileID).First(&f).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up file")
		}
	}

	return f.Downloaded
}

func (b *Bounce) FileEmbedded(fileID uuid.UUID) bool {
	var f file
	err := b.database.Select("size").Where("id = ?", fileID).First(&f).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up file")
		}
	}

	return f.Size <= EmbeddedFileLimit
}

func (b *Bounce) FileWanted(fileID uuid.UUID) bool {
	var f file
	err := b.database.Select("wanted").Where("id = ?", fileID).First(&f).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up file")
		}
	}

	return f.Wanted
}

func splitChunks(fileID uuid.UUID, data []byte) ([]chunk, string) {
	chunks := []chunk{}
	hashes := []string{}

	index := 0
	for {
		if len(data) < fileChunkSize {
			c := makeChunk(fileID, index, data)
			chunks = append(chunks, c)
			hashes = append(hashes, c.Hash)
			break
		}

		chunkData := data[:fileChunkSize]
		data = data[fileChunkSize:]

		c := makeChunk(fileID, index, chunkData)
		chunks = append(chunks, c)
		hashes = append(hashes, c.Hash)

		index += 1
	}

	return chunks, strings.Join(hashes, ",")
}

func makeChunk(fileID uuid.UUID, index int, data []byte) chunk {
	hash := blake3.Sum256(data)
	chunkID, err := uuid.FromBytes(hash[:16])
	if err != nil {
		log.Fatal("cannot make uuid from bytes for chunk")
	}

	return chunk{
		ID:         xor(fileID, chunkID),
		Hash:       hashString(hash),
		Index:      index,
		Downloaded: true,
	}
}

func hashString(hash [32]byte) string {
	return hex.EncodeToString(hash[:])
}

func validImage(data []byte) bool {
	_, _, err := image.Decode(bytes.NewReader(data))
	return err == nil
}
