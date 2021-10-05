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
	customOnSubmitted func()
	selectKeyDown     bool
	selecting         bool
	selectRow         int
	selectColumn      int
	sizingLabel       *widget.Label
	maxHeightLabel    *widget.Label
}

func newThreadEntry(maxRows int) *threadEntry {
	entry := &threadEntry{}
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
	entry.sizingLabel.Text = entry.Text
	entry.sizingLabel.Resize(entry.Size())
	entry.sizingLabel.Refresh()

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
	if ev != nil && ev.Name == fyne.KeyReturn {
		if entry.selectKeyDown {
			entry.Refresh()
		} else {
			if entry.customOnSubmitted == nil {
				log.Fatal("user interface bug: a threadEntry does not have a customOnSubmitted defined")
			} else {
				if entry.Text != "" {
					entry.customOnSubmitted()
				}
			}
			return
		}
	}
	entry.Entry.TypedKey(ev)
}

//
// Just to get selectKeyDown
//

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
	e.Entry.FocusLost()
}
