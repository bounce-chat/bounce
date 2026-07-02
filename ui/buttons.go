package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

type keyboardPreservableButton struct {
	widget.Button
}

func newKeyboardPreservableButton(label string, icon fyne.Resource, tapped func()) *keyboardPreservableButton {
	kpb := &keyboardPreservableButton{}

	kpb.Text = label
	kpb.Icon = icon
	kpb.OnTapped = tapped

	kpb.ExtendBaseWidget(kpb)

	return kpb
}

func (kpb *keyboardPreservableButton) DoNotHideKeyboardWhenFocusing() bool {
	return true
}

func (kpb *keyboardPreservableButton) DoNotHideKeyboardWhenLosingFocus() bool {
	return true
}

type nohoverButton struct {
	widget.Button
	KeyboardPreservable bool
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

func (b *nohoverButton) DoNotHideKeyboardWhenFocusing() bool {
	return b.KeyboardPreservable
}

func (b *nohoverButton) DoNotHideKeyboardWhenLosingFocus() bool {
	return b.KeyboardPreservable
}
