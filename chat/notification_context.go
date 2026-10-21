package chat

import (
	"sync"
	"sync/atomic"

	log "github.com/sirupsen/logrus"
)

var uiIsInForeground = atomic.Bool{}

var activeThread ID
var notificationContextMutex sync.Mutex

var scrolledDown = make(map[ID]bool)

var notificationIcons = map[string][]byte{}
var notificationIconsMutex sync.Mutex

func (b *Bounce) SetForeground(value bool) {
	uiIsInForeground.Store(value)
}

func (b *Bounce) SetActiveThread(threadID ID) {
	notificationContextMutex.Lock()
	activeThread = threadID
	notificationContextMutex.Unlock()
}

func (b *Bounce) SetScrolledDown(id ID, value bool) {
	notificationContextMutex.Lock()
	defer notificationContextMutex.Unlock()

	scrolledDown[id] = value
}

func autoscrolling(threadID ID) bool {
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
