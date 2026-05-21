package ui

import (
	"fyne.io/fyne/v2/widget"
)

type autohideEntry struct {
	widget.Entry
	hideAction func()
}

func newAutohideEntry(hideAction func()) *autohideEntry {
	e := &autohideEntry{hideAction: hideAction}
	e.ExtendBaseWidget(e)

	return e
}

func (e *autohideEntry) FocusLost() {
	e.Text = ""
	e.Hide()
	e.hideAction()
}
