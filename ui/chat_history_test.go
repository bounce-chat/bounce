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
		unreadCountCallback: func(int) {},
	}
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
