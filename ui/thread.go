package ui

import (
	"sort"
	"strconv"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
	log "github.com/sirupsen/logrus"
)

var messages = map[uuid.UUID]threadable{} // TODO: put on fyne, not package level?
var messagesMutex sync.Mutex

type thread interface {
	getID() uuid.UUID
	getView() *fyne.Container
	getEntry() *threadEntry
	chatHistoryScroll() *chatHistory
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

func (fyneUI *Fyne) populateInitialItems(t thread, items threadItems) {
	sort.Sort(items)

	fyneUI.threadWithItemMutex.Lock()
	widgetData := []threadable{}
	for _, item := range items {
		fyneUI.threadWithItem[item.id] = t
		widgetData = append(widgetData, item.widgetData)
	}
	t.chatHistoryScroll().setItems(widgetData)

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

	t.chatHistoryScroll().setItemHeights(fyneUI.chatContainerSizeAtStartup())
	t.chatHistoryScroll().scrollToLastRead()
	t.chatHistoryScroll().updateUnreadCounter()
}

func (fyneUI *Fyne) appendThreadItem(t thread, ti *threadItem) {
	// Determine if we are already scrolled down to the bottom of this thread before appending
	autoscroll := false
	location := t.chatHistoryScroll().GetScrollOffset()
	height := t.chatHistoryScroll().contentHeight() - t.chatHistoryScroll().Size().Height
	if height == location {
		autoscroll = true
	}

	// Determine if the thread item we are adding will be the latest item in the thread
	appendingToEnd := ti.timestamp > t.chatHistoryScroll().headTimestamp()

	// Add this thread item to the chat history
	t.chatHistoryScroll().insertItem(ti, appendingToEnd)

	// Keep track of which threads have which items
	fyneUI.threadWithItemMutex.Lock()
	fyneUI.threadWithItem[ti.id] = t
	fyneUI.threadWithItemMutex.Unlock()

	// Update the thread button if this is the latest message
	if ti.setButton != nil && ti.timestamp > t.getLastMessageTime() { // TODO: same as appendingToEnd?
		ti.setButton(t.getButton())
	}

	// Keep the thread scrolled down, if it is open and was already scrolled down
	if autoscroll && fyneUI.isActive(t) && appendingToEnd && fyneUI.focused {
		t.chatHistoryScroll().ScrollToBottom()
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
	messagesMutex.Lock()
	item, ok := messages[id]
	messagesMutex.Unlock()
	if !ok {
		log.WithFields(log.Fields{
			"id": id,
		}).Warn("item not found during attempt to mark item as undeliverable")
	}

	item.setState(stateError)

	fyneUI.threadWithItemMutex.Lock()
	thread, ok := fyneUI.threadWithItem[id]
	fyneUI.threadWithItemMutex.Unlock()
	if !ok {
		log.WithFields(log.Fields{
			"message_id": id,
		}).Warn("attempt to mark message as undeliverable that was not found in any thread")
		return
	}

	thread.chatHistoryScroll().Refresh()
}

func (fyneUI *Fyne) MessageSeen(id uuid.UUID) {
	messagesMutex.Lock()
	item, ok := messages[id]
	messagesMutex.Unlock()
	if !ok {
		log.WithFields(log.Fields{
			"id": id,
		}).Warn("item not found during attempt to mark item as seen")
	}

	item.markSeen()
}

func (fyneUI *Fyne) MessageDelivered(messageID, userID uuid.UUID) {
	messagesMutex.Lock()
	item, ok := messages[messageID]
	messagesMutex.Unlock()
	if !ok {
		log.WithFields(log.Fields{
			"id": messageID,
		}).Warn("item not found during attempt to mark item as delivered")
	}

	fyneUI.threadWithItemMutex.Lock()
	t, ok := fyneUI.threadWithItem[messageID]
	fyneUI.threadWithItemMutex.Unlock()
	if !ok {
		log.WithFields(log.Fields{
			"message_id": messageID,
		}).Warn("attempt to mark message as delivered that was not found in any thread")
		return
	}

	currentState := item.getState()
	if currentState == stateRead || currentState == stateDelivered {
		return
	}
	if userID == fyneUI.profile.id {
		if currentState == statePending {
			item.setState(stateSynced)
			if item.getAuthor() == fyneUI.profile.id && t.chatHistoryScroll().isLastItem(messageID) {
				t.getButton().showLastMessageState(stateSynced)
			}
		}

	} else {
		item.setState(stateDelivered)
		if item.getAuthor() == fyneUI.profile.id && t.chatHistoryScroll().isLastItem(messageID) {
			t.getButton().showLastMessageState(stateDelivered)
		}
	}

	t.chatHistoryScroll().Refresh()
}

func (fyneUI *Fyne) ReceivedReadReceipt(rr chat.ReadReceipt) {
	if rr.Actor == fyneUI.profile.id {
		return
	}

	messagesMutex.Lock()
	item, ok := messages[rr.Target]
	messagesMutex.Unlock()
	if !ok {
		log.WithFields(log.Fields{
			"id": rr.Target,
		}).Warn("item not found during attempt to mark item as read")
	}

	fyneUI.threadWithItemMutex.Lock()
	t, ok := fyneUI.threadWithItem[rr.Target]
	fyneUI.threadWithItemMutex.Unlock()
	if !ok {
		log.WithFields(log.Fields{
			"message_id": rr.Target,
		}).Warn("attempt to mark message as read that was not found in any thread")
		return
	}

	item.setState(stateRead)
	if item.getAuthor() == fyneUI.profile.id && t.chatHistoryScroll().isLastItem(rr.Target) {
		t.getButton().showLastMessageState(stateRead)
	}

	t.chatHistoryScroll().Refresh()
}

func (fyneUI *Fyne) DeleteItem(id uuid.UUID) {
	messagesMutex.Lock()
	delete(messages, id)
	messagesMutex.Unlock()

	fyneUI.threadWithItemMutex.Lock()
	thread, ok := fyneUI.threadWithItem[id]
	fyneUI.threadWithItemMutex.Unlock()
	if !ok {
		log.WithFields(log.Fields{
			"message_id": id,
		}).Warn("attempt to expire message that was not found in any thread")
		return
	}

	deleted := thread.chatHistoryScroll().deleteItem(id)

	if deleted {
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
	fyneUI.activeThread = thread.getID()
	fyneUI.chatContainer.Objects = []fyne.CanvasObject{thread.getView()}
	fyneUI.chatContainer.Refresh()

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
	t, ok := fyneUI.getThread(threadID)
	if !ok {
		// Someone could be typing into a DM that isn't open on our side yet, this isn't always an error
		log.WithFields(log.Fields{
			"thread_id": threadID,
		}).Debug("cannot clear typing indicator in a button with unknown thread")
		return
	}

	if t.chatHistoryScroll().isLastAuthor(fyneUI.profile.id) {
		t.getButton().showCurrentStatusIcon()
	}

	t.getButton().stopTyping()
}

func timestampString(timestamp int64) string {
	now := time.Now()
	diff := now.Unix() - timestamp
	if diff >= 0 && diff < 60 {
		return "Now"
	}

	timestampTime := time.Unix(timestamp, 0)
	timestring := ""
	if timestampTime.Year() != now.Year() {
		timestring = strconv.Itoa(timestampTime.Year())
	}
	if timestampTime.Month() != now.Month() {
		if len(timestring) != 0 {
			timestring = timestring + " "
		}
		timestring = timestring + timestampTime.Format("Jan 2")
	} else if timestampTime.Day() != now.Day() {
		if timestampTime.Before(now.Add(time.Duration(-24 * time.Hour))) {
			if len(timestring) != 0 {
				timestring = timestring + " "
			}
			timestring = timestring + timestampTime.Weekday().String()[0:3]
			if timestampTime.Before(now.Add(time.Duration(-7 * 24 * time.Hour))) {
				timestring = timestring + " " + timestampTime.Format("2")
			}
		}
	}

	if len(timestring) != 0 {
		timestring = timestring + " "
	}
	timestring = timestring + timestampTime.Format(time.Kitchen)

	return timestring
}
