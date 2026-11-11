package ui

import (
	"testing"

	"github.com/alecthomas/assert/v2"
	"github.com/bounce-chat/bounce/chat"
)

// bareChatHistory is a chat history with no widgets behind it, enough to exercise how items and their measured
// heights are kept in step
func bareChatHistory() *chatHistory {
	return &chatHistory{
		messages: &messageStore{
			messages:                 map[chat.ID]threadable{},
			messagesByAuthor:         map[chat.ID]map[chat.ID]threadable{},
			messagesByFileAttachment: map[chat.ID]threadable{},
			cache:                    map[chat.ID]cachedData{},
		},
		added:               map[chat.ID]bool{},
		seenTracking:        map[chat.ID]bool{},
		myID:                me,
		unreadCountCallback: func(int) {},
		windowFocused:       func() bool { return false },
	}
}

var me = chat.NewID()
var alice = chat.NewID()

func bubble(author chat.ID, timestamp int64, seen bool) *chatBubbleData {
	return &chatBubbleData{id: chat.NewID(), frameType: chat.TypeDirectMessage, author: author, writtenAt: timestamp, seen: seen}
}

func statusAt(timestamp int64) *statusChangeData {
	return &statusChangeData{id: chat.NewID(), timestamp: timestamp, seen: true}
}

func TestInsertingAnOlderItemKeepsMeasuredHeightsBesideTheirItems(t *testing.T) {
	ch := bareChatHistory()
	first, second, third := statusAt(10), statusAt(20), statusAt(30)
	for _, item := range []*statusChangeData{first, second, third} {
		ch.insertItem(item, true)
	}
	// Only the first two have been measured so far
	ch.SetItemHeight(0, 11)
	ch.SetItemHeight(1, 22)

	// A late arriving item from between them is placed by timestamp
	late := statusAt(15)
	ch.insertItem(late, false)
	assert.Equal(t, []chat.ID{first.id, late.id, second.id, third.id}, ch.ids)

	height, ok := ch.getItemHeight(0)
	assert.True(t, ok)
	assert.Equal(t, float32(11), height)
	_, ok = ch.getItemHeight(1)
	assert.False(t, ok, "the inserted item should not have inherited a neighbour's height")
	height, ok = ch.getItemHeight(2)
	assert.True(t, ok, "the second item's height was lost")
	assert.Equal(t, float32(22), height)
}

func TestInsertingBeforeEverythingShiftsHeights(t *testing.T) {
	ch := bareChatHistory()
	only := statusAt(20)
	ch.insertItem(only, true)
	ch.SetItemHeight(0, 42)

	ch.insertItem(statusAt(10), false)
	height, ok := ch.getItemHeight(1)
	assert.True(t, ok)
	assert.Equal(t, float32(42), height)
}

func TestDeletingAnUnreadMessageLowersTheUnreadCountAndForgetsIt(t *testing.T) {
	ch := bareChatHistory()
	first, second := bubble(alice, 10, false), bubble(alice, 20, false)
	ch.insertItem(first, true)
	ch.insertItem(second, true)
	ch.insertItem(bubble(me, 30, false), true)
	assert.Equal(t, 2, ch.unread, "only the other user's unseen messages count")

	assert.True(t, ch.deleteItem(first.id))
	assert.Equal(t, 1, ch.unread, "deleting an unread message should lower the count")
	assert.Equal(t, 2, len(ch.ids))

	// The same message arriving again is not a duplicate any more
	ch.insertItem(first, false)
	assert.Equal(t, 3, len(ch.ids), "a deleted message could not be inserted again")
	assert.Equal(t, 2, ch.unread)
}

func TestDeletingAMessageRecomputesTheMergesAroundIt(t *testing.T) {
	ch := bareChatHistory()
	// Each neighbour is within the merge window, the outer two are not
	first, middle, last := bubble(alice, 0, true), bubble(alice, 200, true), bubble(alice, 400, true)
	for _, b := range []*chatBubbleData{first, middle, last} {
		ch.insertItem(b, true)
	}
	assert.Equal(t, mergeModeTop, first.mergeMode)
	assert.Equal(t, mergeModeMiddle, middle.mergeMode)
	assert.Equal(t, mergeModeBottom, last.mergeMode)

	ch.deleteItem(middle.id)
	assert.Equal(t, mergeModeStandalone, first.mergeMode, "the bubble above still thinks it merges downward")
	assert.Equal(t, mergeModeStandalone, last.mergeMode, "the bubble below still thinks it merges upward")
}

func TestDeletingPastTheMeasuredHeightsDoesNotPanic(t *testing.T) {
	ch := bareChatHistory()
	first, second := bubble(alice, 10, true), bubble(alice, 20, true)
	ch.insertItem(first, true)
	ch.insertItem(second, true)
	ch.SetItemHeight(0, 10)

	assert.True(t, ch.deleteItem(second.id))
	assert.Equal(t, 1, len(ch.ids))
}

func TestScrollingPastAnAlreadySeenMessageDoesNotLowerTheUnreadCount(t *testing.T) {
	ch := bareChatHistory()
	unread, seenElsewhere := bubble(alice, 10, false), bubble(alice, 20, true)
	ch.insertItem(unread, true)
	ch.insertItem(seenElsewhere, true)
	assert.Equal(t, 1, ch.unread)

	ch.seen(1)
	assert.Equal(t, 1, ch.unread, "a message seen on another device was counted as read here")
	ch.seen(0)
	assert.Equal(t, 0, ch.unread)
}
