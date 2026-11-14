package ui

import (
	"math"
	"sort"
	"time"

	"fyne.io/fyne/v2"
	"github.com/bounce-chat/bounce/chat"
	log "github.com/sirupsen/logrus"
)

type thread interface {
	getID() chat.ID
	getName() string
	getView() *fyne.Container
	getEntry() *threadEntry
	chatHistoryScroll() *chatHistory
	getButtonData() *threadButtonData
	getTypingIndicator() *typingIndicator
	getLastMessageTime() int64
	setLastMessageTime(int64)
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
		if lastItem.setButtonData == nil {
			continue
		} else {
			lastItem.setButtonData(t.getButtonData())
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
	appendingToEnd := ti.timestamp >= t.chatHistoryScroll().headTimestamp()

	// Add this thread item to the chat history
	t.chatHistoryScroll().insertItem(ti.widgetData, appendingToEnd)

	// Keep track of which threads have which items
	ui.threads.associate(t, ti.id)

	// Update the thread button if this is the latest message
	if ti.setButtonData != nil && ti.timestamp > t.getLastMessageTime() { // TODO: same as appendingToEnd?
		ti.setButtonData(t.getButtonData())
	}

	// Keep the thread scrolled down, if it is open and was already scrolled down
	if autoscroll && ui.isActive(t) && appendingToEnd {
		t.chatHistoryScroll().ScrollToBottom()
	} else {
		t.chatHistoryScroll().displayJumpToBottomIfNeeded()
	}

	// Update the latest time of this thread and update the thread order
	if !ti.dontBumpThread && appendingToEnd {
		t.setLastMessageTime(ti.timestamp)
		ui.containers.threads.Refresh()
	}
}

// A user has synced up with us after coming online and delivered the following messages
// and read receipts in bulk.  Load them all in without needing to refresh or notify, then
// update the UI all at once and send one notification per thread.
func (ui *ui) CatchUpMessages(bu chat.BulkUpdate, initialSync bool) {
	seen := map[chat.ID]bool{}
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

		items := threadItems{}
		allSeen := true
		for _, gm := range gms {
			ti, err := ui.newGroupMessage(gm)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error creating thread item for group message")
				continue
			}
			if !ui.prepareCaughtUpItem(ti, gm.ID, gm.Author, bu, seen) {
				allSeen = false
			}
			items = append(items, ti)
		}
		ui.showCaughtUpItems(g, items, allSeen, initialSync)
	}

	for userID, dms := range bu.DirectMessages {
		t, ok := ui.threads.getDM(userID)
		if !ok {
			log.WithFields(log.Fields{
				"user_id": userID,
			}).Debug("open direct message thread not found during bulk update")
			continue
		}

		items := threadItems{}
		allSeen := true
		for _, dm := range dms {
			ti, err := ui.newDirectMessage(dm)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error creating thread item for direct message")
				continue
			}
			if !ui.prepareCaughtUpItem(ti, dm.ID, dm.Author, bu, seen) {
				allSeen = false
			}
			items = append(items, ti)
		}
		ui.showCaughtUpItems(t, items, allSeen, initialSync)
	}

	for _, rrs := range bu.ReadReceipts {
		for _, rr := range rrs {
			ui.ReceivedReadReceipt(rr)
		}
	}

	fyne.Do(func() { ui.containers.threads.Refresh() })
}

// prepareCaughtUpItem marks a caught up item as seen when the sync said it was, and as read when a read receipt for
// it arrived in the same catch up, consuming that receipt.  It reports whether the item was seen.
func (ui *ui) prepareCaughtUpItem(ti *threadItem, id, author chat.ID, bu chat.BulkUpdate, seen map[chat.ID]bool) bool {
	if seen[id] {
		ti.widgetData.markSeen()
	}
	for _, rr := range bu.ReadReceipts[id] {
		if rr.Actor == ui.state.profile.id && author == ui.state.profile.id {
			continue
		}
		ti.widgetData.setState(stateRead)
		delete(bu.ReadReceipts, id)
		break
	}
	return seen[id]
}

// showCaughtUpItems places a thread's caught up items into its history in timestamp order, in one pass on the UI
// thread.  A catch up arrives in the order the other device saved its frames, which is not the order they were
// written when an older message reached it late, so each item is placed against the head the one before it left,
// and the thread button shows the latest message rather than the last one received.  Each item is claimed by its
// thread only once it is in the message store, so a lookup by thread never finds an item the store does not have.
func (ui *ui) showCaughtUpItems(t thread, items threadItems, allSeen, initialSync bool) {
	sort.Stable(items)
	fyne.DoAndWait(func() {
		var latest *threadItem
		for _, ti := range items {
			// An item written in the same second as the head goes after it, keeping the order they were sorted in
			appendingToEnd := ti.timestamp >= t.chatHistoryScroll().headTimestamp()
			t.chatHistoryScroll().insertItem(ti.widgetData, appendingToEnd)
			ui.threads.associate(t, ti.id)
			if ti.setButtonData != nil {
				latest = ti
			}
		}

		if latest != nil && (initialSync || latest.timestamp > t.getLastMessageTime()) {
			t.setLastMessageTime(latest.timestamp)
			latest.setButtonData(t.getButtonData())
			if latest.widgetData.getAuthor() == ui.state.profile.id {
				t.getButtonData().setShowLastMessageState(latest.widgetData.getState())
			}
		}

		if !allSeen {
			t.chatHistoryScroll().scrollToLastRead()
			t.chatHistoryScroll().displayJumpToBottomIfNeeded()
		}
		t.chatHistoryScroll().Refresh()
	})
}

func (ui *ui) MarkMessageUndeliverable(id chat.ID) {
	item, ok := ui.messages.get(id)
	if !ok {
		log.WithFields(log.Fields{
			"id": id,
		}).Warn("item not found during attempt to mark item as undeliverable")
		return
	}

	item.setState(stateError)

	t, ok := ui.threads.withItem(id)
	if !ok {
		log.WithFields(log.Fields{
			"message_id": id,
		}).Warn("attempt to mark message as undeliverable that was not found in any thread")
		return
	}

	fyne.DoAndWait(func() {
		t.chatHistoryScroll().Refresh()
		ui.containers.threads.Refresh()
	})
}

func (ui *ui) MessageSeen(id chat.ID) {
	item, ok := ui.messages.get(id)
	if !ok {
		log.WithFields(log.Fields{
			"id": id,
		}).Warn("item not found during attempt to mark item as seen")
		return
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
		if item.countsAsUnread() && item.getAuthor() != ui.state.profile.id {
			fyne.DoAndWait(func() {
				if t.chatHistoryScroll().unread > 0 {
					t.chatHistoryScroll().unread -= 1
				}
				t.chatHistoryScroll().updateUnreadCounter()
				t.chatHistoryScroll().Refresh()
				ui.containers.threads.Refresh()
			})
		}
	}

	if !t.hasBeenOpened() {
		fyne.DoAndWait(func() { t.chatHistoryScroll().scrollToLastRead() })
	}

}

func (ui *ui) MessageDelivered(messageID, userID chat.ID) {
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
					t.getButtonData().setShowLastMessageState(stateSynced)
					t.chatHistoryScroll().Refresh()
					ui.containers.threads.Refresh()
				})
			}
		}
	} else {
		item.setState(stateDelivered)
		if item.getAuthor() == ui.state.profile.id && t.chatHistoryScroll().isLastItem(messageID) {
			fyne.DoAndWait(func() {
				t.getButtonData().setShowLastMessageState(stateDelivered)
				t.chatHistoryScroll().Refresh()
				ui.containers.threads.Refresh()
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
		fyne.DoAndWait(func() { t.getButtonData().setShowLastMessageState(stateRead) })
	}

	fyne.DoAndWait(func() {
		t.chatHistoryScroll().Refresh()
		ui.containers.threads.Refresh()
	})
}

func (ui *ui) DeleteItem(id chat.ID) {
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
	ui.bounce.SetActiveThread(t.getID())
	ui.containers.chat.Objects = []fyne.CanvasObject{t.getView()}
	ui.containers.chat.Refresh()

	if fyne.CurrentDevice().IsMobile() {
		ui.setContent(viewTypeThread, ui.containers.chat)
		ui.containers.chat.Show()
	} else {
		if !isInvite {
			ui.window.Canvas().Focus(t.getEntry())
		}
	}

	if !opened && !isInvite {
		t.chatHistoryScroll().scrollToLastRead()
		t.chatHistoryScroll().disableSeenTracking = false
		t.chatHistoryScroll().Refresh()
	}

	if !isInvite {
		t.getEntry().TypedKey(&fyne.KeyEvent{Name: fyne.KeyEnd})
	}
}

func (ui *ui) isActive(t thread) bool {
	return ui.state.activeThread == t.getID()
}

func (ui *ui) ShowTypingIndicator(userID, threadID chat.ID) {
	t, ok := ui.threads.get(threadID)
	if !ok {
		log.WithFields(log.Fields{
			"thread_id": threadID,
		}).Warn("thread not found showing typing indicators")
		return
	}
	if _, isInvite := t.(*invite); isInvite {
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

		t.getButtonData().showTypingUser(u)
		ui.containers.threads.Refresh()
	})
}

func (ui *ui) HideTypingIndicator(userID, threadID chat.ID) {
	t, ok := ui.threads.get(threadID)
	if !ok {
		log.WithFields(log.Fields{
			"thread_id": threadID,
		}).Warn("thread not found hiding typing indicators")
		return
	}
	if _, isInvite := t.(*invite); isInvite {
		return
	}

	fyne.DoAndWait(func() {
		t.getTypingIndicator().hideUser(userID)
		t.getView().Refresh()

		t.getButtonData().hideTypingUser(userID)
		ui.containers.threads.Refresh()
	})
}

func (ui *ui) UpdateDraft(d chat.Draft) {
	t, ok := ui.threads.get(d.Thread)
	if !ok {
		return
	}
	if _, isInvite := t.(*invite); isInvite {
		return
	}

	fyne.Do(func() {
		t.getEntry().Text = d.Text
		t.getEntry().Refresh()

		t.getButtonData().setDraft(d.Text)
	})
}

// timestampString renders a time relative to now, for message timestamps and for "muted until" alike: the clock
// alone on the same calendar day, the weekday within a week in either direction, the date beyond that, and the year
// only when it differs.  Days are compared as calendar days, so yesterday evening is never shown as a bare clock
// and a date a year ago is never mistaken for today.
func timestampString(timestamp int64, now time.Time) string {
	if diff := now.Unix() - timestamp; diff >= 0 && diff < 60 {
		return "Now"
	}

	t := time.Unix(timestamp, 0).In(now.Location())
	clock := t.Format(time.Kitchen)
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return clock
	}

	startOfDay := func(x time.Time) time.Time {
		return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, x.Location())
	}
	days := math.Abs(math.Round(startOfDay(now).Sub(startOfDay(t)).Hours() / 24))
	if days < 7 {
		return t.Weekday().String()[:3] + " " + clock
	}
	if t.Year() == now.Year() {
		return t.Format("Jan 2") + " " + clock
	}
	return t.Format("Jan 2 2006") + " " + clock
}
