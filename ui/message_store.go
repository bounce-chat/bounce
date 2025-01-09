package ui

import (
	"sync"

	"github.com/google/uuid"
)

type cachedData struct {
	height    float32
	mergeMode int
}

type messageStore struct {
	sync.Mutex
	messages         map[uuid.UUID]threadable // TODO: store the thread items?  new widget with height?
	messagesByAuthor map[uuid.UUID]map[uuid.UUID]threadable
	//threadWithMessage map[uuid.UUID]*chatHistory
}

func newMessageStore() *messageStore {
	return &messageStore{
		messages:         make(map[uuid.UUID]threadable),
		messagesByAuthor: make(map[uuid.UUID]map[uuid.UUID]threadable),
	}
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
		case *statusChangeData:
			// TODO: anything to do here?
		}
	}
}
