package ui

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
	log "github.com/sirupsen/logrus"
)

type threadEntry struct {
	widget.Entry
	hasAttachments    func() bool
	customOnSubmitted func()
	customFocusLost   func()
	selectKeyDown     bool
	selecting         bool
	selectRow         int
	selectColumn      int
	sizingLabel       *widget.Label
	maxHeightLabel    *widget.Label
}

func newThreadEntry(maxRows int, hasAttachments func() bool) *threadEntry {
	entry := &threadEntry{
		hasAttachments: hasAttachments,
	}
	entry.ExtendBaseWidget(entry)
	entry.MultiLine = true
	entry.Wrapping = fyne.TextWrapWord
	entry.sizingLabel = widget.NewLabel("")
	entry.sizingLabel.Wrapping = fyne.TextWrapWord
	entry.maxHeightLabel = widget.NewLabel("")
	var maxHeightString strings.Builder
	for i := 0; i < maxRows-1; i++ {
		maxHeightString.WriteString("\n")
	}
	entry.maxHeightLabel.Text = maxHeightString.String()

	return entry
}

func (entry *threadEntry) MinSize() fyne.Size {
	entry.sizingLabel.SetText(entry.Text)
	entry.sizingLabel.Resize(entry.Size())

	height := float32(0)
	maxHeight := entry.maxHeightLabel.MinSize().Height
	desiredHeight := entry.sizingLabel.MinSize().Height
	if desiredHeight < maxHeight {
		height = desiredHeight
	} else {
		height = maxHeight
	}
	width := entry.sizingLabel.MinSize().Width

	return fyne.Size{Height: height, Width: width}
}

func (entry *threadEntry) TypedKey(ev *fyne.KeyEvent) {
	if ev == nil {
		return
	}

	if fyne.CurrentDevice().IsMobile() {
		entry.Entry.TypedKey(ev)
		entry.Refresh()
		return
	}

	if ev.Name != fyne.KeyReturn {
		entry.Entry.TypedKey(ev)
		entry.Refresh()
		return
	}

	if entry.selectKeyDown {
		entry.Entry.TypedKey(ev)
		entry.Refresh()
	} else {
		if entry.customOnSubmitted == nil {
			log.Fatal("user interface bug: a threadEntry does not have a customOnSubmitted defined")
		}

		if strings.TrimSpace(entry.Text) != "" || entry.hasAttachments() {
			entry.customOnSubmitted()
		}
	}
}

func (e *threadEntry) KeyDown(key *fyne.KeyEvent) {
	if e.Disabled() {
		return
	}
	if key.Name == desktop.KeyShiftLeft || key.Name == desktop.KeyShiftRight {
		if !e.selecting {
			e.selectRow = e.CursorRow
			e.selectColumn = e.CursorColumn
		}
		e.selectKeyDown = true
	}
	e.Entry.KeyDown(key)
}

func (e *threadEntry) KeyUp(key *fyne.KeyEvent) {
	if e.Disabled() {
		return
	}
	if key.Name == desktop.KeyShiftLeft || key.Name == desktop.KeyShiftRight {
		e.selectKeyDown = false
	}
	e.Entry.KeyUp(key)
}

func (e *threadEntry) FocusLost() {
	e.selectKeyDown = false
	if e.customFocusLost != nil {
		e.customFocusLost()
	}
	e.Entry.FocusLost()
}
