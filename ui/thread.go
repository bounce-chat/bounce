package ui

import (
	"sort"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

type thread interface { // TODO: probably only need this for sorting threads
	getView() *fyne.Container
	getEntry() *threadEntry
	chatHistoryScroll() *container.Scroll
	getButton() *widget.Button
	getLastMessage() int64 // TODO: rename these better
	setLastMessage(int64)
	getID() string
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
	return threads[i].getLastMessage() > threads[j].getLastMessage()
}

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

func (fyneUI *Fyne) displaySentMessage(thread thread, message string) {
	chatHistory := thread.chatHistoryScroll().Content.(*fyne.Container)

	chatHistory.Objects = append(chatHistory.Objects, newChatBubble("You", message, true, time.Now().Unix(), nil))
	chatHistory.Refresh()

	thread.chatHistoryScroll().Refresh()
	thread.chatHistoryScroll().ScrollToBottom()
	thread.chatHistoryScroll().Refresh()

	thread.setLastMessage(time.Now().Unix())
	fyneUI.refreshThreadOrder()
}

func (fyneUI *Fyne) displayThread(thread thread) {
	fyneUI.activeThread = thread.getID()
	fyneUI.chatContainer.Objects = []fyne.CanvasObject{thread.getView()}
	fyneUI.chatContainer.Refresh()
	thread.getButton().Importance = widget.LowImportance
	thread.getButton().Refresh()
	fyneUI.mainWindow.Canvas().Focus(thread.getEntry())
}

func (fyneUI *Fyne) isActive(thread thread) bool {
	return fyneUI.activeThread == thread.getID()
}
