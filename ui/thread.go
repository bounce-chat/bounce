package ui

import (
	"sort"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
	log "github.com/sirupsen/logrus"
)

type thread interface {
	getID() uuid.UUID
	getView() *fyne.Container
	getEntry() *threadEntry
	chatHistoryScroll() *container.Scroll
	getButton() *threadButton
	getLastMessageTime() int64
	setLastMessageTime(int64)
	getNotificationsMutedUntil() int64
}

//
// Threads should be sorted by the timestamp of the last message sent or received.
// This type allows for use of the sort.Sort function by implementing sort.Interface.
//

type sortableThreads []thread

func (threads sortableThreads) Len() int {
	return len(threads)
}
func (threads sortableThreads) Swap(i, j int) {
	threads[i], threads[j] = threads[j], threads[i]
}
func (threads sortableThreads) Less(i, j int) bool {
	// Reverse order, highest timestamp on top
	return threads[i].getLastMessageTime() > threads[j].getLastMessageTime()
}

//
// Helper functions for organizing and displaying threads in the UI
//

func (fyneUI *Fyne) refreshThreadOrder() {
	allThreads := sortableThreads{}
	for _, group := range fyneUI.groups {
		allThreads = append(allThreads, group)
	}
	for _, dm := range fyneUI.dms {
		allThreads = append(allThreads, dm)
	}
	sort.Sort(allThreads)

	buttons := []fyne.CanvasObject{}
	for _, thread := range allThreads {
		buttons = append(buttons, thread.getButton())
	}
	fyneUI.threadVBox.Objects = buttons
	fyneUI.threadVBox.Refresh()
}

func (fyneUI *Fyne) populateItems(t thread, items threadItems) {
	sort.Sort(items)

	chatHistory := t.chatHistoryScroll().Content.(*fyne.Container)
	for _, item := range items {
		chatHistory.Objects = append(chatHistory.Objects, item.widget)
	}

	lastItem := items[len(items)-1]
	if lastItem.setButton != nil {
		lastItem.setButton(t.getButton())
	} else {
		log.Warn("thread item doesn't support setting last button")
	}
	fyneUI.refreshThreadOrder()
}

func (fyneUI *Fyne) appendThreadItem(t thread, ti *threadItem) {
	autoscroll := false
	location := t.chatHistoryScroll().Offset.Y
	height := t.chatHistoryScroll().Content.Size().Height - t.chatHistoryScroll().Size().Height
	if height == location {
		autoscroll = true
	}

	// Insert statusChange thread items into their correct location in time, insert everything else
	// at the bottom regaurdless of timestamp
	chatHistory := t.chatHistoryScroll().Content.(*fyne.Container)
	//if _, ok := ti.widget.(*statusChange); ok {
	//	if len(chatHistory.Objects) == 0 {
	//		chatHistory.Objects = append(chatHistory.Objects, ti.widget)
	//	} else {
	//		for i := len(chatHistory.Objects) - 1; i >= 0; i-- {
	//			var compareTime int64
	//			compare := chatHistory.Objects[i]
	//			switch cast := compare.(type) {
	//			case *chatBubble:
	//				compareTime = cast.timestamp
	//			case *statusChange:
	//				compareTime = cast.timestamp
	//			default:
	//				log.Warn("cannot compare timestamp with unknown thread item type")
	//				continue
	//			}
	//			if ti.timestamp > compareTime {
	//				chatHistory.Objects = append(chatHistory.Objects[:i], append([]fyne.CanvasObject{ti.widget}, chatHistory.Objects[i:]...)...)
	//			}
	//		}
	//	}
	//} else {
	chatHistory.Objects = append(chatHistory.Objects, ti.widget)
	//}
	t.chatHistoryScroll().Refresh()

	fyneUI.threadWithItemMutex.Lock()
	fyneUI.threadWithItem[ti.id] = t
	fyneUI.threadWithItemMutex.Unlock()

	if ti.setButton != nil {
		ti.setButton(t.getButton())
	} else {
		log.Error("thread item does not support setting button")
	}

	if autoscroll && fyneUI.isActive(t) {
		t.chatHistoryScroll().ScrollToBottom()
		t.chatHistoryScroll().Refresh()
	}

	notificationsEnabled := t.getNotificationsMutedUntil() != chat.MutedForever
	notificationsMuted := time.Now().Unix() < t.getNotificationsMutedUntil()

	if ti.notification != nil && notificationsEnabled && !notificationsMuted && !autoscroll { //TODO: also notify if this is false but we're not focused?
		fyneUI.app.SendNotification(ti.notification)
	}

	t.setLastMessageTime(ti.timestamp)
	fyneUI.refreshThreadOrder()
}

func (fyneUI *Fyne) MarkMessageUndeliverable(id uuid.UUID) {
	log.WithFields(log.Fields{
		"message_id": id,
	}).Info("chat engine wants to mark a message as undeliverable")
	// TODO
}

func (fyneUI *Fyne) UpdateMessageDeletionTime(id uuid.UUID, timestamp int64) {
	log.WithFields(log.Fields{
		"message_id": id,
		"delete_at":  timestamp,
	}).Info("chat engine wants to update the delete time of a message")
	// TODO
}

//
// Silently drop a message from the chat history because it is past retention
//
func (fyneUI *Fyne) DeleteItem(id uuid.UUID) {
	fyneUI.threadWithItemMutex.Lock()
	thread, ok := fyneUI.threadWithItem[id]
	fyneUI.threadWithItemMutex.Unlock()
	if !ok {
		log.WithFields(log.Fields{
			"message_id": id,
		}).Warn("attempt to expire message that was not found in any thread")
		return
	}
	chatHistory := thread.chatHistoryScroll().Content.(*fyne.Container)

	found := false
	location := 0
	for i, obj := range chatHistory.Objects {
		switch cast := obj.(type) {
		case *chatBubble:
			if cast.id == id {
				found = true
				location = i
				break
			}
		case *statusChange:
			if cast.id == id {
				found = true
				location = i
				break
			}
		default:
			continue
		}
	}

	if found {
		// TODO: is there an error here if the message that is being deleted is the last message in the slice?
		// TODO: race condition here when mutating the slice directly?  mutex all changes to a thread's slice?
		chatHistory.Objects = append(chatHistory.Objects[:location], chatHistory.Objects[location+1:]...) // TODO: may be cheaper to use copy to shift slice
		thread.chatHistoryScroll().Refresh()
		fyneUI.threadWithItemMutex.Lock()
		delete(fyneUI.threadWithItem, id)
		fyneUI.threadWithItemMutex.Unlock()
	} else {
		log.WithFields(log.Fields{
			"message_id": id,
			"thread_id":  thread.getID(),
		}).Warn("attempt to delete thread item that was not in thread despite being in threadWithItem map")
	}
}

func (fyneUI *Fyne) displayThread(thread thread) {
	// TODO: for now, until we can actually track read status of each message
	thread.getButton().clearUnreadCount()

	fyneUI.activeThread = thread.getID()
	fyneUI.chatContainer.Objects = []fyne.CanvasObject{thread.getView()}
	fyneUI.chatContainer.Refresh()
	thread.getButton().Refresh()
	fyneUI.mainWindow.Canvas().Focus(thread.getEntry())
}

func (fyneUI *Fyne) isActive(thread thread) bool {
	return fyneUI.activeThread == thread.getID()
}

func (fyneUI *Fyne) getThread(id uuid.UUID) (thread, bool) {
	groupThread, ok := fyneUI.groups[id]
	if ok {
		return groupThread, true
	}

	dmThread, ok := fyneUI.dms[id]
	if ok {
		return dmThread, true
	}

	return nil, false
}

func (fyneUI *Fyne) ShowTypingIndicatorInHistory(userID, threadID uuid.UUID) {
	// TODO: show in the chat history that the user is typing
}

func (fyneUI *Fyne) ShowTypingIndicatorInButton(userID, threadID uuid.UUID) {
	thread, ok := fyneUI.getThread(threadID)
	if !ok {
		// Someone could be typing into a DM that isn't open on our side yet, this isn't always an error
		log.WithFields(log.Fields{
			"thread_id": threadID,
			"user_id":   userID,
		}).Debug("cannot indicate a user is typing in a button with unknown thread")
		return
	}

	u, ok := fyneUI.users.get(userID)
	if !ok {
		log.WithFields(log.Fields{
			"thread_id": threadID,
			"user_id":   userID,
		}).Error("cannot indicate a user is typing in a button with unknown user")
		return
	}

	name := u.name
	if u.id == fyneUI.profile.id {
		name = "You"
	}

	thread.getButton().startTyping(name)
}

func (fyneUI *Fyne) HideTypingIndicatorInHistory(userID, threadID uuid.UUID) {
	// TODO: remove the indicator that a user is typing from the chat history
}

func (fyneUI *Fyne) HideTypingIndicatorInButton(threadID uuid.UUID) {
	thread, ok := fyneUI.getThread(threadID)
	if !ok {
		// Someone could be typing into a DM that isn't open on our side yet, this isn't always an error
		log.WithFields(log.Fields{
			"thread_id": threadID,
		}).Debug("cannot clear typing indicator in a button with unknown thread")
		return
	}

	thread.getButton().stopTyping()
}
