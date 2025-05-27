package chat

import (
	"encoding/hex"
	"errors"
	"math/rand"
	"sort"
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

var lastChunkTime = map[string]int64{}
var lastChunkTimeMutex sync.Mutex
var writingChunk = map[string]bool{}
var writingChunkMutex sync.Mutex

const embeddedFileLimit = 1024 * 1024 * 20 // 20MiB
const fileChunkSize = 1024 * 1024          // 1MiB
const expectedChunkDeliverySeconds = 1     // 100KiB/s

const fileTypeGroupImage = 0
const fileTypeUserImage = 1
const fileTypeMessageAttachment = 2

var fileMutex sync.Mutex
var chunkOfferMutex sync.Mutex
var chunkMutex sync.Mutex
var chunkRequestMutex sync.Mutex

var ErrFileTooBig = errors.New("file is too large")
var errFileNotFound = errors.New("file not found")

type file struct {
	ID              uuid.UUID `gorm:"type:uuid;primary_key;"`
	Name            string
	Type            int
	AttachedTo      uuid.UUID
	Hash            string
	Size            int
	ChunkSize       int
	HashList        string
	Wanted          bool `msgpack:"-"`
	Downloaded      bool `msgpack:"-"`
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
	err := tx.Where("file_id = ?", f.ID).Delete(&chunk{}).Error
	if err != nil {
		return err
	}
	err = tx.Where("file_id = ?", f.ID).Delete(&chunkOffer{}).Error
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

func (b *bounce) handleFile(peer string, payload []byte, catchUp bool) broadcastable {
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
		}).Warn("ignoring file that was not signed by the supposed author")
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

		// If a chunk with the same data already exists in the database, copy the data over and mark this one as downloaded
		var preexistingChunk chunk
		err = b.database.Where("hash =? AND downloaded = ?", chunkHash, true).First(&preexistingChunk).Error
		if err == nil {
			c.Data = preexistingChunk.Data
			c.Downloaded = true
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking for preexisting chunk when creating file")
		}

		f.Chunks = append(f.Chunks, c)
	}

	// We don't want to auto-download message attachments from threads that aren't regularly read
	if f.Type == fileTypeGroupImage {
		f.Wanted = true
	}

	err = b.database.Create(&f).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving file")
	}

	b.makeChunkRequests()

	return &f
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

func (b *bounce) handleChunkOffer(peer string, payload []byte, catchUp bool) broadcastable {
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
		}).Warn("ignoring chunk offer that was not signed by the supposed author")
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
		b.makeChunkRequests()
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

func (b *bounce) handleChunkRequest(peer string, payload []byte, catchUp bool) broadcastable {
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

	// Find the chunk data that is being requested
	var c chunk
	err = b.database.First(&c, "hash = ?", cr.Hash).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"peer": peer,
			"hash": cr.Hash,
		}).Warn("peer sent request for unknown file chunk")
		return nil
	} else if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up chunk")
	}

	// Make sure a file with this chunk should be sent to the user
	if b.peerCanHaveChunk(peer, cr.Hash) {
		b.sendDirect(peer, &c)
	} else {
		log.WithFields(log.Fields{
			"peer":    peer,
			"file_id": c.FileID,
			"hash":    cr.Hash,
		}).Warn("peer requested valid chunk they are not allowed to have")
	}

	return nil
}

func (b *bounce) peerCanHaveChunk(peer, hash string) bool {
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
	chunkMutex.Lock()
	defer chunkMutex.Unlock()
	chunkRequestMutex.Lock()
	defer chunkRequestMutex.Unlock()

	// Track the last time this peer sent a chunk
	lastChunkTimeMutex.Lock()
	lastChunkTime[peer] = time.Now().Unix()
	lastChunkTimeMutex.Unlock()

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
	var targetChunks []chunk
	err = b.database.Where("hash = ? AND downloaded = ?", hashString(hash), false).Find(&targetChunks).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up chunks")
	}
	for _, targetChunk := range targetChunks {
		// Update the data in the empty chunk
		err = b.database.Model(&targetChunk).Update("data", c.Data).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("database error updating chunk data")
		}

		// Update the download status
		err = b.database.Model(&targetChunk).Update("downloaded", true).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("database error updating chunk data")
		}

		// Mark the file as downloaded if this was the last chunk to get
		var otherEmptyChunks []chunk
		err = b.database.Where("file_id = ? AND downloaded = ?", targetChunk.FileID, false).Find(&otherEmptyChunks).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error checking for remaining empty chunks")
		}
		if len(otherEmptyChunks) == 0 {
			err = b.database.Table("files").Where("id = ?", targetChunk.FileID).Update("downloaded", true).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error setting file as downloaded")
			}
			b.userInterface.FileCompleted(targetChunk.FileID)
		}

		// Offer this chunk
		b.offerChunk(targetChunk)
	}

	return nil
}

func (b *bounce) makeChunkRequests() {
	chunkRequestMutex.Lock()
	defer chunkRequestMutex.Unlock()

	var unfinishedFiles []file
	err := b.database.Preload(clause.Associations).Where("wanted = ? AND downloaded = ?", true, false).Find(&unfinishedFiles).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up unfinished files")
	}

	activePeers := []string{}
	lastChunkTimeMutex.Lock()
	for peer, t := range lastChunkTime {
		if t > time.Now().Unix()-expectedChunkDeliverySeconds {
			activePeers = append(activePeers, peer)
		}
	}
	lastChunkTimeMutex.Unlock()

	recheck := false
	for _, f := range unfinishedFiles {
		for _, c := range f.Chunks {
			if c.Downloaded {
				continue
			}
			// Find all the offers where we just requested this chunk a few seconds ago, or where we have requested this chunk before from a peer that is
			// actively delivering chunks right now.  If either exists, we don't need to make any additional requests for this chunk.
			activeOffers := []chunkOffer{}
			err = b.database.Where("hash = ? AND (last_request_time > ? OR location IN (?))", c.Hash, time.Now().Unix()-expectedChunkDeliverySeconds, activePeers).Find(&activeOffers).Error
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
				rd := b.getRemoteDevice(offer.Location)
				if rd.connectedSockets.Load() > 0 {
					availableUnusedLocations = append(availableUnusedLocations, offer.Location)
				}
			}
			if len(availableUnusedLocations) > 0 {
				r := rand.New(rand.NewSource(time.Now().UnixNano()))
				location := availableUnusedLocations[r.Intn(len(availableUnusedLocations))]

				go b.sendDirect(location, &chunkRequest{Hash: c.Hash})
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
				rd := b.getRemoteDevice(offer.Location)
				if rd.connectedSockets.Load() > 0 {
					availableRetryLocations = append(availableRetryLocations, offer.Location)
				}
			}
			if len(availableRetryLocations) > 0 {
				r := rand.New(rand.NewSource(time.Now().UnixNano()))
				location := availableRetryLocations[r.Intn(len(availableRetryLocations))]

				go b.sendDirect(location, &chunkRequest{Hash: c.Hash})
				recheck = true
			}
		}
	}

	if recheck {
		go func() {
			time.Sleep(expectedChunkDeliverySeconds + 1*time.Second)
			b.makeChunkRequests()
		}()
	}
}

func (b *bounce) distributeFile(data []byte, scope int, destination uuid.UUID, fileType int, attachment uuid.UUID) (uuid.UUID, error) {
	if len(data) > embeddedFileLimit {
		return uuid.Nil, ErrFileTooBig
	}

	fileID := uuid.New()
	hash := blake3.Sum256(data)

	chunks, hashList := splitChunks(fileID, data)

	f := &file{
		ID:          fileID,
		Type:        fileType,
		AttachedTo:  attachment,
		Hash:        hashString(hash),
		Size:        len(data),
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
		return uuid.Nil, err
	}

	b.broadcast(f)

	for _, c := range f.Chunks {
		b.offerChunk(c)
	}

	return fileID, nil
}

func (b *bounce) offerChunk(c chunk) {
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

func (b *bounce) getFileData(fileID uuid.UUID) ([]byte, error) {
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

	sort.Slice(f.Chunks, func(i, j int) bool { return f.Chunks[i].Index < f.Chunks[j].Index })

	data := []byte{}
	for _, c := range f.Chunks {
		data = append(data, c.Data...)
	}

	return data, nil
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
		ID:    xor(fileID, chunkID),
		Hash:  hashString(hash),
		Index: index,
		Data:  data,
	}
}

func hashString(hash [32]byte) string {
	return hex.EncodeToString(hash[:])
}
