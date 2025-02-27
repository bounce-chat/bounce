package ui

import (
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
)

var maxTypingUsers = 8
var dotSize = float32(8)

type typingIndicator struct {
	widget.BaseWidget

	icons           []*defaultImage
	typingAnimation *fyne.Animation
	firstTypingDot  *canvas.Circle
	secondTypingDot *canvas.Circle
	thirdTypingDot  *canvas.Circle
}

func newTypingIndicator() *typingIndicator {
	ti := &typingIndicator{
		icons:           []*defaultImage{},
		firstTypingDot:  canvas.NewCircle(color.RGBA{R: 0x96, G: 0x96, B: 0x96, A: 0xff}),
		secondTypingDot: canvas.NewCircle(color.RGBA{R: 0x96, G: 0x96, B: 0x96, A: 0xff}),
		thirdTypingDot:  canvas.NewCircle(color.RGBA{R: 0x96, G: 0x96, B: 0x96, A: 0xff}),
	}

	ti.typingAnimation = &fyne.Animation{
		AutoReverse: false,
		Curve:       fyne.AnimationLinear,
		Duration:    1800 * time.Millisecond,
		RepeatCount: fyne.AnimationRepeatForever,
		Tick:        ti.updateTypingAnimation,
	}

	ti.ExtendBaseWidget(ti)

	return ti
}

func (ti *typingIndicator) updateTypingAnimation(state float32) {
	dull := color.RGBA{R: 0x96, G: 0x96, B: 0x96, A: 0xff}
	bright := color.RGBA{R: 0xe3, G: 0xe3, B: 0xe3, A: 0xff}

	if state < 0.333 {
		ti.firstTypingDot.FillColor = bright
		ti.secondTypingDot.FillColor = dull
		ti.thirdTypingDot.FillColor = dull
		ti.firstTypingDot.Refresh()
		ti.secondTypingDot.Refresh()
		ti.thirdTypingDot.Refresh()
	} else if state < 0.666 {
		ti.firstTypingDot.FillColor = dull
		ti.secondTypingDot.FillColor = bright
		ti.thirdTypingDot.FillColor = dull
		ti.firstTypingDot.Refresh()
		ti.secondTypingDot.Refresh()
		ti.thirdTypingDot.Refresh()
	} else {
		ti.firstTypingDot.FillColor = dull
		ti.secondTypingDot.FillColor = dull
		ti.thirdTypingDot.FillColor = bright
		ti.firstTypingDot.Refresh()
		ti.secondTypingDot.Refresh()
		ti.thirdTypingDot.Refresh()
	}
}

func (ti *typingIndicator) showUser(u *user) {
	for _, di := range ti.icons {
		if di.id == u.id {
			return
		}
	}

	ti.icons = append(
		ti.icons,
		newDefaultImage(u.id, u.initials, theme.IconInlineSize(), nil),
	)

	if len(ti.icons) == 1 {
		ti.typingAnimation.Start()
		ti.Show()
	}
}

func (ti *typingIndicator) hideUser(userID uuid.UUID) {
	iconsWithoutUser := []*defaultImage{}

	for _, di := range ti.icons {
		if di.id != userID {
			iconsWithoutUser = append(iconsWithoutUser, di)
		}
	}

	ti.icons = iconsWithoutUser

	if len(ti.icons) == 0 {
		ti.typingAnimation.Stop()
		ti.Hide()
	}
}

func (ti *typingIndicator) CreateRenderer() fyne.WidgetRenderer {
	ti.ExtendBaseWidget(ti)

	tir := &typingIndicatorRenderer{
		ti: ti,
	}

	return tir
}

type typingIndicatorRenderer struct {
	ti *typingIndicator
}

func (tir *typingIndicatorRenderer) Destroy() {}

func (tir *typingIndicatorRenderer) Layout(size fyne.Size) {
	animationLeft := float32(0)
	for i, icon := range tir.ti.icons {
		if i+1 >= maxTypingUsers {
			icon.Hide()
			continue
		} else {
			icon.Show()
		}

		icon.Resize(fyne.Size{
			Width:  theme.IconInlineSize(),
			Height: theme.IconInlineSize(),
		})

		left := float32(0)
		if i > 0 {
			left += theme.IconInlineSize() * float32(i)
			left -= theme.IconInlineSize() / 2
		}

		icon.Move(fyne.Position{
			X: left,
			Y: theme.Padding(),
		})

		animationLeft = left + theme.IconInlineSize() + theme.Padding()
	}

	tir.ti.firstTypingDot.Resize(fyne.Size{Width: dotSize, Height: dotSize})
	tir.ti.firstTypingDot.Move(fyne.Position{
		X: animationLeft,
		Y: size.Height/2 - dotSize/2,
	})

	tir.ti.secondTypingDot.Resize(fyne.Size{Width: dotSize, Height: dotSize})
	tir.ti.secondTypingDot.Move(fyne.Position{
		X: animationLeft + dotSize + theme.Padding(),
		Y: size.Height/2 - dotSize/2,
	})

	tir.ti.thirdTypingDot.Resize(fyne.Size{Width: dotSize, Height: dotSize})
	tir.ti.thirdTypingDot.Move(fyne.Position{
		X: animationLeft + dotSize + theme.Padding() + dotSize + theme.Padding(),
		Y: size.Height/2 - dotSize/2,
	})
}

func (tir *typingIndicatorRenderer) MinSize() fyne.Size {
	width := theme.IconInlineSize()*float32(len(tir.ti.icons)) + dotSize*3 + theme.Padding()*3
	if len(tir.ti.icons) > 1 {
		width -= theme.IconInlineSize() / 2
	}

	return fyne.Size{
		Width:  width,
		Height: theme.IconInlineSize() + theme.Padding()*2,
	}
}

func (tir *typingIndicatorRenderer) Objects() []fyne.CanvasObject {
	objs := []fyne.CanvasObject{}
	for _, icon := range tir.ti.icons {
		objs = append(objs, icon)
	}
	objs = append(objs, tir.ti.firstTypingDot)
	objs = append(objs, tir.ti.secondTypingDot)
	objs = append(objs, tir.ti.thirdTypingDot)
	return objs
}

func (tir *typingIndicatorRenderer) Refresh() {
	for _, obj := range tir.Objects() {
		obj.Refresh()
	}
}
