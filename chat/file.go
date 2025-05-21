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

//const embeddedFileLimit = 1024 * 1024 * 10 // 10MiB
const fileChunkSize = 1024 * 1024      // 1MiB
const expectedChunkDeliverySeconds = 1 // 100KiB/s

var fileMutex sync.Mutex
var chunkOfferMutex sync.Mutex
var chunkMutex sync.Mutex
var chunkRequestMutex sync.Mutex

//var ErrFileTooBig = errors.New("file is too large")
var errFileNotFound = errors.New("file not found")

type file struct {
	ID          uuid.UUID `gorm:"type:uuid;primary_key;"`
	Scope       int
	Destination uuid.UUID
	Author      uuid.UUID
	Hash        string
	Size        int
	ChunkSize   int
	Wanted      bool `msgpack:"-"`
	Downloaded  bool `msgpack:"-"`
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

	// TODO: default to not downloading it until something in the UI tells us to?

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
	f.Wanted = true // TODO: don't start downloads for large files that are part of a message until the user hits the download icon
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
	Scope           int
	Destination     uuid.UUID
	Author          uuid.UUID
	Hash            string
	Location        string
	Timestamp       int64
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

	err = b.database.Create(&co).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving chunk offer")
	}

	var c chunk
	err = b.database.First(&c, "hash = ?", co.Hash).Error
	if err == nil {
		if c.Downloaded {
			return &co
		}
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"hash": co.Hash,
			"peer": peer,
		}).Warn("received chunk offer for unknown chunk")
		return &co
	} else {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up chunk")
	}

	b.makeChunkRequests()

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

	// Find the chunk that is being requested
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

	// Find the file that this chunk is a part of
	var f file
	err = b.database.First(&f, "id = ?", c.FileID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"peer": peer,
			"id":   c.FileID,
		}).Warn("peer sent request for chunk that is not part of a file")
		return nil
	} else if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up file")
	}

	// Make sure this peer is allowed to have this file
	allowed := b.getBroadcastScope(&f)
	found := false
	for _, addr := range allowed {
		if addr == peer {
			found = true
			break
		}
	}
	if !found {
		log.WithFields(log.Fields{
			"peer":    peer,
			"file_id": c.FileID,
			"hash":    cr.Hash,
		}).Warn("peer requested valid chunk they are not allowed to have")
		return nil
	}

	// Directly send the chunk to this peer
	b.sendDirect(peer, &c)

	return nil
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
	var targetChunk chunk
	err = b.database.Where("hash = ? AND downloaded = ?", hashString(hash), false).First(&targetChunk).Error
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

	// Update the data in the empty chunk TODO: if this file is going to be stored on disk, write it there
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
	// TODO: need to check every file that uses this chunk
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

func (b *bounce) distributeFile(data []byte, scope int, destination uuid.UUID) (uuid.UUID, error) { // TODO: different function for files on disk?
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
		// Create the chunk offer
		co := &chunkOffer{
			ID:          uuid.New(),
			Scope:       scope,
			Destination: destination,
			Author:      b.currentUserID(),
			Hash:        c.Hash,
			Location:    b.network.Address(),
			Timestamp:   time.Now().Unix(),
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

	return fileID, nil
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

/*
func (b *bounce) pruneChunks(fileID uuid.UUID) {
	var orphanedChunks []chunk
	err := b.database.
		Select("chunks.id").
		Joins("LEFT JOIN files ON chunks.file_id == files.id").
		Where("files.id IS NULL").
		Find(&orphanedChunks).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error finding orphaned chunks")
	}

	if len(orphanedChunks) > 0 {
		err = b.database.Delete(&orphanedChunks).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error deleting orphaned chunks")
		}
	}
}
*/

func splitChunks(data []byte) ([]chunk, string) {
	chunks := []chunk{}
	hashes := []string{}

	index := 0
	for {
		if len(data) < fileChunkSize {
			c := makeChunk(index, data)
			chunks = append(chunks, c)
			hashes = append(hashes, c.Hash)
			break
		}

		chunkData := data[:fileChunkSize]
		data = data[fileChunkSize:]

		c := makeChunk(index, chunkData)
		chunks = append(chunks, c)
		hashes = append(hashes, c.Hash)

		index += 1
	}

	return chunks, strings.Join(hashes, ",")
}

func makeChunk(index int, data []byte) chunk {
	hash := blake3.Sum256(data)
	chunkID, err := uuid.FromBytes(hash[:16])
	if err != nil {
		log.Fatal("cannot make uuid from bytes for chunk")
	}

	return chunk{
		ID:    chunkID,
		Hash:  hashString(hash),
		Index: index,
		Data:  data,
	}
}

func hashString(hash [32]byte) string {
	return hex.EncodeToString(hash[:])
}

func validImage(data []byte) bool {
	// TODO
	return true
}
