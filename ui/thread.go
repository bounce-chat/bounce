package ui

import (
	"sort"
	"time"

	"fyne.io/fyne/v2"
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
	log "github.com/sirupsen/logrus"
)

type thread interface {
	getID() uuid.UUID
	getView() *fyne.Container
	getEntry() *threadEntry
	chatHistoryScroll() *List
	getItems() []threadable
	setItems([]threadable)
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

	fyneUI.threadWithItemMutex.Lock()
	widgetData := []threadable{}
	for _, item := range items {
		fyneUI.threadWithItem[item.id] = t
		widgetData = append(widgetData, item.widgetData)
		//t.appendItemAtEnd(item.widgetData) // TODO: try to avoid needed interface implementations
	}
	t.setItems(widgetData)
	fyneUI.threadWithItemMutex.Unlock()

	for i := len(items) - 1; i >= 0; i-- {
		lastItem := items[i]
		if lastItem.setButton == nil {
			continue
		} else {
			lastItem.setButton(t.getButton())
			break
		}
	}
	fyneUI.refreshThreadOrder()
}

func (fyneUI *Fyne) appendThreadItem(t thread, ti *threadItem) {
	// Determine if we are already scrolled down to the bottom of this thread before appending
	autoscroll := false
	//location := t.chatHistoryScroll().Offset.Y
	location := t.chatHistoryScroll().GetScrollOffset()
	//height := t.chatHistoryScroll().Content.Size().Height - t.chatHistoryScroll().Size().Height
	height := t.chatHistoryScroll().contentHeight() - t.chatHistoryScroll().Size().Height
	if height == location {
		autoscroll = true
	}

	// Determine if the thread item we are adding will be the latest item in the thread
	threadItems := t.getItems()
	currentHead := int64(0)
	if len(threadItems) > 0 {
		compare := threadItems[len(threadItems)-1]
		switch cast := compare.(type) {
		case *chatBubbleData:
			currentHead = cast.writtenAt
		case *statusChangeData:
			currentHead = cast.timestamp
		default:
			log.Warn("cannot compare timestamp with unknown thread item type")
		}
	}
	appendingToEnd := ti.timestamp > currentHead

	// Insert statusChange thread items into their correct location in time, insert everything else
	// at the bottom regaurdless of timestamp
	if _, ok := ti.widgetData.(*statusChangeData); ok {
		if len(threadItems) == 0 || appendingToEnd {
			threadItems = append(threadItems, ti.widgetData)
		} else {
			for i := len(threadItems); i >= 0; i-- {
				if i == 0 {
					threadItems = append([]threadable{ti.widgetData}, threadItems...)
					break
				}
				var compareTime int64
				compare := threadItems[i-1]
				switch cast := compare.(type) {
				case *chatBubbleData:
					compareTime = cast.writtenAt
				case *statusChangeData:
					compareTime = cast.timestamp
				default:
					log.Warn("cannot compare timestamp with unknown thread item type")
					continue
				}
				if ti.timestamp > compareTime {
					threadItems = append(threadItems[:i], append([]threadable{ti.widgetData}, threadItems[i:]...)...)
					break
				}
			}
		}
	} else {
		threadItems = append(threadItems, ti.widgetData)
	}
	// TODO: set heights
	t.setItems(threadItems)

	// Keep track of which threads have which items
	fyneUI.threadWithItemMutex.Lock()
	fyneUI.threadWithItem[ti.id] = t
	fyneUI.threadWithItemMutex.Unlock()

	// Update the thread button if this is the latest message
	if ti.setButton != nil && ti.timestamp > t.getLastMessageTime() {
		ti.setButton(t.getButton())
	}

	// Keep the thread scrolled down, if it is open and was already scrolled down
	if autoscroll && fyneUI.isActive(t) && appendingToEnd {
		t.chatHistoryScroll().ScrollToBottom()
		t.chatHistoryScroll().Refresh()
	}

	// Send a notification if required
	notificationsEnabled := (t.getNotificationsMutedUntil() != chat.MutedForever) && !(t.getID() == fyneUI.profile.id)
	notificationsMuted := time.Now().Unix() < t.getNotificationsMutedUntil()
	if ti.notification != nil && notificationsEnabled && !notificationsMuted && !autoscroll && !fyneUI.initialSyncIncomplete { //TODO: also notify if this is false but we're not focused?
		fyneUI.app.SendNotification(ti.notification)
	}

	// Update the latest time of this thread and update the thread order
	if !ti.dontBumpThread && appendingToEnd {
		t.setLastMessageTime(ti.timestamp)
		fyneUI.refreshThreadOrder()
	}
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
	//chatHistory := thread.chatHistoryScroll().Content.(*fyne.Container)
	threadItems := thread.getItems()

	found := false
	location := 0
	for i, obj := range threadItems { //chatHistory.Objects {
		switch cast := obj.(type) {
		case *chatBubbleData:
			if cast.id == id {
				found = true
				location = i
				break
			}
		case *statusChangeData:
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
		thread.setItems(append(threadItems[:location], threadItems[location+1:]...))
		//chatHistory.Objects = append(chatHistory.Objects[:location], chatHistory.Objects[location+1:]...) // TODO: may be cheaper to use copy to shift slice

		// TODO: set heights

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

	if fyne.CurrentDevice().IsMobile() {
		fyneUI.mainWindow.SetContent(fyneUI.chatContainer)
		fyneUI.chatContainer.Show()
	} else {
		fyneUI.mainWindow.Canvas().Focus(thread.getEntry())
	}
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

	name := u.getName()
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
