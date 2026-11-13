package ui

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/alecthomas/assert/v2"
)

func TestTimestampsAreRelativeToTheCalendarDay(t *testing.T) {
	now := time.Date(2026, time.March, 10, 15, 4, 0, 0, time.UTC) // a fixed zone, so a day is always 24 hours here
	at := func(d time.Duration) int64 { return now.Add(d).Unix() }

	assert.Equal(t, "Now", timestampString(at(-30*time.Second), now))
	assert.Equal(t, "1:04PM", timestampString(at(-2*time.Hour), now), "earlier today is just the clock")
	assert.Equal(t, "Mon 4:04PM", timestampString(at(-23*time.Hour), now), "yesterday within 24 hours is still yesterday")
	assert.Equal(t, "Wed 3:04PM", timestampString(at(-6*24*time.Hour), now), "within a week the weekday is enough")
	assert.Equal(t, "Mar 2 3:04PM", timestampString(at(-8*24*time.Hour), now), "beyond a week the date")
	assert.Equal(t, "Mar 10 2025 3:04PM", timestampString(at(-365*24*time.Hour), now), "the same month and day a year ago is not today")

	// Muted-until times run the other way
	assert.Equal(t, "Wed 3:04PM", timestampString(at(24*time.Hour), now), "tomorrow is a weekday")
	assert.Equal(t, "Mar 20 3:04PM", timestampString(at(10*24*time.Hour), now))
	assert.Equal(t, "Jan 5 2027 3:04PM", timestampString(at(301*24*time.Hour), now))
}

func TestPreviewsAreCutBetweenCharacters(t *testing.T) {
	short := "héllo"
	assert.Equal(t, short, previewOf(short))

	long := strings.Repeat("é", 250)
	preview := previewOf(long)
	assert.True(t, utf8.ValidString(preview), "a preview was cut inside a character")
	assert.Equal(t, previewRunes, utf8.RuneCountInString(preview))

	tbd := &threadButtonData{}
	tbd.setLastMessage("Ann", long, false)
	assert.True(t, utf8.ValidString(tbd.lastMessageSegmentTwo))
	tbd.setDraft(long)
	assert.Equal(t, previewRunes, utf8.RuneCountInString(tbd.draft))
}
