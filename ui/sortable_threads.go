package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

type threadable interface { // TODO: probably only need this for sorting threads
	getView() *fyne.Container
	getEntry() *threadEntry
	chatHistoryScroll() *container.Scroll
	getButton() *widget.Button
	getLastMessage() int64 // TODO: rename these better
	setLastMessage(int64)
	getID() string
	getNotificationsEnabled() bool
	getNotificationsMutedUntil() int64
}

//
// Threads should be sorted by the timestamp of the last message sent or received.
// This type allows for use of the sort.Sort function by implementing sort.Interface.
//

type sortableThreads []threadable

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
