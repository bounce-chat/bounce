package chat

import (
	"errors"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const maxOutstandingChunkRequests = 5
const expectedKbps = 100
const expectedChunkDeliverySeconds = ((fileChunkSize / 1024) / expectedKbps) * maxOutstandingChunkRequests
const maxChunkAttempts = 3

type chunkKey struct {
	fileID ID
	hash   string
}

type chunkEngine struct {
	sync.Mutex

	byLocation          map[string][]chunkKey
	byKey               map[chunkKey][]string
	toEncryptedHash     map[chunkKey]string
	preferToAvoid       map[chunkKey][]string             // From chunk key to list of peers we've already tried unsuccessfully
	lastRequestTime     map[chunkKey]map[string]time.Time // From chunk key to location to time of last sent chunkRequest
	requestCount        map[chunkKey]map[string]int       // From chunk key to location to the number of requests we've sent
	outstandingRequests map[string][]chunkKey             // From location to list of chunk keys
}

func (b *Bounce) loadChunkEngine() {
	b.chunkEngine = &chunkEngine{
		byLocation:          make(map[string][]chunkKey),
		byKey:               make(map[chunkKey][]string),
		toEncryptedHash:     make(map[chunkKey]string),
		preferToAvoid:       make(map[chunkKey][]string),
		lastRequestTime:     make(map[chunkKey]map[string]time.Time),
		requestCount:        make(map[chunkKey]map[string]int),
		outstandingRequests: make(map[string][]chunkKey),
	}

	b.chunkEngine.Lock()
	var unfinishedFiles []file
	err := b.database.Preload(clause.Associations).Where("downloaded = ? AND wanted = ?", false, true).Find(&unfinishedFiles).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up unfinished files")
	}

	for _, f := range unfinishedFiles {
		for _, c := range f.Chunks {
			if !c.Downloaded {
				key := chunkKey{fileID: f.ID, hash: c.Hash}
				offers := []chunkOffer{}
				err = b.database.Where("file_id = ? AND hash = ? AND location != ?", f.ID, c.Hash, b.network.Address()).Find(&offers).Error
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Fatal("database error looking up chunk offers")
				}
				for _, co := range offers {
					b.chunkEngine.byKey[key] = append(b.chunkEngine.byKey[key], co.Location)
					b.chunkEngine.byLocation[co.Location] = append(b.chunkEngine.byLocation[co.Location], key)
				}

				if c.EncryptedHash != "" {
					b.chunkEngine.toEncryptedHash[key] = c.EncryptedHash

					encryptedOffers := []encryptedChunkOffer{}
					err = b.database.Where("hash = ? AND location != ?", c.EncryptedHash, b.network.Address()).Find(&encryptedOffers).Error
					if err != nil {
						log.WithFields(log.Fields{
							"error": err.Error(),
						}).Fatal("database error looking up encrypted chunk offers")
					}
					for _, eco := range encryptedOffers {
						b.chunkEngine.byKey[key] = append(b.chunkEngine.byKey[key], eco.Location)
						b.chunkEngine.byLocation[eco.Location] = append(b.chunkEngine.byLocation[eco.Location], key)
					}
				}
			}
		}
	}
	b.chunkEngine.Unlock()

	b.background(func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-b.done:
				return
			case <-ticker.C:
				b.makeAnyChunkRequests()
			}
		}
	})
}

func (b *Bounce) makeAnyChunkRequests() {
	b.chunkEngine.Lock()
	defer b.chunkEngine.Unlock()

	// Keep all outstanding requests as outstanding, unless we've had multiple unsuccessful tries and there are other options,
	// in which case evict that outstanding request and disfavor that peer for that chunk.
	updatedOutstandingRequests := map[string][]chunkKey{}
	for location, requests := range b.chunkEngine.outstandingRequests {
		if b.getRemoteDevice(location).connectedSockets() <= 0 {
			continue
		}

		for _, key := range requests {
			if b.chunkEngine.requestsMade(location, key) > maxChunkAttempts {
				totalOptions, ok := b.chunkEngine.byKey[key]
				if !ok {
					log.WithFields(log.Fields{
						"file_id": key.fileID,
						"hash":    key.hash,
					}).Error("chunk engine map has no options for chunk that is currently outstanding")
					continue
				}
				if len(totalOptions) > 1 {
					b.chunkEngine.preferToAvoid[key] = append(b.chunkEngine.preferToAvoid[key], location)
				} else {
					updatedOutstandingRequests[location] = append(updatedOutstandingRequests[location], key)
				}
			} else {
				updatedOutstandingRequests[location] = append(updatedOutstandingRequests[location], key)
			}
		}
	}
	b.chunkEngine.outstandingRequests = updatedOutstandingRequests

	// For any chunks that we want that are not outstanding, find a peer to assign them to, preferring ones we haven't already
	// tried, if any are available
	unassignedKeys := b.chunkEngine.unassignedKeys()
	for _, key := range unassignedKeys {
		if !b.chunkWanted(key) {
			b.chunkEngine.removeKey(key)
			continue
		}

		options, ok := b.chunkEngine.byKey[key]
		if !ok {
			log.WithFields(log.Fields{
				"file_id": key.fileID,
				"hash":    key.hash,
			}).Warn("no locations in map for chunk")
			continue
		}

		if len(options) > 1 {
			filteredOptions := []string{}

			disfavored := b.chunkEngine.preferToAvoid[key]
			disfavoredMap := map[string]bool{}
			for _, peer := range disfavored {
				disfavoredMap[peer] = true
			}
			for _, option := range options {
				if _, ok := disfavoredMap[option]; !ok {
					filteredOptions = append(filteredOptions, option)
				}
			}

			if len(filteredOptions) > 1 {
				options = filteredOptions
			}
		}

		for _, option := range options {
			if len(b.chunkEngine.outstandingRequests[option]) > maxOutstandingChunkRequests {
				continue
			}

			if b.getRemoteDevice(option).connectedSockets() <= 0 {
				continue
			}

			encryptedDeviceCacheMutex.Lock()
			_, encrypted := encryptedDeviceCache[option]
			encryptedDeviceCacheMutex.Unlock()
			req := &chunkRequest{FileID: key.fileID, Hash: key.hash}
			if encrypted {
				encryptedHash, ok := b.chunkEngine.toEncryptedHash[key]
				if !ok {
					log.WithFields(log.Fields{
						"file_id":  key.fileID,
						"hash":     key.hash,
						"location": option,
					}).Warn("cannot get encrypted hash for hash stored on encrypted device")
					continue
				}
				req = &chunkRequest{Hash: encryptedHash}
			}

			b.chunkEngine.updateLastRequestTime(option, key)
			b.chunkEngine.setRequestCount(option, key, 1)

			b.chunkEngine.outstandingRequests[option] = append(b.chunkEngine.outstandingRequests[option], key)
			log.WithFields(log.Fields{
				"file_id":  key.fileID,
				"hash":     key.hash,
				"location": option,
			}).Debug("requesting a chunk")
			go b.sendDirect(option, req)
			break
		}
	}

	// Re-issue any requests that have not been downloaded by an expected time
	for location, requests := range b.chunkEngine.outstandingRequests {
		if b.getRemoteDevice(location).connectedSockets() <= 0 {
			continue
		}
		for _, key := range requests {
			if !b.chunkWanted(key) {
				b.chunkEngine.removeKey(key)
				continue
			}

			if time.Since(b.chunkEngine.getLastRequestTime(location, key)) > expectedChunkDeliverySeconds*time.Second {
				encryptedDeviceCacheMutex.Lock()
				_, encrypted := encryptedDeviceCache[location]
				encryptedDeviceCacheMutex.Unlock()
				req := &chunkRequest{FileID: key.fileID, Hash: key.hash}
				if encrypted {
					encryptedHash, ok := b.chunkEngine.toEncryptedHash[key]
					if !ok {
						log.WithFields(log.Fields{
							"file_id":  key.fileID,
							"hash":     key.hash,
							"location": location,
						}).Warn("cannot get encrypted hash for hash stored on encrypted device")
						continue
					}
					req = &chunkRequest{Hash: encryptedHash}
				}

				b.chunkEngine.updateLastRequestTime(location, key)
				b.chunkEngine.setRequestCount(location, key, b.chunkEngine.requestsMade(location, key)+1)
				log.WithFields(log.Fields{
					"file_id":  key.fileID,
					"hash":     key.hash,
					"location": location,
				}).Warn("re-requesting a chunk")
				go b.sendDirect(location, req)
			}
		}
	}
}

func (ce *chunkEngine) setRequestCount(location string, key chunkKey, count int) {
	locationCounts, ok := ce.requestCount[key]
	if !ok {
		ce.requestCount[key] = map[string]int{
			location: count,
		}
	} else {
		locationCounts[location] = count
	}
}

func (ce *chunkEngine) requestsMade(location string, key chunkKey) int {
	count := 0
	locationCounts, ok := ce.requestCount[key]
	if !ok {
		ce.requestCount[key] = make(map[string]int)
	} else {
		count, _ = locationCounts[location]
	}
	return count
}

func (ce *chunkEngine) updateLastRequestTime(location string, key chunkKey) {
	locationTimes, ok := ce.lastRequestTime[key]
	if !ok {
		ce.lastRequestTime[key] = map[string]time.Time{
			location: time.Now(),
		}
	} else {
		locationTimes[location] = time.Now()
	}
}

func (ce *chunkEngine) getLastRequestTime(location string, key chunkKey) time.Time {
	t := time.Time{}

	locationTimes, ok := ce.lastRequestTime[key]
	if !ok {
		ce.lastRequestTime[key] = make(map[string]time.Time)
	} else {
		t, _ = locationTimes[location]
	}
	return t
}

func (ce *chunkEngine) unassignedKeys() []chunkKey {
	unassigned := []chunkKey{}

	outstandingKeys := map[chunkKey]bool{}
	for _, outstanding := range ce.outstandingRequests {
		for _, key := range outstanding {
			outstandingKeys[key] = true
		}
	}

	for key := range ce.byKey {
		if _, outstanding := outstandingKeys[key]; !outstanding {
			unassigned = append(unassigned, key)
		}
	}

	return unassigned
}

func (b *Bounce) addChunkOfferToChunkEngine(co *chunkOffer) {
	key := chunkKey{fileID: co.FileID, hash: co.Hash}
	if co.Location == b.network.Address() || !b.chunkWanted(key) {
		return
	}

	b.chunkEngine.Lock()
	defer b.chunkEngine.Unlock()

	alreadyInKey := false
	for _, l := range b.chunkEngine.byKey[key] {
		if l == co.Location {
			alreadyInKey = true
		}
	}
	if !alreadyInKey {
		b.chunkEngine.byKey[key] = append(b.chunkEngine.byKey[key], co.Location)
	}

	alreadyInLocation := false
	for _, k := range b.chunkEngine.byLocation[co.Location] {
		if k == key {
			alreadyInLocation = true
		}
	}
	if !alreadyInLocation {
		b.chunkEngine.byLocation[co.Location] = append(b.chunkEngine.byLocation[co.Location], key)
	}
}

func (b *Bounce) addEncryptedChunkOfferToChunkEngine(eco *encryptedChunkOffer) {
	if eco.Location == b.network.Address() {
		return
	}

	var c chunk
	err := b.database.Where("encrypted_hash = ?", eco.Hash).Take(&c).Error
	if err != nil {
		log.WithFields(log.Fields{
			"hash": eco.Hash,
		}).Warn("cannot add encrypted chunk offer to chunk engine for chunk we don't yet have")
		return
	}
	key := chunkKey{fileID: c.FileID, hash: c.Hash}
	if !b.chunkWanted(key) {
		return
	}

	b.chunkEngine.Lock()
	defer b.chunkEngine.Unlock()

	b.chunkEngine.toEncryptedHash[key] = c.EncryptedHash

	alreadyInKey := false
	for _, l := range b.chunkEngine.byKey[key] {
		if l == eco.Location {
			alreadyInKey = true
		}
	}
	if !alreadyInKey {
		b.chunkEngine.byKey[key] = append(b.chunkEngine.byKey[key], eco.Location)
	}

	alreadyInLocation := false
	for _, k := range b.chunkEngine.byLocation[eco.Location] {
		if k == key {
			alreadyInLocation = true
		}
	}
	if !alreadyInLocation {
		b.chunkEngine.byLocation[eco.Location] = append(b.chunkEngine.byLocation[eco.Location], key)
	}
}

func (ce *chunkEngine) completed(target chunkKey) {
	ce.Lock()
	defer ce.Unlock()
	ce.removeKey(target)
}

func (ce *chunkEngine) removeKey(target chunkKey) {
	prunedLocations := make(map[string][]chunkKey)
	for location, keys := range ce.byLocation {
		keysWithoutTarget := []chunkKey{}
		for _, key := range keys {
			if key != target {
				keysWithoutTarget = append(keysWithoutTarget, key)
			}
		}
		prunedLocations[location] = keysWithoutTarget
	}
	ce.byLocation = prunedLocations

	delete(ce.byKey, target)
	delete(ce.toEncryptedHash, target)
	delete(ce.preferToAvoid, target)
	delete(ce.lastRequestTime, target)
	delete(ce.requestCount, target)

	prunedOutstandingRequests := make(map[string][]chunkKey)
	for location, requests := range ce.outstandingRequests {
		requestsWithoutTarget := []chunkKey{}
		for _, key := range requests {
			if key != target {
				requestsWithoutTarget = append(requestsWithoutTarget, key)
			}
		}
		prunedOutstandingRequests[location] = requestsWithoutTarget
	}

	ce.outstandingRequests = prunedOutstandingRequests
}

func (ce *chunkEngine) remove(targetLocation string, targetKey chunkKey) {
	ce.Lock()
	defer ce.Unlock()

	currentLocationsWithKey := ce.byKey[targetKey]
	locationsWithoutTarget := []string{}
	for _, l := range currentLocationsWithKey {
		if l != targetLocation {
			locationsWithoutTarget = append(locationsWithoutTarget, l)
		}
	}
	ce.byKey[targetKey] = locationsWithoutTarget

	currentKeysAtLocation := ce.byLocation[targetLocation]
	keysWithoutTarget := []chunkKey{}
	for _, k := range currentKeysAtLocation {
		if k != targetKey {
			keysWithoutTarget = append(keysWithoutTarget, k)
		}
	}
	ce.byLocation[targetLocation] = keysWithoutTarget

	currentOutstandingRequests := ce.outstandingRequests[targetLocation]
	outstandingWithoutTarget := []chunkKey{}
	for _, k := range currentOutstandingRequests {
		if k != targetKey {
			outstandingWithoutTarget = append(outstandingWithoutTarget, k)
		}
	}
	ce.outstandingRequests[targetLocation] = outstandingWithoutTarget
}

func (b *Bounce) chunkWanted(key chunkKey) bool {
	var f file
	err := b.database.Select("wanted", "downloaded").Where("id = ?", key.fileID).Take(&f).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false
		}
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up file")
	}
	if !f.Wanted || f.Downloaded {
		return false
	}

	var count int64
	err = b.database.Model(&chunk{}).Where("file_id = ? AND hash = ? AND downloaded = ?", key.fileID, key.hash, false).Count(&count).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up chunks")
	}
	return count > 0
}
