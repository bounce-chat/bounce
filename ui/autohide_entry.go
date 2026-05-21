package ui

import (
	"fyne.io/fyne/v2/widget"
)

type autohideEntry struct {
	widget.Entry
	hideAction func()
	dontHide   func() bool
}

func newAutohideEntry(hideAction func(), dontHide func() bool) *autohideEntry {
	e := &autohideEntry{
		hideAction: hideAction,
		dontHide:   dontHide,
	}
	e.ExtendBaseWidget(e)

	return e
}

func (e *autohideEntry) FocusLost() {
	e.Entry.FocusLost()
	if !e.dontHide() {
		e.Text = ""
		e.Hide()
		e.hideAction()
	}
}
