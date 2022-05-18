package ui

import (
	"sort"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"github.com/google/uuid"
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

func (fyneUI *Fyne) displaySentMessage(thread thread, id uuid.UUID, message string) { // TODO: going to be able to get rid of this if using thread-specific message handlers for displaying outgoing and incoming?
	chatHistory := thread.chatHistoryScroll().Content.(*fyne.Container)

	thread.getButton().setLastMessage("You", message)
	thread.getButton().setLastMessageTime(time.Now())

	chatHistory.Objects = append(chatHistory.Objects, newChatBubble("You", id, message, true, time.Now().Unix(), nil))
	fyneUI.threadWithMessageMutex.Lock()
	fyneUI.threadWithMessage[id] = thread
	fyneUI.threadWithMessageMutex.Unlock()

	thread.chatHistoryScroll().Refresh()
	thread.chatHistoryScroll().ScrollToBottom()
	thread.chatHistoryScroll().Refresh() // TODO: needed, or does scrolling do a refresh?

	thread.setLastMessageTime(time.Now().Unix())
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
func (fyneUI *Fyne) DeleteMessage(id uuid.UUID) {
	fyneUI.threadWithMessageMutex.Lock()
	thread, ok := fyneUI.threadWithMessage[id]
	fyneUI.threadWithMessageMutex.Unlock()
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
		bubble, ok := obj.(*chatBubble)
		if !ok {
			continue
		}
		if bubble.id == id {
			found = true
			location = i
			break
		}
	}

	if found {
		chatHistory.Objects = append(chatHistory.Objects[:location], chatHistory.Objects[location+1:]...) // TODO: may be cheaper to use copy to shift slice
		thread.chatHistoryScroll().Refresh()
		fyneUI.threadWithMessageMutex.Lock()
		delete(fyneUI.threadWithMessage, id)
		fyneUI.threadWithMessageMutex.Unlock()
	} else {
		log.WithFields(log.Fields{
			"message_id": id,
			"thread_id":  thread.getID(),
		}).Warn("attempt to expire message that was not in thread despite being in threadWithMessage map")
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
		log.WithFields(log.Fields{
			"thread_id": threadID,
			"user_id":   userID,
		}).Error("cannot indicate a user is typing in a button with unknown thread")
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
		log.WithFields(log.Fields{
			"thread_id": threadID,
		}).Error("cannot clear typing indicator in a button with unknown thread")
		return
	}

	thread.getButton().stopTyping()
}
