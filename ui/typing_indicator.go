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

const typingIndicatorModeText = 0
const typingIndicatorModeIcons = 1

const maxTypingUsers = 8
const dotSize = float32(8)

type typingIndicator struct {
	widget.BaseWidget

	mode                int
	users               []*user
	icons               []*defaultImage
	fileGetter          func(uuid.UUID) ([]byte, error)
	currentlyTypingUser *widget.RichText
	typingAnimation     *fyne.Animation
	firstTypingDot      *canvas.Circle
	secondTypingDot     *canvas.Circle
	thirdTypingDot      *canvas.Circle
	objects             []fyne.CanvasObject
}

func newTypingIndicator(mode int, fileGetter func(uuid.UUID) ([]byte, error)) *typingIndicator {
	ti := &typingIndicator{
		mode:       mode,
		icons:      []*defaultImage{},
		fileGetter: fileGetter,
		currentlyTypingUser: widget.NewRichText(
			&widget.TextSegment{
				Style: widget.RichTextStyle{
					Inline: true,
					TextStyle: fyne.TextStyle{
						Bold: true,
					},
				},
				Text: "",
			},
		),
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

	ti.updateObjects()

	ti.ExtendBaseWidget(ti)

	return ti
}

func (ti *typingIndicator) updateObjects() {
	objs := []fyne.CanvasObject{}
	for _, icon := range ti.icons {
		objs = append(objs, icon)
	}
	objs = append(objs, ti.currentlyTypingUser)
	objs = append(objs, ti.firstTypingDot)
	objs = append(objs, ti.secondTypingDot)
	objs = append(objs, ti.thirdTypingDot)
	ti.objects = objs
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

	userIcon := newDefaultImage(u.id, u.images, u.initials, theme.IconInlineSize(), ti.fileGetter, nil)
	userIcon.images = u.images
	ti.icons = append(
		ti.icons,
		userIcon,
	)
	ti.users = append(
		ti.users,
		u,
	)

	if len(ti.icons) == 1 {
		ti.typingAnimation.Start()
		ti.Show()
	}

	ti.updateObjects()
	ti.Refresh()
}

func (ti *typingIndicator) hideUser(userID uuid.UUID) {
	iconsWithoutUser := []*defaultImage{}
	usersWithoutUser := []*user{}

	for _, di := range ti.icons {
		if di.id != userID {
			iconsWithoutUser = append(iconsWithoutUser, di)
		}
	}
	for _, u := range ti.users {
		if u.id != userID {
			usersWithoutUser = append(usersWithoutUser, u)
		}
	}

	ti.icons = iconsWithoutUser
	ti.users = usersWithoutUser

	if len(ti.icons) == 0 {
		ti.typingAnimation.Stop()
		ti.Hide()
	}

	ti.updateObjects()
	ti.Refresh()
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

	if tir.ti.mode == typingIndicatorModeIcons {
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
	} else {
		// TODO: resize it to the available space and truncate the username
		tir.ti.currentlyTypingUser.Move(fyne.Position{
			X: 0,
			Y: size.Height/2 - tir.ti.currentlyTypingUser.MinSize().Height/2,
		})
		animationLeft = tir.ti.currentlyTypingUser.MinSize().Width
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
	var width float32
	var height float32

	if tir.ti.mode == typingIndicatorModeIcons {
		width = theme.IconInlineSize()*float32(len(tir.ti.icons)) + dotSize*3 + theme.Padding()*3
		if len(tir.ti.icons) > 1 {
			width -= theme.IconInlineSize() / 2
		}
		height = theme.IconInlineSize() + theme.Padding()*2
	} else {
		width = tir.ti.currentlyTypingUser.MinSize().Width + theme.Padding()*2 + dotSize*3 + theme.Padding()*3
		height = tir.ti.currentlyTypingUser.MinSize().Height
	}

	return fyne.Size{
		Width:  width,
		Height: height,
	}
}

func (tir *typingIndicatorRenderer) Objects() []fyne.CanvasObject {
	return tir.ti.objects
}

func (tir *typingIndicatorRenderer) Refresh() {
	if tir.ti.mode == typingIndicatorModeIcons {
		for _, icon := range tir.ti.icons {
			icon.Show()
		}
		tir.ti.currentlyTypingUser.Hide()
	} else {
		for _, icon := range tir.ti.icons {
			icon.Hide()
		}
		if len(tir.ti.users) > 0 {
			u := tir.ti.users[len(tir.ti.users)-1]
			tir.ti.currentlyTypingUser.Segments[0].(*widget.TextSegment).Text = u.getDisplayName() + ":"
		}
		tir.ti.currentlyTypingUser.Show()
	}

	for _, obj := range tir.Objects() {
		obj.Refresh()
	}
}
