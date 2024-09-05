package ui

import (
	"image"
	"image/color"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

type chatBubble struct {
	widget.BaseWidget
	id            uuid.UUID
	timestamp     int64
	outgoing      bool
	direct        bool
	timestampText *canvas.Text
	username      *canvas.Text
	message       *widget.Label
	icon          *defaultImage
	background    *canvas.Rectangle
}

func newChatBubbleTemplate() *chatBubble {
	// TODO: I'm just using a widget.Label because it comes with wrapping out of the box, and all the wrapping code
	// isn't exported and non-trivial to copy out.  A canvas.Text would probably be better as I would have more
	// control over the presentation of the font and could hopefully enable click and drag selection for copying
	// text, but how to do this remains an open question.  Also, using a label comes with padding out of the box
	// that I don't want.

	messageLabel := widget.NewLabel("")
	messageLabel.Wrapping = fyne.TextWrapWord

	username := canvas.NewText("", &color.RGBA{})
	username.TextStyle.Bold = true

	timestampText := canvas.NewText("", theme.ForegroundColor())
	timestampText.TextSize = theme.TextSize() * 0.6

	// Incoming messages have a grey background, outgoing messages have a blue background
	background := &canvas.Rectangle{
		CornerRadius: 15,
	}

	bubble := &chatBubble{
		id:       uuid.Nil,
		username: username,
		message:  messageLabel,
		icon: &defaultImage{
			size: theme.IconInlineSize(),
			foregroundText: &canvas.Text{
				Text:     "",
				TextSize: theme.IconInlineSize() / 2,
			},
			backgroundColor: canvas.NewImageFromImage(makeCircle(&colorRectangle{
				rect: image.Rect(0, 0, int(theme.IconInlineSize())*8, int(theme.IconInlineSize())*8),
			})),
		},
		outgoing:      false,
		direct:        false,
		timestamp:     0,
		timestampText: timestampText,
		background:    background,
	}

	bubble.ExtendBaseWidget(bubble)
	return bubble
}

func (bubble *chatBubble) setData(name, initials binding.String, authorID, id uuid.UUID, message string, outgoing, direct bool, timestamp int64) { // TODO: export chat.Message for this?
	bubble.id = id

	usernameText, err := name.Get()
	if err != nil {
		log.Fatal("data bindings broken")
	}
	bubble.username.Text = usernameText
	bubble.username.Color = uuidToColor(authorID)
	if outgoing || direct {
		bubble.username.Hide()
	} else {
		bubble.username.Show()
	}
	bubble.username.Refresh()

	bubble.message.Text = message
	bubble.message.Refresh()

	bubble.timestamp = timestamp
	bubble.timestampText.Text = time.Unix(timestamp, 0).Format("1/2 15:04")

	bubble.outgoing = outgoing
	bubble.direct = direct

	if !outgoing && !direct {
		iconText, err := initials.Get()
		if err != nil {
			log.Fatal("data bindings broken")
		}
		bubble.icon.foregroundText.Text = iconText
		bubble.icon.backgroundColor = canvas.NewImageFromImage(makeCircle(&colorRectangle{
			rect:  image.Rect(0, 0, int(theme.IconInlineSize())*8, int(theme.IconInlineSize())*8),
			color: uuidToColor(authorID), // TODO: access this color without recreating the whole thing?
		}))
		if fyne.CurrentApp().Settings().ThemeVariant() == theme.VariantLight {
			bubble.icon.foregroundText.Color = color.RGBA{0xff, 0xff, 0xff, 0xff}
		}
		bubble.icon.clicked = func() { // TODO: not clickable
			log.WithFields(log.Fields{
				"id":   authorID,
				"name": usernameText,
			}).Info("user wants to open profile via icon")
		}
		bubble.icon.Show()
		bubble.icon.Refresh()
	} else {
		bubble.icon.Hide()
	}

	if fyne.CurrentApp().Settings().ThemeVariant() == theme.VariantLight {
		if outgoing {
			bubble.background.FillColor = color.NRGBA{0xb5, 0xd0, 0xff, 0xff}
		} else {
			bubble.background.FillColor = color.NRGBA{0xdd, 0xdd, 0xdd, 0xff}
		}
	} else {
		if outgoing {
			bubble.background.FillColor = color.NRGBA{0, 0x23, 0x75, 0xff}
		} else {
			bubble.background.FillColor = color.NRGBA{0x20, 0x20, 0x20, 0xff}
		}
	}

	name.AddListener(binding.NewDataListener(func() {
		nameStr, err := name.Get()
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error getting data binding")
		}
		bubble.username.Text = nameStr
		bubble.username.Refresh()
		bubble.Refresh()
	}))
}

func (bubble *chatBubble) CreateRenderer() fyne.WidgetRenderer {
	bubble.ExtendBaseWidget(bubble)

	renderer := &bubbleRenderer{
		background:  bubble.background,
		bubble:      bubble,
		message:     bubble.message,
		icon:        bubble.icon,
		timestamp:   bubble.timestampText,
		longestLine: longestLine(bubble.message.Text), // We have to know the longest line for width calculation, more on that later
		objects: []fyne.CanvasObject{
			bubble.background,
			bubble.username,
			bubble.message,
			bubble.timestampText,
			bubble.icon,
		},
		verticalPaddingAboveBackground:      theme.Padding(),
		verticalPaddingAboveUsername:        theme.Padding() * 2,
		verticalPaddingAboveMessage:         theme.Padding() * 2,
		verticalPaddingAboveTimestamp:       theme.Padding() * 2,
		verticalPaddingAboveBackgroundEnd:   theme.Padding() * 2,
		verticalPaddingAboveEnd:             theme.Padding(),
		horizontalPaddingSideOfBackground:   theme.Padding() * 2,
		horizontalPaddingSideOfText:         theme.Padding() * 2,
		horizontalPaddingSideOfIcon:         theme.Padding() * 2,
		horizontalPaddingMinimumOnOtherSize: theme.Padding() * 7,
	}

	//if bubble.icon != nil {
	//	renderer.objects = append(renderer.objects, bubble.icon)
	//}

	return renderer
}

type bubbleRenderer struct {
	message     *widget.Label
	timestamp   *canvas.Text
	icon        fyne.CanvasObject
	longestLine string
	background  *canvas.Rectangle
	bubble      *chatBubble
	objects     []fyne.CanvasObject
	// Easier to read the Layout math by using these variable names
	verticalPaddingAboveBackground      float32
	verticalPaddingAboveUsername        float32
	verticalPaddingAboveMessage         float32
	verticalPaddingAboveTimestamp       float32
	verticalPaddingAboveBackgroundEnd   float32
	verticalPaddingAboveEnd             float32
	horizontalPaddingSideOfBackground   float32
	horizontalPaddingSideOfText         float32
	horizontalPaddingSideOfIcon         float32
	horizontalPaddingMinimumOnOtherSize float32
}

func (renderer *bubbleRenderer) Layout(size fyne.Size) {
	usernameSize := renderer.bubble.username.MinSize()
	timestampSize := renderer.timestamp.MinSize()

	allVerticalPadding := renderer.verticalPaddingAboveBackground +
		renderer.verticalPaddingAboveMessage +
		renderer.verticalPaddingAboveTimestamp +
		renderer.verticalPaddingAboveBackgroundEnd +
		renderer.verticalPaddingAboveEnd
	if !renderer.bubble.outgoing && !renderer.bubble.direct {
		allVerticalPadding += renderer.verticalPaddingAboveUsername
	}

	// We multiply the things that are on both sides by 2
	allHorizontalPadding := renderer.horizontalPaddingSideOfBackground*2 +
		renderer.horizontalPaddingSideOfText*2 +
		renderer.horizontalPaddingMinimumOnOtherSize

	// Resize the message label into how big of space we're actually going to give it.  That's the total
	// size that we're being asked to layout in, minus the space in our widget used for padding and other
	// items like the username and timestamp text
	renderer.message.Resize(
		size.Subtract(fyne.Size{
			Width:  allHorizontalPadding,
			Height: allVerticalPadding + usernameSize.Height + timestampSize.Height,
		}),
	)
	messageSize := renderer.message.MinSize()

	//
	// Now we take some measurments for how much of the available space we want to use for the bubble vs how much
	// we need to reserve for space on the non-justified side, and calculate offsets we're going to use later
	//

	// Our xOffset is far how to scoot everything to the right when we're right justified, or how much we're going to scoot things right to fit an icon when we're left justified
	xOffset := renderer.horizontalPaddingMinimumOnOtherSize
	// Creating this variable just so things below are a little more readable
	availableWidth := size.Width
	// We need to figure out which text in the bubble is going to be the widest so we can size the colored background accordingly
	// We do this by measuring all the text we're going to print in the bubble, and comparing them and their padding
	takenTimestampWidth := fyne.MeasureText(renderer.timestamp.Text, renderer.timestamp.TextSize, renderer.timestamp.TextStyle).Width + renderer.horizontalPaddingSideOfText*2
	takenUsernameWidth := fyne.MeasureText(renderer.bubble.username.Text, renderer.bubble.username.TextSize, renderer.bubble.username.TextStyle).Width + renderer.horizontalPaddingSideOfText*2
	// For multi-line messages, we measure the longest line.  Note: this measurment doesn't account for line wrapping, so we
	// will have to detect when it's going to wrap and make some additional adjustments based on that.
	takenMessageWidth := fyne.MeasureText(renderer.longestLine, theme.TextSize(), renderer.message.TextStyle).Width + renderer.horizontalPaddingSideOfText*2 // Can't get the TextSize of a label, use theme.TextSize()

	// We start by assuming the timestamp is the widest text in the bubble, then replace that definition if anything else is wider.
	// It is importatnt to remember in that this variable includes padding built in
	maximumTextWidth := takenTimestampWidth
	if takenUsernameWidth > maximumTextWidth {
		maximumTextWidth = takenUsernameWidth
	}
	if takenMessageWidth > maximumTextWidth {
		maximumTextWidth = takenMessageWidth
	}

	//
	// Now, if we're not going to wrap the text then we can expand the xOffset to fill in the remaining space.
	// We determine this by checking if the maximum text width is going to be larger than what we have available.
	// If not, we increase the xOffset to make up the difference
	//
	if maximumTextWidth+allHorizontalPadding < availableWidth {
		// We're not going to wrap, we can use up all the space that isn't the text with padding
		xOffset = availableWidth - maximumTextWidth
	}
	// Lastly, there's going to be padding on the justified side of the bubble.  Reduce the offset for that.
	xOffset -= renderer.horizontalPaddingSideOfBackground * 4

	//
	// For incoming messages that contain an icon, we need to scoot the whole bubble out a bit so that we can place an icon on the side
	//
	if !renderer.bubble.outgoing {
		// Outgoing messages are right justified, so we undo the offset calculation, but we scoot out a bit if we need to fit an icon in
		xOffset = 0
		if renderer.icon != nil {
			xOffset += theme.IconInlineSize() + renderer.horizontalPaddingSideOfIcon
		}
	}

	//
	// Now, make the correct size bubble and place all the objects where they belong, adding the xOffset where needed for right justification
	//

	// Determine the size of the chat bubble
	backgroundHeight := renderer.verticalPaddingAboveMessage +
		messageSize.Height +
		renderer.verticalPaddingAboveTimestamp +
		timestampSize.Height +
		renderer.verticalPaddingAboveBackgroundEnd

	if !renderer.bubble.outgoing && !renderer.bubble.direct {
		backgroundHeight += renderer.verticalPaddingAboveUsername + usernameSize.Height
	}

	renderer.background.Resize(fyne.Size{
		Height: backgroundHeight,
		Width:  maximumTextWidth + renderer.horizontalPaddingSideOfBackground*2,
	}) // TODO: look into using a subtractive size like the oginal message sizer

	// Put the username on the top of the bubble
	usernameX := renderer.horizontalPaddingSideOfBackground + renderer.horizontalPaddingSideOfText
	renderer.bubble.username.Move(fyne.Position{
		X: xOffset + usernameX,
		Y: renderer.verticalPaddingAboveBackground + renderer.verticalPaddingAboveUsername,
	})

	// Put the message underneith the username
	messageX := xOffset +
		renderer.horizontalPaddingSideOfBackground +
		renderer.horizontalPaddingSideOfText -
		theme.Padding() // subtract theme.Padding() to compensate for widget.Label's built-in padding, TODO: remove if/when using canvas.Text
	messageY := renderer.verticalPaddingAboveBackground +
		renderer.verticalPaddingAboveMessage

	if !renderer.bubble.outgoing && !renderer.bubble.direct {
		messageY += renderer.verticalPaddingAboveUsername + usernameSize.Height
	}
	renderer.message.Move(fyne.Position{
		X: messageX,
		Y: messageY,
	})

	// Put the timestamp at the bottom left or bottom right depending on justification, half way less indented than the message
	timestampX := float32(0)
	if renderer.bubble.outgoing {
		timestampX = availableWidth - renderer.horizontalPaddingSideOfBackground - renderer.horizontalPaddingSideOfText/2 - timestampSize.Width
	} else {
		timestampX = xOffset + renderer.horizontalPaddingSideOfBackground + renderer.horizontalPaddingSideOfText/2
	}
	timestampY := renderer.verticalPaddingAboveBackground +
		renderer.verticalPaddingAboveMessage +
		messageSize.Height +
		renderer.verticalPaddingAboveTimestamp
	if !renderer.bubble.outgoing && !renderer.bubble.direct {
		timestampY += renderer.verticalPaddingAboveUsername +
			usernameSize.Height
	}
	renderer.timestamp.Move(fyne.Position{
		X: timestampX,
		Y: timestampY,
	})

	// If an icon was supplied, place it at the bottom left of the message bubble
	if renderer.icon != nil {
		iconY := renderer.verticalPaddingAboveMessage +
			messageSize.Height +
			renderer.verticalPaddingAboveTimestamp +
			timestampSize.Height +
			renderer.verticalPaddingAboveBackgroundEnd -
			theme.IconInlineSize()

		if !renderer.bubble.outgoing && !renderer.bubble.direct {
			iconY += renderer.verticalPaddingAboveUsername +
				usernameSize.Height
		}

		renderer.icon.Move(fyne.Position{
			X: renderer.horizontalPaddingSideOfIcon,
			Y: iconY,
		})
	}

	// Place the background
	bubbleX := renderer.horizontalPaddingSideOfBackground
	renderer.background.Move(fyne.Position{
		X: xOffset + bubbleX,
		Y: renderer.verticalPaddingAboveBackground,
	})
}

func (renderer *bubbleRenderer) MinSize() (size fyne.Size) {
	usernameSize := renderer.bubble.username.MinSize()
	messageSize := renderer.message.MinSize()
	timestampSize := renderer.timestamp.MinSize()

	allVerticalPadding := renderer.verticalPaddingAboveBackground +
		renderer.verticalPaddingAboveMessage +
		renderer.verticalPaddingAboveTimestamp +
		renderer.verticalPaddingAboveBackgroundEnd +
		renderer.verticalPaddingAboveEnd

	if !renderer.bubble.outgoing && !renderer.bubble.direct {
		allVerticalPadding += renderer.verticalPaddingAboveUsername
	}

	minSize := fyne.Size{
		Width:  renderer.horizontalPaddingSideOfBackground*2 + renderer.horizontalPaddingSideOfText*2 + messageSize.Width,
		Height: allVerticalPadding + messageSize.Height + timestampSize.Height,
	}

	if !renderer.bubble.outgoing && !renderer.bubble.direct {
		minSize.Height += usernameSize.Height
	}

	return minSize
}

func (renderer *bubbleRenderer) Refresh() {
	//renderer.username.Text = renderer.bubble.username
	renderer.message = renderer.bubble.message
	renderer.longestLine = longestLine(renderer.message.Text)
	//r.updateIconAndText()
	//r.applyTheme() // TODO: got these from the button example, do I need to build equivalents?
	//r.background.Refresh()
	renderer.Layout(renderer.bubble.Size())
	//canvas.Refresh(renderer.bubble.super())
}

func (renderer *bubbleRenderer) Destroy() {
	// TODO: do I need to do something here?
}

func (renderer *bubbleRenderer) Objects() []fyne.CanvasObject {
	return renderer.objects
}

func (renderer *bubbleRenderer) padding() float32 {
	return theme.Padding() * 2
}

func longestLine(message string) string {
	lines := strings.Split(message, "\n") // TODO: is '\n' too OS specific?  Maybe do this https://stackoverflow.com/a/49963413
	longest := ""
	for _, line := range lines {
		if len(line) > len(longest) {
			longest = line
		}
	}
	return longest
}
