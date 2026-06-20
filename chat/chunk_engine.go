package chat

import (
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"gorm.io/gorm/clause"
)

const maxOutstandingChunkRequests = 5
const expectedKbps = 100
const expectedChunkDeliverySeconds = ((fileChunkSize / 1024) / expectedKbps) * maxOutstandingChunkRequests
const maxChunkAttempts = 3

type chunkEngine struct {
	sync.Mutex

	byLocation          map[string][]string
	byHash              map[string][]string
	toEncryptedHash     map[string]string
	preferToAvoid       map[string][]string             // From hash to list of peers we've already tried unsuccessfully
	lastRequestTime     map[string]map[string]time.Time // From hash to location to time of last sent chunkRequest
	requestCount        map[string]map[string]int       // From hash to location to the number of requests we've sent
	outstandingRequests map[string][]string             // From location to list of hashes
}

func (b *Bounce) loadChunkEngine() {
	b.chunkEngine = &chunkEngine{
		byLocation:          make(map[string][]string),
		byHash:              make(map[string][]string),
		toEncryptedHash:     make(map[string]string),
		preferToAvoid:       make(map[string][]string),
		lastRequestTime:     make(map[string]map[string]time.Time),
		requestCount:        make(map[string]map[string]int),
		outstandingRequests: make(map[string][]string),
	}

	b.chunkEngine.Lock()
	var unfinishedFiles []file
	err := b.database.Preload(clause.Associations).Where("wanted = ? AND downloaded = ?", true, false).Find(&unfinishedFiles).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up unfinished files")
	}

	for _, f := range unfinishedFiles {
		for _, c := range f.Chunks {
			if !c.Downloaded {
				offers := []chunkOffer{}
				err = b.database.Where("hash = ?", c.Hash).Find(&offers).Error
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Fatal("database error looking up chunk offers")
				}
				for _, co := range offers {
					b.chunkEngine.byHash[c.Hash] = append(b.chunkEngine.byHash[c.Hash], co.Location)
					b.chunkEngine.byLocation[co.Location] = append(b.chunkEngine.byLocation[co.Location], co.Hash)
				}

				if c.EncryptedHash != "" {
					b.chunkEngine.toEncryptedHash[c.Hash] = c.EncryptedHash

					encryptedOffers := []encryptedChunkOffer{}
					err = b.database.Where("hash = ?", c.EncryptedHash).Find(&encryptedOffers).Error
					if err != nil {
						log.WithFields(log.Fields{
							"error": err.Error(),
						}).Fatal("database error looking up encrypted chunk offers")
					}
					for _, eco := range encryptedOffers {
						b.chunkEngine.byHash[c.Hash] = append(b.chunkEngine.byHash[c.Hash], eco.Location)
						b.chunkEngine.byHash[eco.Location] = append(b.chunkEngine.byLocation[eco.Location], c.Hash)
					}
				}
			}
		}
	}
	b.chunkEngine.Unlock()

	go func() {
		for range time.NewTicker(5 * time.Second).C {
			b.makeAnyChunkRequests()
		}
	}()
}

func (b *Bounce) makeAnyChunkRequests() {
	b.chunkEngine.Lock()
	defer b.chunkEngine.Unlock()

	// Keep all outstanding requests as outstanding, unless we've had multiple unsuccessful tries and there are other options,
	// in which case evict that outstanding request and disfavor that peer for that hash.
	updatedOutstandingRequests := map[string][]string{}
	for location, requests := range b.chunkEngine.outstandingRequests {
		if b.getRemoteDevice(location).connectedSockets.Load() <= 0 {
			continue
		}

		for _, hash := range requests {
			if b.chunkEngine.requestsMade(location, hash) > maxChunkAttempts {
				totalOptions, ok := b.chunkEngine.byHash[hash]
				if !ok {
					log.WithFields(log.Fields{
						"hash": hash,
					}).Error("chunk engine hash map has no options for chunk that is current outstanding")
					continue
				}
				if len(totalOptions) > 1 {
					b.chunkEngine.preferToAvoid[hash] = append(b.chunkEngine.preferToAvoid[hash], location)
				} else {
					updatedOutstandingRequests[location] = append(updatedOutstandingRequests[location], hash)
				}
			} else {
				updatedOutstandingRequests[location] = append(updatedOutstandingRequests[location], hash)
			}
		}
	}
	b.chunkEngine.outstandingRequests = updatedOutstandingRequests

	// For any hashes that we want that are not outstanding, find a peer to assign them to, prefering ones we haven't already
	// tried, if any are avilable
	unassignedHashes := b.chunkEngine.unassignedHashes()
	for _, hash := range unassignedHashes {
		options, ok := b.chunkEngine.byHash[hash]
		if !ok {
			log.WithFields(log.Fields{
				"hash": hash,
			}).Warn("no locations in map for hash")
			continue
		}

		if len(options) > 1 {
			filteredOptions := []string{}

			disfavored := b.chunkEngine.preferToAvoid[hash]
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

			if b.getRemoteDevice(option).connectedSockets.Load() <= 0 {
				continue
			}

			encryptedDeviceCacheMutex.Lock()
			_, encrypted := encryptedDeviceCache[option]
			encryptedDeviceCacheMutex.Unlock()
			if encrypted {
				encryptedHash, ok := b.chunkEngine.toEncryptedHash[hash]
				if !ok {
					log.WithFields(log.Fields{
						"hash":     hash,
						"location": option,
					}).Warn("cannot get encrypted hash for hash stored on encrypted device")
					continue
				}
				hash = encryptedHash
			}

			b.chunkEngine.updateLastRequestTime(option, hash)
			b.chunkEngine.setRequestCount(option, hash, 1)

			b.chunkEngine.outstandingRequests[option] = append(b.chunkEngine.outstandingRequests[option], hash)
			log.WithFields(log.Fields{
				"hash":     hash,
				"location": option,
			}).Debug("requesting a chunk")
			go b.sendDirect(option, &chunkRequest{Hash: hash})
			break
		}
	}

	// Re-issue any requests that have not been downloaded by an expected time
	for location, requests := range b.chunkEngine.outstandingRequests {
		if b.getRemoteDevice(location).connectedSockets.Load() <= 0 {
			continue
		}
		for _, hash := range requests {
			if time.Since(b.chunkEngine.getLastRequestTime(location, hash)) > expectedChunkDeliverySeconds*time.Second {
				encryptedDeviceCacheMutex.Lock()
				_, encrypted := encryptedDeviceCache[location]
				encryptedDeviceCacheMutex.Unlock()
				if encrypted {
					encryptedHash, ok := b.chunkEngine.toEncryptedHash[hash]
					if !ok {
						log.WithFields(log.Fields{
							"hash":     hash,
							"location": location,
						}).Warn("cannot get encrypted hash for hash stored on encrypted device")
						continue
					}
					hash = encryptedHash
				}

				b.chunkEngine.updateLastRequestTime(location, hash)
				b.chunkEngine.setRequestCount(location, hash, b.chunkEngine.requestsMade(location, hash)+1)
				log.WithFields(log.Fields{
					"hash":     hash,
					"location": location,
				}).Warn("re-requesting a chunk")
				go b.sendDirect(location, &chunkRequest{Hash: hash})
			}
		}
	}
}

func (ce *chunkEngine) setRequestCount(location, hash string, count int) {
	locationCounts, ok := ce.requestCount[hash]
	if !ok {
		ce.requestCount[hash] = map[string]int{
			location: count,
		}
	} else {
		locationCounts[location] = count
	}
}

func (ce *chunkEngine) requestsMade(location, hash string) int {
	count := 0
	locationCounts, ok := ce.requestCount[hash]
	if !ok {
		ce.requestCount[hash] = make(map[string]int)
	} else {
		count, _ = locationCounts[location]
	}
	return count
}

func (ce *chunkEngine) updateLastRequestTime(location, hash string) {
	locationTimes, ok := ce.lastRequestTime[hash]
	if !ok {
		ce.lastRequestTime[hash] = map[string]time.Time{
			location: time.Now(),
		}
	} else {
		locationTimes[location] = time.Now()
	}
}

func (ce *chunkEngine) getLastRequestTime(location, hash string) time.Time {
	t := time.Time{}

	locationTimes, ok := ce.lastRequestTime[hash]
	if !ok {
		ce.lastRequestTime[hash] = make(map[string]time.Time)
	} else {
		t, _ = locationTimes[location]
	}
	return t
}

func (ce *chunkEngine) unassignedHashes() []string {
	unassigned := []string{}

	outstandingHashes := map[string]bool{}
	for _, outstanding := range ce.outstandingRequests {
		for _, hash := range outstanding {
			outstandingHashes[hash] = true
		}
	}

	for hash, _ := range ce.byHash {
		if _, outstanding := outstandingHashes[hash]; !outstanding {
			unassigned = append(unassigned, hash)
		}
	}

	return unassigned
}

func (b *Bounce) addChunkOfferToChunkEngine(co *chunkOffer) {
	b.chunkEngine.Lock()
	defer b.chunkEngine.Unlock()

	var c chunk
	err := b.database.Where("hash = ?", co.Hash).Take(&c).Error
	if err != nil {
		log.WithFields(log.Fields{
			"hash": co.Hash,
		}).Warn("cannot add chunk offer to chunk engine for chunk we don't yet have")
		return
	}

	b.chunkEngine.byHash[c.Hash] = append(b.chunkEngine.byHash[c.Hash], co.Location)
	b.chunkEngine.byLocation[co.Location] = append(b.chunkEngine.byHash[c.Hash], co.Hash)
}

func (b *Bounce) addEncryptedChunkOfferToChunkEngine(eco *encryptedChunkOffer) {
	b.chunkEngine.Lock()
	defer b.chunkEngine.Unlock()

	var c chunk
	err := b.database.Where("encrypted_hash = ?", eco.Hash).Take(&c).Error
	if err != nil {
		log.WithFields(log.Fields{
			"hash": eco.Hash,
		}).Warn("cannot add encrypted chunk offer to chunk engine for chunk we don't yet have")
		return
	}

	b.chunkEngine.byHash[c.Hash] = append(b.chunkEngine.byHash[c.Hash], eco.Location)
	b.chunkEngine.byHash[eco.Location] = append(b.chunkEngine.byHash[c.Hash], c.Hash)
}

func (ce *chunkEngine) completed(target string) {
	ce.Lock()
	defer ce.Unlock()

	prunedLocations := make(map[string][]string)
	for location, hashes := range ce.byLocation {
		hashesWithoutTarget := []string{}
		for _, hash := range hashes {
			if hash != target {
				hashesWithoutTarget = append(hashesWithoutTarget, hash)
			}
		}
		prunedLocations[location] = hashesWithoutTarget
	}
	ce.byLocation = prunedLocations

	delete(ce.byHash, target)
	delete(ce.toEncryptedHash, target)
	delete(ce.preferToAvoid, target)
	delete(ce.lastRequestTime, target)
	delete(ce.requestCount, target)

	prunedOutstandingRequests := make(map[string][]string)
	for location, requests := range prunedOutstandingRequests {
		requestsWithoutTarget := []string{}
		for _, hash := range requests {
			if hash != target {
				requestsWithoutTarget = append(requestsWithoutTarget, hash)
			}
		}
		prunedOutstandingRequests[location] = requestsWithoutTarget
	}
	ce.outstandingRequests = prunedOutstandingRequests
}

func (ce *chunkEngine) remove(targetLocation, targetHash string) {
	ce.Lock()
	defer ce.Unlock()

	currentLocationsWithHash, _ := ce.byHash[targetHash]
	hashLocationsWithoutTargetLocation := []string{}
	for _, l := range currentLocationsWithHash {
		if l != targetLocation {
			hashLocationsWithoutTargetLocation = append(hashLocationsWithoutTargetLocation, l)
		}
	}
	ce.byHash[targetHash] = hashLocationsWithoutTargetLocation

	currentHashesAtLocation, _ := ce.byLocation[targetLocation]
	hashesAtLocationWithoutTargetHash := []string{}
	for _, h := range currentHashesAtLocation {
		if h != targetHash {
			hashesAtLocationWithoutTargetHash = append(hashesAtLocationWithoutTargetHash, h)
		}
	}
	ce.byLocation[targetLocation] = hashesAtLocationWithoutTargetHash

	currentOutstandingRequests, _ := ce.outstandingRequests[targetLocation]
	outstandingRequestsWithoutTargetHash := []string{}
	for _, h := range currentOutstandingRequests {
		if h != targetHash {
			outstandingRequestsWithoutTargetHash = append(outstandingRequestsWithoutTargetHash, h)
		}
	}
	ce.outstandingRequests[targetLocation] = outstandingRequestsWithoutTargetHash
}
