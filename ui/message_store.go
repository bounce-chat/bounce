package ui

import (
	"io/ioutil"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
)

type cachedData struct {
	Width     float32
	Height    float32
	Merges    bool
	MergeMode int
}

type messageStore struct {
	sync.Mutex
	messages         map[uuid.UUID]threadable
	messagesByAuthor map[uuid.UUID]map[uuid.UUID]threadable
	cache            map[uuid.UUID]cachedData
	cacheFile        string
}

func newMessageStore(configDirectory string) *messageStore {
	cache := map[uuid.UUID]cachedData{}
	cacheFile := configDirectory + "/messageStore.cache"
	data, err := ioutil.ReadFile(cacheFile)
	if err != nil {
		if !os.IsNotExist(err) {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error opening message store cache file")
		}
	} else {
		err := msgpack.Unmarshal(data, &cache)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("message store cache does not contain valid data")
		}
	}

	ms := &messageStore{
		messages:         make(map[uuid.UUID]threadable),
		messagesByAuthor: make(map[uuid.UUID]map[uuid.UUID]threadable),
		cache:            cache,
		cacheFile:        cacheFile,
	}

	go func() {
		for range time.NewTicker(10 * time.Minute).C {
			ms.writeCache()
		}
	}()

	return ms
}

func (ms *messageStore) insert(t threadable) {
	ms.Lock()
	defer ms.Unlock()

	// Add the the global index
	ms.messages[t.getID()] = t

	// Index by author
	_, ok := ms.messagesByAuthor[t.getAuthor()]
	if !ok {
		ms.messagesByAuthor[t.getAuthor()] = make(map[uuid.UUID]threadable)
	}
	ms.messagesByAuthor[t.getAuthor()][t.getID()] = t
}

func (ms *messageStore) get(id uuid.UUID) (threadable, bool) {
	ms.Lock()
	t, ok := ms.messages[id]
	ms.Unlock()

	return t, ok
}

func (ms *messageStore) remove(id uuid.UUID) {
	ms.Lock()
	defer ms.Unlock()

	m, ok := ms.messages[id]
	if !ok {
		return
	}

	// Delete from global index
	delete(ms.messages, id)

	// Delete from index by author
	delete(ms.messagesByAuthor[m.getAuthor()], id)
}

func (ms *messageStore) renameUser(userID uuid.UUID, name, initials string) {
	ms.Lock()
	defer ms.Unlock()

	messages, ok := ms.messagesByAuthor[userID]
	if !ok {
		return
	}

	for _, m := range messages {
		switch item := m.(type) {
		case *chatBubbleData:
			item.username = name
			item.initials = initials
		}
	}
}

func (ms *messageStore) queryCache(id uuid.UUID) (cachedData, bool) {
	ms.Lock()
	cd, ok := ms.cache[id]
	ms.Unlock()

	return cd, ok
}

func (ms *messageStore) cacheData(id uuid.UUID, cd cachedData) {
	ms.Lock()
	defer ms.Unlock()

	ms.cache[id] = cd
}

func (ms *messageStore) writeCache() {
	ms.Lock()
	defer ms.Unlock()

	for id, _ := range ms.cache {
		if _, ok := ms.messages[id]; !ok {
			delete(ms.cache, id)
		}
	}

	data, err := msgpack.Marshal(ms.cache)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshaling message store cache")
	} else {
		err = os.WriteFile(ms.cacheFile, data, 0600)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error writing message store cache")
		}
	}
}
