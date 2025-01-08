package ui

import (
	"image/color"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

var threadButtonHeight = float32(64)

type threadButton struct {
	widget.BaseWidget
	// TODO: mutex everything that changes this?
	threadImage               fyne.CanvasObject
	threadName                *widget.RichText
	lastMessage               *widget.RichText
	currentlyTypingUser       *widget.RichText
	typingAnimation           *fyne.Animation
	firstTypingDot            *canvas.Circle
	secondTypingDot           *canvas.Circle
	thirdTypingDot            *canvas.Circle
	threadNameFadeOut         *canvas.LinearGradient
	lastMessageTimeBackground *canvas.Rectangle
	lastMessageTimeFadeOut    *canvas.LinearGradient
	defaultTextFadeOut        *canvas.LinearGradient
	unreadCounterTextFadeOut  *canvas.LinearGradient
	unreadCounterBackground   *canvas.Rectangle
	unreadCounterCircle       *canvas.Circle
	unreadCounterText         *widget.RichText
	lastMessageTimeText       *widget.RichText
	lastMessageTime           time.Time
	statusIcons               *fyne.Container
	pending                   *canvas.Image
	synced                    *canvas.Image
	delivered                 *canvas.Image
	read                      *canvas.Image
	errorIcon                 *canvas.Image
	unreadCount               int
	clicked                   func()
}

func newThreadButton(image fyne.CanvasObject, name binding.String, clicked func()) *threadButton {
	if clicked == nil {
		log.Fatal("threadButton widgets must be defined with a clicked callback")
	}

	nameString, err := name.Get()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("data bindings broken for user name")

	}

	pending := canvas.NewImageFromResource(newEmbeddedResource("assets/icons/chat_bubble/white/png/pending.png"))
	pending.FillMode = canvas.ImageFillContain
	pending.Translucency = 0.4
	pending.SetMinSize(fyne.Size{iconSize, iconSize})
	pending.Hide()

	synced := canvas.NewImageFromResource(newEmbeddedResource("assets/icons/chat_bubble/white/png/synced.png"))
	synced.FillMode = canvas.ImageFillContain
	synced.Translucency = 0.4
	synced.SetMinSize(fyne.Size{iconSize, iconSize})
	synced.Hide()

	delivered := canvas.NewImageFromResource(newEmbeddedResource("assets/icons/chat_bubble/white/png/delivered.png"))
	delivered.FillMode = canvas.ImageFillContain
	delivered.Translucency = 0.4
	delivered.SetMinSize(fyne.Size{iconSize, iconSize})
	delivered.Hide()

	read := canvas.NewImageFromResource(newEmbeddedResource("assets/icons/chat_bubble/white/png/read.png"))
	read.FillMode = canvas.ImageFillContain
	read.Translucency = 0.4
	read.SetMinSize(fyne.Size{iconSize, iconSize})
	read.Hide()

	errorIcon := canvas.NewImageFromResource(newEmbeddedResource("assets/icons/chat_bubble/white/png/error.png"))
	errorIcon.FillMode = canvas.ImageFillContain
	errorIcon.Translucency = 0.4
	errorIcon.SetMinSize(fyne.Size{iconSize, iconSize})
	errorIcon.Hide()

	tb := &threadButton{
		threadImage: image,
		threadName: widget.NewRichText(&widget.TextSegment{
			Style: widget.RichTextStyle{
				SizeName: theme.SizeNameSubHeadingText,
			},
			Text: nameString,
		}),
		lastMessage: widget.NewRichText(
			&widget.TextSegment{
				Style: widget.RichTextStyle{
					Inline: true,
					TextStyle: fyne.TextStyle{
						Bold: true,
					},
				},
				Text: "",
			},
			&widget.TextSegment{
				Text: "",
			},
		),
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
		threadNameFadeOut: canvas.NewLinearGradient(
			color.RGBA{},
			theme.Color(theme.ColorNameBackground),
			270,
		),
		lastMessageTimeBackground: canvas.NewRectangle(theme.Color(theme.ColorNameBackground)),
		lastMessageTimeFadeOut: canvas.NewLinearGradient(
			color.RGBA{},
			theme.Color(theme.ColorNameBackground),
			270,
		),
		defaultTextFadeOut: canvas.NewLinearGradient(
			color.RGBA{},
			theme.Color(theme.ColorNameBackground),
			270,
		),
		unreadCounterTextFadeOut: canvas.NewLinearGradient(
			color.RGBA{},
			theme.Color(theme.ColorNameBackground),
			270,
		),
		unreadCounterBackground: canvas.NewRectangle(theme.Color(theme.ColorNameBackground)),
		unreadCounterCircle: canvas.NewCircle(color.RGBA{
			R: 0x21,
			G: 0x7e,
			B: 0xff,
			A: 0xff,
		}),
		unreadCounterText: widget.NewRichText(&widget.TextSegment{
			Style: widget.RichTextStyle{
				Alignment: fyne.TextAlignCenter,
				SizeName:  theme.SizeNameCaptionText,
			},
			Text: "",
		}),
		lastMessageTimeText: widget.NewRichText(&widget.TextSegment{
			Style: widget.RichTextStyle{
				SizeName: theme.SizeNameCaptionText,
			},
			Text: "",
		}),
		statusIcons: container.NewStack(
			pending,
			synced,
			delivered,
			read,
			errorIcon,
		),
		pending:   pending,
		synced:    synced,
		delivered: delivered,
		read:      read,
		errorIcon: errorIcon,
		clicked:   clicked,
	}

	// Bind the name and update the thread button when the name changes
	name.AddListener(binding.NewDataListener(func() {
		nameStr, err := name.Get()
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error getting data binding")
		}
		tb.threadName.Segments[0].(*widget.TextSegment).Text = nameStr
		tb.threadName.Refresh()
		tb.Refresh()
	}))

	tb.typingAnimation = &fyne.Animation{
		AutoReverse: false,
		Curve:       fyne.AnimationLinear,
		Duration:    1800 * time.Millisecond,
		RepeatCount: fyne.AnimationRepeatForever,
		Tick:        tb.updateTypingAnimation,
	}

	tb.firstTypingDot.Hide()
	tb.secondTypingDot.Hide()
	tb.thirdTypingDot.Hide()
	tb.currentlyTypingUser.Hide()
	tb.lastMessageTimeBackground.Hide()
	tb.lastMessageTimeFadeOut.Hide()
	tb.unreadCounterTextFadeOut.Hide()
	tb.unreadCounterBackground.Hide()
	tb.unreadCounterCircle.Hide()
	tb.unreadCounterText.Hide()
	tb.lastMessageTimeText.Hide()

	tb.ExtendBaseWidget(tb)
	return tb
}

func (tb *threadButton) Tapped(*fyne.PointEvent) {
	if tb.clicked != nil {
		tb.clicked()
	} else {
		log.Fatal("threadButton widgets must have a clickable callback")
	}
}

func (tb *threadButton) updateTypingAnimation(state float32) {
	dull := color.RGBA{R: 0x96, G: 0x96, B: 0x96, A: 0xff}
	bright := color.RGBA{R: 0xe3, G: 0xe3, B: 0xe3, A: 0xff}

	if state < 0.333 {
		tb.firstTypingDot.FillColor = bright
		tb.secondTypingDot.FillColor = dull
		tb.thirdTypingDot.FillColor = dull
		tb.firstTypingDot.Refresh()
		tb.secondTypingDot.Refresh()
		tb.thirdTypingDot.Refresh()
	} else if state < 0.666 {
		tb.firstTypingDot.FillColor = dull
		tb.secondTypingDot.FillColor = bright
		tb.thirdTypingDot.FillColor = dull
		tb.firstTypingDot.Refresh()
		tb.secondTypingDot.Refresh()
		tb.thirdTypingDot.Refresh()
	} else {
		tb.firstTypingDot.FillColor = dull
		tb.secondTypingDot.FillColor = dull
		tb.thirdTypingDot.FillColor = bright
		tb.firstTypingDot.Refresh()
		tb.secondTypingDot.Refresh()
		tb.thirdTypingDot.Refresh()
	}
}

func (tb *threadButton) setLastMessage(name, text string) { // TODO: will need to detect when a user's name changes and they are the last message and update it here, or reference by UUID
	tb.lastMessage.Segments[0].(*widget.TextSegment).Text = name + ": "
	tb.lastMessage.Segments[0].(*widget.TextSegment).Style.TextStyle.Italic = false
	tb.lastMessage.Segments[0].(*widget.TextSegment).Style.TextStyle.Bold = true
	tb.lastMessage.Segments[1].(*widget.TextSegment).Text = strings.ReplaceAll(text, "\n", " ") // TODO: use non-platform-scpeific newlines
	tb.lastMessage.Refresh()
}

func (tb *threadButton) setLastAction(text string) {
	tb.lastMessage.Segments[0].(*widget.TextSegment).Text = text
	tb.lastMessage.Segments[0].(*widget.TextSegment).Style.TextStyle.Italic = true
	tb.lastMessage.Segments[0].(*widget.TextSegment).Style.TextStyle.Bold = false
	tb.lastMessage.Segments[1].(*widget.TextSegment).Text = ""
	tb.lastMessage.Refresh()
}

// TODO: use when all messages expire or history is cleared
func (tb *threadButton) clearLastMessage() {
	tb.lastMessage.Segments[0].(*widget.TextSegment).Text = ""
	tb.lastMessage.Segments[1].(*widget.TextSegment).Text = ""
	tb.lastMessage.Refresh()
}

func (tb *threadButton) showLastMessageState(state int) {
	if tb.unreadCount > 0 {
		return
	}

	switch state {
	case statePending:
		tb.pending.Show()
		tb.synced.Hide()
		tb.delivered.Hide()
		tb.read.Hide()
		tb.errorIcon.Hide()
	case stateSynced:
		tb.pending.Hide()
		tb.synced.Show()
		tb.delivered.Hide()
		tb.read.Hide()
		tb.errorIcon.Hide()
	case stateDelivered:
		tb.pending.Hide()
		tb.synced.Hide()
		tb.delivered.Show()
		tb.read.Hide()
		tb.errorIcon.Hide()
	case stateRead:
		tb.pending.Hide()
		tb.synced.Hide()
		tb.delivered.Hide()
		tb.read.Show()
		tb.errorIcon.Hide()
	case stateError:
		tb.pending.Hide()
		tb.synced.Hide()
		tb.delivered.Hide()
		tb.read.Hide()
		tb.errorIcon.Show()
	}
	tb.statusIcons.Show()
	tb.statusIcons.Refresh()

	tb.unreadCounterTextFadeOut.Show()
	tb.unreadCounterBackground.Show()
}

func (tb *threadButton) hideLastMessageState() {
	tb.statusIcons.Hide()
}

func (tb *threadButton) showCurrentStatusIcon() {
	tb.unreadCounterTextFadeOut.Show()
	tb.unreadCounterBackground.Show()
	tb.statusIcons.Show()
}

func (tb *threadButton) setLastMessageTime(timestamp time.Time) {
	tb.lastMessageTime = timestamp
	tb.updateLastMessageTimeText()
}

func (tb *threadButton) updateLastMessageTimeText() { //TODO: use the same time string as thread items?
	elapsed := time.Since(tb.lastMessageTime)
	displayTime := ""

	if elapsed.Seconds() < 60 {
		displayTime = "now"
	} else if elapsed.Minutes() < 60 {
		displayTime = strconv.Itoa(int(elapsed.Minutes())) + "m"
	} else if elapsed.Hours() < 24 {
		displayTime = strconv.Itoa(int(elapsed.Hours())) + "h"
	} else if elapsed.Hours() < 24*7 {
		displayTime = tb.lastMessageTime.Weekday().String()[0:3]
	} else if elapsed.Hours() < 24*365 { // TODO: bad time assumption but whatever
		displayTime = tb.lastMessageTime.Month().String()[0:3] + " " + strconv.Itoa(tb.lastMessageTime.Day())
	} else {
		displayTime = tb.lastMessageTime.Month().String()[0:3] + " " + strconv.Itoa(tb.lastMessageTime.Day()) + " " + strconv.Itoa(tb.lastMessageTime.Year())
	}

	tb.lastMessageTimeText.Segments[0].(*widget.TextSegment).Text = displayTime
	tb.lastMessageTimeBackground.Show()
	tb.lastMessageTimeFadeOut.Show()
	tb.lastMessageTimeText.Show()
	// TODO: hide the default fade out here since it isn't needed?
	tb.lastMessageTimeText.Refresh()
}

func (tb *threadButton) addUnread() {
	tb.unreadCount += 1
	tb.displayCorrectUnreadCount()
}

func (tb *threadButton) removeUnread() {
	tb.unreadCount -= 1
	tb.displayCorrectUnreadCount()
}

func (tb *threadButton) setUnreadCount(n int) {
	tb.unreadCount = n
	tb.displayCorrectUnreadCount()
}

func (tb *threadButton) clearUnreadCount() {
	tb.unreadCount = 0
	tb.displayCorrectUnreadCount()
}

func (tb *threadButton) displayCorrectUnreadCount() {
	if tb.unreadCount < 0 {
		tb.unreadCount = 0
	}

	if tb.unreadCount == 0 {
		tb.unreadCounterText.Hide()
		if !tb.statusIcons.Visible() {
			tb.unreadCounterTextFadeOut.Hide()
			tb.unreadCounterBackground.Hide()
		}
		tb.unreadCounterCircle.Hide()
	} else {
		tb.statusIcons.Hide()
		unreadCountDisplay := strconv.Itoa(tb.unreadCount)
		if tb.unreadCount > 999 {
			unreadCountDisplay = "999+"
		}
		tb.unreadCounterText.Segments[0].(*widget.TextSegment).Text = unreadCountDisplay
		tb.unreadCounterText.Refresh()
		tb.unreadCounterText.Show()
		//tb.defaultTextFadeOut.Hide()  // TODO: hide the unneeded gradients when they aren't needed?
		tb.unreadCounterTextFadeOut.Show()
		tb.unreadCounterBackground.Show()
		tb.unreadCounterCircle.Show()
	}
}

func (tb *threadButton) startTyping(name string) {
	tb.lastMessage.Hide()
	tb.statusIcons.Hide()
	tb.currentlyTypingUser.Segments[0].(*widget.TextSegment).Text = name + ": "
	tb.currentlyTypingUser.Refresh()
	tb.currentlyTypingUser.Show()
	tb.firstTypingDot.Show()
	tb.secondTypingDot.Show()
	tb.thirdTypingDot.Show()
	tb.typingAnimation.Start()
}

func (tb *threadButton) stopTyping() {
	tb.firstTypingDot.Hide()
	tb.secondTypingDot.Hide()
	tb.thirdTypingDot.Hide()
	tb.typingAnimation.Stop()
	tb.currentlyTypingUser.Hide()
	tb.lastMessage.Show()
}

//func (tb *threadButton) Hide() {} // TODO: need to call into base widget here?

//func (tb *threadButton) MinSize() {} // TODO: needed?

func (tb *threadButton) CreateRenderer() fyne.WidgetRenderer {
	tb.ExtendBaseWidget(tb)

	tbr := &threadButtonRenderer{
		threadButton: tb,
	}

	return tbr
}

type threadButtonRenderer struct {
	threadButton *threadButton
}

func (tbr *threadButtonRenderer) Destroy() {}

func (tbr *threadButtonRenderer) Layout(size fyne.Size) {
	tbr.threadButton.threadImage.Resize(fyne.Size{Width: threadButtonHeight, Height: threadButtonHeight})
	tbr.threadButton.threadImage.Move(fyne.Position{
		X: 0,
		Y: 0,
	})
	tbr.threadButton.threadName.Move(fyne.Position{
		X: threadButtonHeight,
		Y: 0 - theme.Padding()*2,
	})
	tbr.threadButton.lastMessage.Move(fyne.Position{
		X: threadButtonHeight,
		Y: threadButtonHeight - tbr.threadButton.lastMessage.MinSize().Height + theme.Padding(),
	})
	tbr.threadButton.currentlyTypingUser.Move(fyne.Position{
		X: threadButtonHeight,
		Y: threadButtonHeight - tbr.threadButton.currentlyTypingUser.MinSize().Height + theme.Padding(),
	})
	tbr.threadButton.firstTypingDot.Resize(fyne.Size{Width: threadButtonHeight / 8, Height: threadButtonHeight / 8})
	tbr.threadButton.firstTypingDot.Move(fyne.Position{
		X: threadButtonHeight + tbr.threadButton.currentlyTypingUser.MinSize().Width - theme.Padding(),
		Y: threadButtonHeight - threadButtonHeight/4,
	})
	tbr.threadButton.secondTypingDot.Resize(fyne.Size{Width: threadButtonHeight / 8, Height: threadButtonHeight / 8})
	tbr.threadButton.secondTypingDot.Move(fyne.Position{
		X: threadButtonHeight + tbr.threadButton.currentlyTypingUser.MinSize().Width - theme.Padding() + threadButtonHeight/8 + theme.Padding(),
		Y: threadButtonHeight - threadButtonHeight/4,
	})
	tbr.threadButton.thirdTypingDot.Resize(fyne.Size{Width: threadButtonHeight / 8, Height: threadButtonHeight / 8})
	tbr.threadButton.thirdTypingDot.Move(fyne.Position{
		X: threadButtonHeight + tbr.threadButton.currentlyTypingUser.MinSize().Width - theme.Padding() + threadButtonHeight/8 + theme.Padding() + threadButtonHeight/8 + theme.Padding(),
		Y: threadButtonHeight - threadButtonHeight/4,
	})
	tbr.threadButton.threadNameFadeOut.Resize(fyne.Size{Width: (threadButtonHeight / 4) + theme.Padding(), Height: threadButtonHeight / 2})
	tbr.threadButton.threadNameFadeOut.Move(fyne.Position{
		X: size.Width - threadButtonHeight/4,
		Y: 0,
	})
	tbr.threadButton.defaultTextFadeOut.Resize(fyne.Size{Width: (threadButtonHeight / 4) + theme.Padding(), Height: threadButtonHeight / 2})
	tbr.threadButton.defaultTextFadeOut.Move(fyne.Position{
		X: size.Width - threadButtonHeight/4,
		Y: threadButtonHeight / 2,
	})

	if tbr.threadButton.statusIcons.Visible() {
		tbr.threadButton.statusIcons.Resize(fyne.Size{Width: theme.IconInlineSize(), Height: theme.IconInlineSize()})
		tbr.threadButton.statusIcons.Move(fyne.Position{
			X: size.Width - theme.IconInlineSize() - theme.Padding(),
			Y: size.Height - theme.IconInlineSize() - theme.Padding(),
		})

		tbr.threadButton.unreadCounterTextFadeOut.Resize(fyne.Size{Width: theme.IconInlineSize(), Height: theme.IconInlineSize()})
		tbr.threadButton.unreadCounterTextFadeOut.Move(fyne.Position{
			X: size.Width - theme.IconInlineSize() - theme.IconInlineSize(),
			Y: size.Height - theme.IconInlineSize() - theme.Padding(),
		})
		tbr.threadButton.unreadCounterBackground.Resize(fyne.Size{Width: theme.IconInlineSize(), Height: theme.IconInlineSize()})
		tbr.threadButton.unreadCounterBackground.Move(fyne.Position{
			X: size.Width - theme.IconInlineSize(),
			Y: size.Height - theme.IconInlineSize() - theme.Padding(),
		})
	}

	if tbr.threadButton.unreadCounterText.Visible() {
		tbr.threadButton.unreadCounterCircle.Resize(fyne.Size{Width: threadButtonHeight / 2, Height: threadButtonHeight / 2})
		tbr.threadButton.unreadCounterCircle.Move(fyne.Position{
			X: size.Width - threadButtonHeight/2,
			Y: threadButtonHeight / 2,
		})
		tbr.threadButton.unreadCounterText.Move(fyne.Position{
			X: size.Width - threadButtonHeight/4,
			Y: threadButtonHeight / 2,
		})
		tbr.threadButton.unreadCounterTextFadeOut.Resize(fyne.Size{Width: threadButtonHeight / 4, Height: threadButtonHeight / 2})
		tbr.threadButton.unreadCounterTextFadeOut.Move(fyne.Position{
			X: size.Width - threadButtonHeight/2 - threadButtonHeight/4,
			Y: threadButtonHeight / 2,
		})
		tbr.threadButton.unreadCounterBackground.Resize(fyne.Size{Width: (threadButtonHeight / 2) + theme.Padding(), Height: threadButtonHeight / 2})
		tbr.threadButton.unreadCounterBackground.Move(fyne.Position{
			X: size.Width - threadButtonHeight/2,
			Y: threadButtonHeight / 2,
		})
	}

	tbr.threadButton.lastMessageTimeText.Move(fyne.Position{
		X: size.Width - tbr.threadButton.lastMessageTimeText.MinSize().Width + theme.Padding(),
		Y: 0,
	})
	tbr.threadButton.lastMessageTimeBackground.Resize(tbr.threadButton.lastMessageTimeText.MinSize())
	tbr.threadButton.lastMessageTimeBackground.Move(fyne.Position{
		X: size.Width - tbr.threadButton.lastMessageTimeText.MinSize().Width + theme.Padding(),
		Y: 0,
	})
	tbr.threadButton.lastMessageTimeFadeOut.Resize(fyne.Size{Width: threadButtonHeight / 4, Height: threadButtonHeight / 2})
	tbr.threadButton.lastMessageTimeFadeOut.Move(fyne.Position{
		X: size.Width - tbr.threadButton.lastMessageTimeText.MinSize().Width + theme.Padding() - threadButtonHeight/4,
		Y: 0,
	})
}

func (tbr *threadButtonRenderer) MinSize() fyne.Size {
	return fyne.Size{Width: threadButtonHeight * 5, Height: threadButtonHeight}
}

func (tbr *threadButtonRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{
		tbr.threadButton.threadImage,
		tbr.threadButton.threadName,
		tbr.threadButton.lastMessage,
		tbr.threadButton.currentlyTypingUser,
		tbr.threadButton.firstTypingDot,
		tbr.threadButton.secondTypingDot,
		tbr.threadButton.thirdTypingDot,
		tbr.threadButton.threadNameFadeOut,
		tbr.threadButton.lastMessageTimeBackground,
		tbr.threadButton.lastMessageTimeFadeOut,
		tbr.threadButton.defaultTextFadeOut,
		tbr.threadButton.unreadCounterTextFadeOut,
		tbr.threadButton.unreadCounterBackground,
		tbr.threadButton.unreadCounterCircle,
		tbr.threadButton.unreadCounterText,
		tbr.threadButton.lastMessageTimeText,
		tbr.threadButton.statusIcons,
	}
}

func (tbr *threadButtonRenderer) Refresh() {
	tbr.threadButton.threadNameFadeOut.EndColor = theme.Color(theme.ColorNameBackground)
	tbr.threadButton.threadNameFadeOut.Refresh()

	tbr.threadButton.lastMessageTimeBackground.FillColor = theme.Color(theme.ColorNameBackground)
	tbr.threadButton.lastMessageTimeBackground.Refresh()

	tbr.threadButton.lastMessageTimeFadeOut.EndColor = theme.Color(theme.ColorNameBackground)
	tbr.threadButton.lastMessageTimeFadeOut.Refresh()

	tbr.threadButton.defaultTextFadeOut.EndColor = theme.Color(theme.ColorNameBackground)
	tbr.threadButton.defaultTextFadeOut.Refresh()

	tbr.threadButton.unreadCounterTextFadeOut.EndColor = theme.Color(theme.ColorNameBackground)
	tbr.threadButton.unreadCounterTextFadeOut.Refresh()

	tbr.threadButton.unreadCounterBackground.FillColor = theme.Color(theme.ColorNameBackground)
	tbr.threadButton.unreadCounterBackground.Refresh()

	for _, obj := range tbr.Objects() {
		obj.Refresh()
	}
}
