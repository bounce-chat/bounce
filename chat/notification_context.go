package chat

import (
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

var uiIsInForeground = atomic.Bool{}

var activeThread uuid.UUID
var notificationContextMutex sync.Mutex

var scrolledDown = make(map[uuid.UUID]bool)

var notificationIcons = map[string][]byte{}
var notificationIconsMutex sync.Mutex

func (b *Bounce) SetForeground(value bool) {
	uiIsInForeground.Store(value)
}

func (b *Bounce) SetActiveThread(threadID uuid.UUID) {
	notificationContextMutex.Lock()
	activeThread = threadID
	notificationContextMutex.Unlock()
}

func (b *Bounce) SetScrolledDown(id uuid.UUID, value bool) {
	notificationContextMutex.Lock()
	defer notificationContextMutex.Unlock()

	scrolledDown[id] = value
}

func autoscrolling(threadID uuid.UUID) bool {
	notificationContextMutex.Lock()
	defer notificationContextMutex.Unlock()

	scrolled, ok := scrolledDown[threadID]
	if !ok {
		log.WithFields(log.Fields{
			"thread": threadID,
		}).Debug("thread does not have record of if it is scrolled all the way down")
	}

	return threadID == activeThread && scrolled
}

func (b *Bounce) SetNotificationIcon(threadID string, data []byte) {
	notificationIconsMutex.Lock()
	defer notificationIconsMutex.Unlock()

	notificationIcons[threadID] = data
}

func (b *Bounce) getNotificationIcon(threadID string) []byte {
	notificationIconsMutex.Lock()
	defer notificationIconsMutex.Unlock()

	data, _ := notificationIcons[threadID]
	return data
}
