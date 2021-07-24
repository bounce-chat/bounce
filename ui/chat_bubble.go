package ui

import (
	"image/color"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	log "github.com/sirupsen/logrus"
)

type chatBubble struct {
	widget.BaseWidget
	id        string
	timestamp int64
	username  string // TODO: needs to update when user changes name.  user.name should be a binding.String and should add a listener
	message   *widget.Label
	icon      *widget.Button
	outgoing  bool
}

func newChatBubble(username, message string, outgoing bool, timestamp int64, icon *widget.Button) *chatBubble {
	// TODO: I'm just using a widget.Label because it comes with wrapping out of the box, and all the wrapping code
	// isn't exported and non-trivial to copy out.  A canvas.Text would probably be better as I would have more
	// control over the presentation of the font and could hopefully enable click and drag selection for copying
	// text, but how to do this remains an open question.  Also, using a label comes with padding out of the box
	// that I don't want.
	messageLabel := widget.NewLabel(message)
	messageLabel.Wrapping = fyne.TextWrapWord

	// Make sure this button has an icon and does not have text
	if icon != nil {
		if icon.Text != "" {
			log.Fatal("cannot create a chatBubble with a profile button containing text")
		}
		if icon.Icon == nil {
			log.Fatal("chatBubble with a profile button must contain an icon")
		}
	}

	bubble := &chatBubble{
		username:  username,
		message:   messageLabel,
		icon:      icon,
		outgoing:  outgoing,
		timestamp: timestamp,
	}

	bubble.ExtendBaseWidget(bubble)
	return bubble
}

func (bubble *chatBubble) CreateRenderer() fyne.WidgetRenderer {
	bubble.ExtendBaseWidget(bubble)
	usernameText := canvas.NewText(bubble.username, theme.ForegroundColor()) // TODO: users should have uniqie colors derived from ID
	usernameText.TextStyle.Bold = true

	timestampText := canvas.NewText(time.Unix(bubble.timestamp, 0).Format("1/2 15:04"), theme.ForegroundColor())
	timestampText.TextSize = theme.TextSize() * 0.6

	// TODO: round the corners when possible: https://github.com/fyne-io/fyne/issues/1090
	// Incoming messages have a grey background and justify to the left
	background := &canvas.Rectangle{FillColor: color.NRGBA{0x20, 0x20, 0x20, 0xff}}
	if bubble.outgoing {
		// Sent messages have a blue background and justify to the right
		background = &canvas.Rectangle{FillColor: color.NRGBA{0, 0x23, 0x75, 0xff}}
	}

	renderer := &bubbleRenderer{
		background:  background,
		bubble:      bubble,
		username:    usernameText,
		message:     bubble.message,
		icon:        bubble.icon,
		timestamp:   timestampText,
		longestLine: longestLine(bubble.message.Text), // We have to know the longest line for width calculation, more on that later
		objects: []fyne.CanvasObject{
			background,
			usernameText,
			bubble.message,
			timestampText,
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

	if bubble.icon != nil {
		renderer.objects = append(renderer.objects, bubble.icon)
	}

	return renderer
}

type bubbleRenderer struct {
	username    *canvas.Text
	message     *widget.Label
	timestamp   *canvas.Text
	icon        *widget.Button
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
	usernameSize := renderer.username.MinSize()
	timestampSize := renderer.timestamp.MinSize()

	allVerticalPadding := renderer.verticalPaddingAboveBackground +
		renderer.verticalPaddingAboveUsername +
		renderer.verticalPaddingAboveMessage +
		renderer.verticalPaddingAboveTimestamp +
		renderer.verticalPaddingAboveBackgroundEnd +
		renderer.verticalPaddingAboveEnd

	// We multiply the things that are on both sides by 2
	allHorizontalPadding := renderer.horizontalPaddingSideOfBackground*2 +
		renderer.horizontalPaddingSideOfText*2 +
		//renderer.horizontalPaddingSideOfIcon*2 +
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
	takenUsernameWidth := fyne.MeasureText(renderer.username.Text, renderer.username.TextSize, renderer.username.TextStyle).Width + renderer.horizontalPaddingSideOfText*2
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
	xOffset -= renderer.horizontalPaddingSideOfBackground * 2

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
	backgroundHeight := renderer.verticalPaddingAboveUsername +
		usernameSize.Height +
		renderer.verticalPaddingAboveMessage +
		messageSize.Height +
		renderer.verticalPaddingAboveTimestamp +
		timestampSize.Height +
		renderer.verticalPaddingAboveBackgroundEnd
	renderer.background.Resize(fyne.Size{
		Height: backgroundHeight,
		Width:  maximumTextWidth + renderer.horizontalPaddingSideOfBackground*2,
	})

	// Put the username on the top of the bubble
	usernameX := renderer.horizontalPaddingSideOfBackground + renderer.horizontalPaddingSideOfText
	renderer.username.Move(fyne.Position{
		X: xOffset + usernameX,
		Y: renderer.verticalPaddingAboveBackground + renderer.verticalPaddingAboveUsername,
	})

	// Put the message underneith the username
	messageX := xOffset +
		renderer.horizontalPaddingSideOfBackground +
		renderer.horizontalPaddingSideOfText -
		theme.Padding() // subtract theme.Padding() to compensate for widget.Label's built-in padding, TODO: remove if/when using canvas.Text
	messageY := renderer.verticalPaddingAboveBackground +
		renderer.verticalPaddingAboveUsername +
		usernameSize.Height +
		renderer.verticalPaddingAboveMessage
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
		renderer.verticalPaddingAboveUsername +
		usernameSize.Height +
		renderer.verticalPaddingAboveMessage +
		messageSize.Height +
		renderer.verticalPaddingAboveTimestamp
	renderer.timestamp.Move(fyne.Position{
		X: timestampX,
		Y: timestampY,
	})

	// If an icon was supplied, place it at the bottom left of the message bubble
	if renderer.icon != nil {
		iconY := renderer.verticalPaddingAboveUsername +
			usernameSize.Height +
			renderer.verticalPaddingAboveMessage +
			messageSize.Height +
			renderer.verticalPaddingAboveTimestamp +
			timestampSize.Height +
			renderer.verticalPaddingAboveBackgroundEnd -
			theme.IconInlineSize()/2
		renderer.icon.Move(fyne.Position{
			X: theme.IconInlineSize()/2 + renderer.horizontalPaddingSideOfIcon, // For some reason renderer.icon.Size() is 0 so I can't use that, but I know buttons use theme.IconInlineSize()
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
	usernameSize := renderer.username.MinSize()
	messageSize := renderer.message.MinSize()
	timestampSize := renderer.timestamp.MinSize()

	allVerticalPadding := renderer.verticalPaddingAboveBackground +
		renderer.verticalPaddingAboveUsername +
		renderer.verticalPaddingAboveMessage +
		renderer.verticalPaddingAboveTimestamp +
		renderer.verticalPaddingAboveBackgroundEnd +
		renderer.verticalPaddingAboveEnd

	return fyne.Size{
		Width:  renderer.horizontalPaddingSideOfBackground*2 + renderer.horizontalPaddingSideOfText*2 + messageSize.Width,
		Height: allVerticalPadding + usernameSize.Height + messageSize.Height + timestampSize.Height,
	}
}

func (renderer *bubbleRenderer) Refresh() {
	renderer.username.Text = renderer.bubble.username
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
