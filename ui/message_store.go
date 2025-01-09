package ui

import (
	"sync"

	"github.com/google/uuid"
)

type messageStore struct {
	sync.Mutex
	messages map[uuid.UUID]threadable // TODO: store the thread items?  new widget with height?
	//messagesByAuthor  map[uuid.UUID][]threadable
	//threadWithMessage map[uuid.UUID]*chatHistory
}

func newMessageStore() *messageStore {
	return &messageStore{
		messages: make(map[uuid.UUID]threadable),
	}
}

func (ms *messageStore) insert(t threadable) {
	ms.Lock()
	defer ms.Unlock()

	ms.messages[t.getID()] = t
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

	delete(ms.messages, id)
}

//func (fyneUI *Fyne) renameUser()

func (ms *messageStore) renameUser(userID uuid.UUID, name, initials string) {
	ms.Lock()
	defer ms.Unlock()

	//messages, ok := ms.messagesByAuthor[userID]
	//if !ok {
	//	return
	//}

	for _, m := range ms.messages {
		// TODO: if it's a chatBubbleData, set the name and initials
		switch item := m.(type) {
		case *chatBubbleData:
			item.username = name
			item.initials = initials
		case *statusChangeData:
			// TODO: anything to do here?
		}
	}
}
