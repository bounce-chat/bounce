package ui

import (
	"fyne.io/fyne/v2"
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

type customTapEntry struct {
	widget.Entry
	customTapped func(*fyne.PointEvent)
}

func newCustomTapEntry(customTap func(pe *fyne.PointEvent)) *customTapEntry {
	e := &customTapEntry{customTapped: customTap}
	e.MultiLine = true
	e.Wrapping = fyne.TextWrap(fyne.TextTruncateClip)

	e.ExtendBaseWidget(e)
	return e
}

func (e *customTapEntry) Tapped(pe *fyne.PointEvent) {
	if e.customTapped != nil {
		e.customTapped(pe)
	}

	e.Entry.Tapped(pe)
}
