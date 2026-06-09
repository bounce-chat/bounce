package ui

import (
	"sort"
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
	log "github.com/sirupsen/logrus"
)

type thread interface {
	getID() uuid.UUID
	getName() string
	getView() *fyne.Container
	getEntry() *threadEntry
	chatHistoryScroll() *chatHistory
	getButton() *threadButton
	getTypingIndicator() *typingIndicator
	getLastMessageTime() int64
	setLastMessageTime(int64)
	getNotificationsMutedUntil() int64
	refreshReadReceiptSettingSelection([]string)
	refreshTypingIndicatorSettingSelection([]string)
	getEditIcon() *defaultImage
	getHeaderIcon() *defaultImage
	setOpened()
	hasBeenOpened() bool
	getLastOpenTime() int64
	hasDraft() bool
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
	iTime := threads[i].getLastMessageTime()
	jTime := threads[j].getLastMessageTime()

	if threads[i].hasDraft() && threads[i].getLastOpenTime() > iTime {
		iTime = threads[i].getLastOpenTime()
	}

	if threads[j].hasDraft() && threads[j].getLastOpenTime() > jTime {
		jTime = threads[j].getLastOpenTime()
	}

	// Reverse order, highest timestamp on top
	return iTime > jTime

}

//
// Helper functions for organizing and displaying threads in the UI
//

func (ui *ui) refreshThreadOrder() {
	buttons := []fyne.CanvasObject{}
	for _, t := range ui.threads.sorted() {
		buttons = append(buttons, t.getButton())
	}
	ui.containers.threads.Objects = buttons
	ui.containers.threads.Refresh()
}

func (ui *ui) populateInitialItems(t thread, items threadItems) {
	sort.Sort(items)

	widgetData := []threadable{}
	for _, item := range items {
		ui.threads.associate(t, item.id)
		widgetData = append(widgetData, item.widgetData)
	}
	t.chatHistoryScroll().setItems(widgetData, chatContainerSizeAtStartup())

	for i := len(items) - 1; i >= 0; i-- {
		lastItem := items[i]
		if lastItem.setButton == nil {
			continue
		} else {
			lastItem.setButton(t.getButton())
			break
		}
	}

	t.chatHistoryScroll().scrollToLastRead()
	t.chatHistoryScroll().updateUnreadCounter()
}

func (ui *ui) appendThreadItem(t thread, ti *threadItem) {
	// Determine if we are already scrolled down to the bottom of this thread before appending
	autoscroll := false
	location := t.chatHistoryScroll().GetScrollOffset()
	height := t.chatHistoryScroll().contentHeight() - t.chatHistoryScroll().Size().Height
	if height == location && ui.state.focused {
		autoscroll = true
	}

	// Determine if the thread item we are adding will be the latest item in the thread
	appendingToEnd := ti.timestamp > t.chatHistoryScroll().headTimestamp()

	// Add this thread item to the chat history
	t.chatHistoryScroll().insertItem(ti.widgetData, appendingToEnd)

	// Keep track of which threads have which items
	ui.threads.associate(t, ti.id)

	// Update the thread button if this is the latest message
	if ti.setButton != nil && ti.timestamp > t.getLastMessageTime() { // TODO: same as appendingToEnd?
		ti.setButton(t.getButton())
	}

	// Keep the thread scrolled down, if it is open and was already scrolled down
	if autoscroll && ui.isActive(t) && appendingToEnd {
		t.chatHistoryScroll().ScrollToBottom()
	} else {
		t.chatHistoryScroll().displayJumpToBottomIfNeeded()
	}

	// Send a notification if required
	notificationsEnabled := (t.getNotificationsMutedUntil() != chat.MutedForever) && !(t.getID() == ui.state.profile.id)
	notificationsMuted := time.Now().Unix() < t.getNotificationsMutedUntil()
	deferToAnotherDevice := !ui.state.active && ui.state.anotherDeviceActive
	if ti.notification != nil && notificationsEnabled && !notificationsMuted && !autoscroll && !ui.state.initialSyncIncomplete && !deferToAnotherDevice {
		ui.app.SendNotification(ti.notification)
	}

	// Update the latest time of this thread and update the thread order
	if !ti.dontBumpThread && appendingToEnd {
		t.setLastMessageTime(ti.timestamp)
		ui.refreshThreadOrder()
	}
}

// A user has synced up with us after coming online and delivered the following messages
// and read receipts in bulk.  Load them all in without needing to refresh or notify, then
// update the UI all at once and send one notification per thread.
func (ui *ui) CatchUpMessages(bu chat.BulkUpdate, initialSync bool) {
	seen := map[uuid.UUID]bool{}
	for _, seenID := range bu.Seen {
		seen[seenID] = true
	}

	for groupID, gms := range bu.GroupMessages {
		g, ok := ui.threads.getGroup(groupID)
		if !ok {
			log.WithFields(log.Fields{
				"group_id": groupID,
			}).Error("group not found during bulk update")
			continue
		}

		var lastNotifyingItem *threadItem
		var lastButtonItem *threadItem
		allSeen := true
		for _, gm := range gms {
			ti, err := ui.newGroupMessage(gm)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error creating thread item for group message")
				continue
			}

			if _, ok := seen[gm.ID]; ok {
				ti.widgetData.markSeen()
			} else {
				allSeen = false
			}

			if _, ok := bu.ReadReceipts[gm.ID]; ok {
				ti.widgetData.setState(stateRead)
				delete(bu.ReadReceipts, gm.ID)
			}

			appendingToEnd := ti.timestamp > g.chatHistoryScroll().headTimestamp()
			fyne.Do(func() { g.chatHistoryScroll().insertItem(ti.widgetData, appendingToEnd) })
			ui.threads.associate(g, ti.id)
			if ti.notification != nil {
				lastNotifyingItem = ti
			}
			if ti.setButton != nil {
				lastButtonItem = ti
			}
		}

		if lastButtonItem != nil && (initialSync || lastButtonItem.timestamp > g.getLastMessageTime()) {
			g.setLastMessageTime(lastButtonItem.timestamp)
			fyne.Do(func() { lastButtonItem.setButton(g.getButton()) })
			if lastButtonItem.widgetData.getAuthor() == ui.state.profile.id {
				fyne.Do(func() { g.getButton().showLastMessageState(lastButtonItem.widgetData.getState()) })
			}
		}

		if lastNotifyingItem != nil && !allSeen {
			notificationsEnabled := g.getNotificationsMutedUntil() != chat.MutedForever
			notificationsMuted := time.Now().Unix() < g.getNotificationsMutedUntil()
			deferToAnotherDevice := !ui.state.active && ui.state.anotherDeviceActive
			if notificationsEnabled && !notificationsMuted && !ui.state.initialSyncIncomplete && !deferToAnotherDevice {
				ui.app.SendNotification(lastNotifyingItem.notification)
			}
		}

		if !allSeen {
			fyne.Do(func() {
				g.chatHistoryScroll().scrollToLastRead()
				g.chatHistoryScroll().displayJumpToBottomIfNeeded()
			})
		}

		fyne.Do(func() { g.chatHistoryScroll().Refresh() })
	}

	for userID, dms := range bu.DirectMessages {
		t, ok := ui.threads.getDM(userID)
		if !ok {
			log.WithFields(log.Fields{
				"user_id": userID,
			}).Error("direct message thread not found during bulk update")
			continue
		}

		var lastNotifyingItem *threadItem
		var lastButtonItem *threadItem
		allSeen := true
		for _, dm := range dms {
			ti, err := ui.newDirectMessage(dm)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error creating thread item for direct message")
				continue
			}

			if _, ok := seen[dm.ID]; ok {
				ti.widgetData.markSeen()
			} else {
				allSeen = false
			}

			if _, ok := bu.ReadReceipts[dm.ID]; ok {
				ti.widgetData.setState(stateRead)
				delete(bu.ReadReceipts, dm.ID)
			}

			appendingToEnd := ti.timestamp > t.chatHistoryScroll().headTimestamp()
			fyne.Do(func() { t.chatHistoryScroll().insertItem(ti.widgetData, appendingToEnd) })
			ui.threads.associate(t, ti.id)
			if ti.notification != nil {
				lastNotifyingItem = ti
			}
			if ti.setButton != nil {
				lastButtonItem = ti
			}
		}

		if lastButtonItem != nil && (initialSync || lastButtonItem.timestamp > t.getLastMessageTime()) {
			t.setLastMessageTime(lastButtonItem.timestamp)
			fyne.Do(func() { lastButtonItem.setButton(t.getButton()) })
			if lastButtonItem.widgetData.getAuthor() == ui.state.profile.id {
				fyne.Do(func() { t.getButton().showLastMessageState(lastButtonItem.widgetData.getState()) })
			}
		}

		if lastNotifyingItem != nil && !allSeen {
			notificationsEnabled := t.getNotificationsMutedUntil() != chat.MutedForever
			notificationsMuted := time.Now().Unix() < t.getNotificationsMutedUntil()
			deferToAnotherDevice := !ui.state.active && ui.state.anotherDeviceActive
			if notificationsEnabled && !notificationsMuted && !ui.state.initialSyncIncomplete && !deferToAnotherDevice {
				ui.app.SendNotification(lastNotifyingItem.notification)
			}
		}

		if !allSeen {
			fyne.Do(func() {
				t.chatHistoryScroll().scrollToLastRead()
				t.chatHistoryScroll().displayJumpToBottomIfNeeded()
			})
		}

		fyne.Do(func() { t.chatHistoryScroll().Refresh() })
	}

	for _, rrs := range bu.ReadReceipts {
		for _, rr := range rrs {
			ui.ReceivedReadReceipt(rr)
		}
	}

	fyne.Do(func() { ui.refreshThreadOrder() })
}

func (ui *ui) MarkMessageUndeliverable(id uuid.UUID) {
	item, ok := ui.messages.get(id)
	if !ok {
		log.WithFields(log.Fields{
			"id": id,
		}).Warn("item not found during attempt to mark item as undeliverable")
	}

	item.setState(stateError)

	t, ok := ui.threads.withItem(id)
	if !ok {
		log.WithFields(log.Fields{
			"message_id": id,
		}).Warn("attempt to mark message as undeliverable that was not found in any thread")
		return
	}

	fyne.DoAndWait(func() { t.chatHistoryScroll().Refresh() })
}

func (ui *ui) MessageSeen(id uuid.UUID) {
	item, ok := ui.messages.get(id)
	if !ok {
		log.WithFields(log.Fields{
			"id": id,
		}).Warn("item not found during attempt to mark item as seen")
	}

	t, ok := ui.threads.withItem(id)
	if !ok {
		log.WithFields(log.Fields{
			"message_id": id,
		}).Warn("marked message as seen that was not found in any thread")
		return
	}

	if !item.isSeen() {
		item.markSeen()
		if item.countsAsUnread() {
			fyne.DoAndWait(func() {
				t.chatHistoryScroll().unread -= 1
				t.chatHistoryScroll().updateUnreadCounter()
				t.chatHistoryScroll().Refresh()
			})
		}
	}

	if !t.hasBeenOpened() {
		fyne.DoAndWait(func() { t.chatHistoryScroll().scrollToLastRead() })
	}

}

func (ui *ui) MessageDelivered(messageID, userID uuid.UUID) {
	item, ok := ui.messages.get(messageID)
	if !ok {
		log.WithFields(log.Fields{
			"id": messageID,
		}).Warn("item not found during attempt to mark item as delivered")
		return
	}

	t, ok := ui.threads.withItem(messageID)
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
	if userID == ui.state.profile.id {
		if currentState == statePending {
			item.setState(stateSynced)
			if item.getAuthor() == ui.state.profile.id && t.chatHistoryScroll().isLastItem(messageID) {
				fyne.DoAndWait(func() {
					t.getButton().showLastMessageState(stateSynced)
					t.chatHistoryScroll().Refresh()
				})
			}
		}
	} else {
		item.setState(stateDelivered)
		if item.getAuthor() == ui.state.profile.id && t.chatHistoryScroll().isLastItem(messageID) {
			fyne.DoAndWait(func() {
				t.getButton().showLastMessageState(stateDelivered)
				t.chatHistoryScroll().Refresh()
			})
		}
	}
}

func (ui *ui) ReceivedReadReceipt(rr chat.ReadReceipt) {
	if rr.Actor == ui.state.profile.id {
		return
	}

	item, ok := ui.messages.get(rr.Target)
	if !ok {
		log.WithFields(log.Fields{
			"id": rr.Target,
		}).Warn("item not found during attempt to mark item as read")
		return
	}

	t, ok := ui.threads.withItem(rr.Target)
	if !ok {
		log.WithFields(log.Fields{
			"message_id": rr.Target,
		}).Warn("attempt to mark message as read that was not found in any thread")
		return
	}

	item.setState(stateRead)

	if item.getAuthor() == ui.state.profile.id && t.chatHistoryScroll().isLastItem(rr.Target) {
		fyne.DoAndWait(func() { t.getButton().showLastMessageState(stateRead) })
	}

	fyne.DoAndWait(func() { t.chatHistoryScroll().Refresh() })
}

func (ui *ui) DeleteItem(id uuid.UUID) {
	t, ok := ui.threads.withItem(id)
	if !ok {
		log.WithFields(log.Fields{
			"message_id": id,
		}).Warn("attempt to expire message that was not found in any thread")
		return
	}

	fyne.DoAndWait(func() {
		deleted := t.chatHistoryScroll().deleteItem(id)

		if deleted {
			ui.threads.removeItem(id)
		} else {
			log.WithFields(log.Fields{
				"message_id": id,
				"thread_id":  t.getID(),
			}).Warn("attempt to delete thread item that was not in chat history widget but was in thread storemap")
		}
	})

	ui.messages.remove(id)
}

func (ui *ui) displayThread(t thread) {
	_, isInvite := t.(*invite)

	opened := t.hasBeenOpened()
	t.setOpened()

	// Don't mark things seen while we find where to scroll to the last read
	if !opened && !isInvite {
		t.chatHistoryScroll().disableSeenTracking = true
	}

	ui.state.activeThread = t.getID()
	ui.containers.chat.Objects = []fyne.CanvasObject{t.getView()}
	ui.containers.chat.Refresh()

	if fyne.CurrentDevice().IsMobile() {
		ui.window.SetContent(ui.containers.chat)
		ui.containers.chat.Show()
	} else {
		if !isInvite {
			ui.window.Canvas().Focus(t.getEntry())
		}
	}

	if !opened && !isInvite {
		t.chatHistoryScroll().scrollToLastRead()
		t.chatHistoryScroll().disableSeenTracking = false
	}

	t.getEntry().TypedKey(&fyne.KeyEvent{Name: fyne.KeyEnd})
}

func (ui *ui) isActive(t thread) bool {
	return ui.state.activeThread == t.getID()
}

func (ui *ui) ShowTypingIndicator(userID, threadID uuid.UUID) {
	t, ok := ui.threads.get(threadID)
	if !ok {
		log.WithFields(log.Fields{
			"thread_id": threadID,
		}).Warn("thread not found showing typing indicators")
		return
	}

	u, ok := ui.users.get(userID)
	if !ok {
		log.WithFields(log.Fields{
			"thread_id": threadID,
			"userID":    userID,
		}).Warn("user not found showing typing indicators")
		return
	}

	fyne.DoAndWait(func() {
		t.getTypingIndicator().showUser(u)
		t.getView().Refresh()

		t.getButton().typingIndicator.showUser(u)
		t.getButton().Refresh()
	})
}

func (ui *ui) HideTypingIndicator(userID, threadID uuid.UUID) {
	t, ok := ui.threads.get(threadID)
	if !ok {
		log.WithFields(log.Fields{
			"thread_id": threadID,
		}).Warn("thread not found hiding typing indicators")
		return
	}

	fyne.DoAndWait(func() {
		t.getTypingIndicator().hideUser(userID)
		t.getView().Refresh()

		button := t.getButton()
		button.typingIndicator.hideUser(userID)
		button.Refresh()
	})
}

func (ui *ui) UpdateDraft(d chat.Draft) {
	t, ok := ui.threads.get(d.Thread)
	if !ok {
		log.WithFields(log.Fields{
			"thread_id": d.Thread,
		}).Warn("attempt to set draft for unknown thread")
		return
	}

	fyne.Do(func() {
		t.getEntry().Text = d.Text
		t.getEntry().Refresh()

		t.getButton().setDraft(d.Text)
	})
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

func mutedTimestampString(timestamp int64) string {
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
		if len(timestring) != 0 {
			timestring = timestring + " "
		}
		timestring = timestring + timestampTime.Weekday().String()[0:3]
		timestring = timestring + " " + timestampTime.Format("2")
	}

	if len(timestring) != 0 {
		timestring = timestring + " "
	}
	timestring = timestring + timestampTime.Format(time.Kitchen)

	return timestring
}
