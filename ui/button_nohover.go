package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

type nohoverButton struct {
	widget.Button
}

func newNoHoverButton(text string, clicked func(), icon fyne.Resource) *nohoverButton {
	b := &nohoverButton{}
	b.ExtendBaseWidget(b)

	b.Text = text
	b.OnTapped = clicked
	b.Icon = icon

	return b
}

func (b *nohoverButton) MouseIn(_ *fyne.PointEvent) {
}

func (b *nohoverButton) MouseOut(_ *fyne.PointEvent) {
}
