package ui

import (
	"image/color"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

var threadButtonHeight = float32(64)

type threadButton struct {
	widget.BaseWidget
	threadImage               *defaultImage
	threadName                *widget.RichText
	lastMessage               *widget.RichText
	draft                     *widget.RichText
	lastMessageHasStatus      bool
	typingIndicator           *typingIndicator
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
	pending                   *themedImage
	synced                    *themedImage
	delivered                 *themedImage
	read                      *themedImage
	errorIcon                 *themedImage
	unreadCount               int
	clicked                   func()
}

func newThreadButtonTemplate(fileGetter func(uuid.UUID) ([]byte, error)) *threadButton {
	pending := newThemedImage(
		newEmbeddedResource("assets/icons/chat_bubble/white/png/pending.png"),
		newEmbeddedResource("assets/icons/chat_bubble/black/png/pending.png"),
		iconSize,
		iconSize,
	)
	pending.image.Translucency = 0.4
	pending.Hide()

	synced := newThemedImage(
		newEmbeddedResource("assets/icons/chat_bubble/white/png/synced.png"),
		newEmbeddedResource("assets/icons/chat_bubble/black/png/synced.png"),
		iconSize,
		iconSize,
	)
	synced.image.Translucency = 0.4
	synced.Hide()

	delivered := newThemedImage(
		newEmbeddedResource("assets/icons/chat_bubble/white/png/delivered.png"),
		newEmbeddedResource("assets/icons/chat_bubble/black/png/delivered.png"),
		iconSize,
		iconSize,
	)
	delivered.image.Translucency = 0.4
	delivered.Hide()

	read := newThemedImage(
		newEmbeddedResource("assets/icons/chat_bubble/white/png/read.png"),
		newEmbeddedResource("assets/icons/chat_bubble/black/png/read.png"),
		iconSize,
		iconSize,
	)
	read.image.Translucency = 0.4
	read.Hide()

	errorIcon := newThemedImage(
		newEmbeddedResource("assets/icons/chat_bubble/white/png/error.png"),
		newEmbeddedResource("assets/icons/chat_bubble/black/png/error.png"),
		iconSize,
		iconSize,
	)
	errorIcon.image.Translucency = 0.4
	errorIcon.Hide()

	tb := &threadButton{
		threadImage: newDefaultImage(uuid.Nil, []uuid.UUID{}, "", 64, fileGetter, nil),
		threadName: widget.NewRichText(&widget.TextSegment{
			Style: widget.RichTextStyle{
				SizeName: theme.SizeNameSubHeadingText,
			},
			Text: "",
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
		draft: widget.NewRichText(
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
		typingIndicator: newTypingIndicator(typingIndicatorModeText, fileGetter),
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
				ColorName: theme.ColorNameForegroundOnPrimary,
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
		clicked:   nil,
	}
	tb.ExtendBaseWidget(tb)
	return tb
}

func (tb *threadButton) setContent(tbd *threadButtonData) {
	tb.threadName.Segments[0].(*widget.TextSegment).Text = tbd.name
	tb.threadName.Refresh()

	tb.clicked = tbd.clicked

	previousID := tb.threadImage.id
	if tbd.id != previousID || !sameImages(tbd.images, tb.threadImage.images) {
		tb.threadImage.id = tbd.id
		tb.threadImage.images = tbd.images
		tb.Refresh()
	}
	tb.threadImage.clicked = tbd.clicked
	tb.threadImage.setString(tbd.initials)

	if tbd.draft == "" {
		tb.draft.Segments[1].(*widget.TextSegment).Text = ""
		tb.draft.Hide()
		tb.lastMessageHasStatus = tbd.showLastMessageState
		tb.lastMessage.Segments[0].(*widget.TextSegment).Text = tbd.lastMessageSegmentOne
		tb.lastMessage.Segments[0].(*widget.TextSegment).Style.TextStyle.Italic = tbd.lastMessageSegmentOneItalic
		tb.lastMessage.Segments[0].(*widget.TextSegment).Style.TextStyle.Bold = tbd.lastMessageSegmentOneBold
		tb.lastMessage.Segments[1].(*widget.TextSegment).Text = tbd.lastMessageSegmentTwo
		tb.lastMessage.Refresh()
		tb.lastMessage.Show()
	} else {
		tb.draft.Segments[0].(*widget.TextSegment).Text = "Draft: "
		tb.draft.Segments[0].(*widget.TextSegment).Style.TextStyle.Italic = true
		tb.draft.Segments[0].(*widget.TextSegment).Style.TextStyle.Bold = true
		tb.draft.Segments[1].(*widget.TextSegment).Text = strings.ReplaceAll(tbd.draft, "\n", " ") // TODO: use non-platform-scpeific newlines?
		tb.draft.Segments[1].(*widget.TextSegment).Style.TextStyle.Italic = true
		tb.draft.Refresh()
		tb.lastMessage.Hide()
		tb.draft.Show()
	}

	if tbd.showLastMessageState && tbd.draft == "" {
		if tbd.unreadCount == 0 {
			switch tbd.lastMessageState {
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
	} else {
		tb.statusIcons.Hide()
	}

	tb.typingIndicator.setUsers(tbd.typing)
	if len(tb.typingIndicator.icons) == 0 {
		tb.typingIndicator.typingAnimation.Stop()
		tb.typingIndicator.Hide()
	} else {
		tb.draft.Hide()
		tb.lastMessage.Hide()
		tb.typingIndicator.typingAnimation.Start()
		tb.typingIndicator.Show()
	}
	tb.typingIndicator.updateObjects()
	tb.typingIndicator.Refresh()

	tb.lastMessageTime = tbd.lastMessageTime
	tb.updateLastMessageTimeText()

	tb.unreadCount = tbd.unreadCount
	tb.displayCorrectUnreadCount()
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
		tb.unreadCounterTextFadeOut.Show()
		tb.unreadCounterBackground.Show()
		tb.unreadCounterCircle.Show()
	}
}

func (tb *threadButton) Tapped(*fyne.PointEvent) {
	if tb.clicked != nil {
		tb.clicked()
	}
}

func (tb *threadButton) MouseIn(*desktop.MouseEvent) {
}

func (tb *threadButton) MouseMoved(*desktop.MouseEvent) {
}

func (tb *threadButton) MouseOut() {
}

func (tb *threadButton) CreateRenderer() fyne.WidgetRenderer {
	tb.ExtendBaseWidget(tb)

	tbr := &threadButtonRenderer{
		threadButton: tb,
		objects: []fyne.CanvasObject{
			tb.threadImage,
			tb.threadName,
			tb.lastMessage,
			tb.draft,
			tb.typingIndicator,
			tb.threadNameFadeOut,
			tb.lastMessageTimeBackground,
			tb.lastMessageTimeFadeOut,
			tb.defaultTextFadeOut,
			tb.unreadCounterTextFadeOut,
			tb.unreadCounterBackground,
			tb.unreadCounterCircle,
			tb.unreadCounterText,
			tb.lastMessageTimeText,
			tb.statusIcons,
		},
	}

	return tbr
}

type threadButtonRenderer struct {
	threadButton *threadButton
	objects      []fyne.CanvasObject
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

	tbr.threadButton.typingIndicator.Resize(tbr.threadButton.typingIndicator.MinSize())
	tbr.threadButton.typingIndicator.Move(fyne.Position{
		X: threadButtonHeight,
		Y: threadButtonHeight - tbr.threadButton.typingIndicator.MinSize().Height + theme.Padding(),
	})
	tbr.threadButton.lastMessage.Move(fyne.Position{
		X: threadButtonHeight,
		Y: threadButtonHeight - tbr.threadButton.lastMessage.MinSize().Height + theme.Padding(),
	})
	tbr.threadButton.draft.Move(fyne.Position{
		X: threadButtonHeight,
		Y: threadButtonHeight - tbr.threadButton.draft.MinSize().Height + theme.Padding(),
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

		tbr.threadButton.statusIcons.Hide()
	}

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
	return tbr.objects
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

	if tbr.threadButton.typingIndicator.Visible() {
		tbr.threadButton.lastMessage.Hide()
		tbr.threadButton.draft.Hide()
		tbr.threadButton.statusIcons.Hide()
	} else {
		if len(tbr.threadButton.draft.Segments[1].(*widget.TextSegment).Text) > 0 {
			tbr.threadButton.draft.Show()
		} else {
			tbr.threadButton.lastMessage.Show()
		}
		if tbr.threadButton.lastMessageHasStatus {
			tbr.threadButton.unreadCounterTextFadeOut.Show()
			tbr.threadButton.unreadCounterBackground.Show()
			tbr.threadButton.statusIcons.Show()
		}
	}
}

type threadButtonData struct {
	id                          uuid.UUID
	name                        string
	initials                    string
	images                      []uuid.UUID
	clicked                     func()
	lastMessageSegmentOne       string
	lastMessageSegmentOneItalic bool
	lastMessageSegmentOneBold   bool
	lastMessageSegmentTwo       string
	lastMessageSegmentTwoItalic bool
	lastMessageSegmentTwoBold   bool
	draft                       string
	lastMessageState            int
	showLastMessageState        bool
	unreadCount                 int
	lastMessageTime             time.Time
	typing                      []*user
}

func (tbd *threadButtonData) setName(name, initials string) {
	tbd.name = name
	tbd.initials = initials
}

func (tbd *threadButtonData) setLastMessage(name, text string, status bool) { // TODO: will need to detect when a user's name changes and they are the last message and update it here, or reference by UUID
	if len(text) > 200 {
		text = text[:200]
	}
	tbd.showLastMessageState = status
	tbd.lastMessageSegmentOne = name + ": "
	tbd.lastMessageSegmentOneItalic = false
	tbd.lastMessageSegmentOneBold = true
	tbd.lastMessageSegmentTwo = strings.ReplaceAll(text, "\n", " ") // TODO: use non-platform-scpeific newlines?
}

func (tbd *threadButtonData) setLastAction(text string, status bool) {
	tbd.showLastMessageState = status
	tbd.lastMessageSegmentOne = text
	tbd.lastMessageSegmentOneItalic = true
	tbd.lastMessageSegmentOneBold = false
	tbd.lastMessageSegmentTwo = ""
}

func (tbd *threadButtonData) setDraft(text string) {
	if len(text) > 200 {
		text = text[:200]
	}
	tbd.draft = text
}

func (tbd *threadButtonData) setShowLastMessageState(state int) {
	tbd.lastMessageState = state
	tbd.showLastMessageState = true
}

func (tbd *threadButtonData) hideLastMessageState() {
	tbd.showLastMessageState = false
}

func (tbd *threadButtonData) setLastMessageTime(timestamp time.Time) {
	tbd.lastMessageTime = timestamp
}

func (tbd *threadButtonData) setUnreadCount(n int) {
	tbd.unreadCount = n
}

func (tbd *threadButtonData) showTypingUser(u *user) {
	for _, di := range tbd.typing {
		if di.id == u.id {
			return
		}
	}

	tbd.typing = append(tbd.typing, u)
}

func (tbd *threadButtonData) hideTypingUser(userID uuid.UUID) {
	usersWithoutUser := []*user{}

	for _, u := range tbd.typing {
		if u.id != userID {
			usersWithoutUser = append(usersWithoutUser, u)
		}
	}

	tbd.typing = usersWithoutUser
}

func sameImages(old, updated []uuid.UUID) bool {
	if len(old) != len(updated) {
		return false
	}
	for i, val := range old {
		if updated[i] != val {
			return false
		}
	}
	return true
}
