package ui

import (
	"sync"

	"github.com/google/uuid"
)

type messageStore struct {
	sync.Mutex
	messages          map[uuid.UUID]threadable // TODO: store the thread items?
	messagesByAuthor  map[uuid.UUID][]threadable
	threadWithMessage map[uuid.UUID]*chatHistory
}

func (ms *messageStore) renameUser(userID uuid.UUID, name, initials string) {
	ms.Lock()
	defer ms.Unlock()

	//messages, ok := ms.messagesByAuthor[userID]
	//if !ok {
	//	return
	//}

	//for _, message := range messages {
	//	// TODO: if it's a chatBubbleData, set the name and initials
	//}
}
