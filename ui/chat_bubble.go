package ui

import (
	"image/color"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type chatBubble struct {
	widget.BaseWidget
	id        string
	timestamp int64
	username  string // TODO: needs to update when user changes name.  user.name should be a binding.String and should add a listener
	message   *widget.Label
	outgoing  bool
}

func newChatBubble(username, message string, outgoing bool, timestamp int64) *chatBubble {
	// TODO: I'm just using a widget.Label because it comes with wrapping out of the box, and all the wrapping code
	// isn't exported and non-trivial to copy out.  A canvas.Text would probably be better as I would have more
	// control over the presentation of the font and could hopefully enable click and drag selection for copying
	// text, but how to do this remains an open question.  Also, using a label comes with padding out of the box
	// that I don't want.
	messageLabel := widget.NewLabel(message)
	messageLabel.Wrapping = fyne.TextWrapWord

	bubble := &chatBubble{
		username:  username,
		message:   messageLabel,
		outgoing:  outgoing,
		timestamp: timestamp,
	}

	bubble.ExtendBaseWidget(bubble)
	return bubble
}

func (bubble *chatBubble) CreateRenderer() fyne.WidgetRenderer {
	bubble.ExtendBaseWidget(bubble)
	usernameText := canvas.NewText(bubble.username, theme.ForegroundColor())
	usernameText.TextStyle.Bold = true

	timestampText := canvas.NewText(time.Unix(bubble.timestamp, 0).Format("1/2 15:04"), theme.ForegroundColor())
	timestampText.TextSize = theme.TextSize() * 0.6

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
		timestamp:   timestampText,
		longestLine: longestLine(bubble.message.Text), // We have to know the longest line for width calculation, more on that later
		objects: []fyne.CanvasObject{
			background,
			usernameText,
			bubble.message,
			timestampText,
		},
		verticalPaddingAboveBackground:    theme.Padding(),
		verticalPaddingAboveUsername:      theme.Padding() * 2,
		verticalPaddingAboveMessage:       theme.Padding() * 2,
		verticalPaddingAboveMetadata:      theme.Padding() * 2,
		verticalPaddingAboveBackgroundEnd: theme.Padding() * 2,
		verticalPaddingAboveEnd:           theme.Padding(),
		horizontalPaddingSideOfBackground: theme.Padding() * 2,
		horizontalPaddingSideOfText:       theme.Padding() * 2,
		//horizontalPaddingSideOfIcon:         theme.Padding() * 2,
		horizontalPaddingMinimumOnOtherSize: theme.Padding() * 7,
	}
	return renderer
}

type bubbleRenderer struct {
	username    *canvas.Text
	message     *widget.Label
	timestamp   *canvas.Text
	longestLine string
	background  *canvas.Rectangle
	bubble      *chatBubble
	objects     []fyne.CanvasObject
	// Easier to read the Layout math by using these variable names
	verticalPaddingAboveBackground    float32
	verticalPaddingAboveUsername      float32
	verticalPaddingAboveMessage       float32
	verticalPaddingAboveMetadata      float32
	verticalPaddingAboveBackgroundEnd float32
	verticalPaddingAboveEnd           float32
	horizontalPaddingSideOfBackground float32
	horizontalPaddingSideOfText       float32
	//horizontalPaddingSideOfIcon         float32
	horizontalPaddingMinimumOnOtherSize float32
}

func (renderer *bubbleRenderer) Layout(size fyne.Size) {
	usernameSize := renderer.username.MinSize()
	metadataSize := renderer.timestamp.MinSize()

	allVerticalPadding := renderer.verticalPaddingAboveBackground +
		renderer.verticalPaddingAboveUsername +
		renderer.verticalPaddingAboveMessage +
		renderer.verticalPaddingAboveMetadata +
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
			Height: allVerticalPadding + usernameSize.Height + metadataSize.Height,
		}),
	)
	messageSize := renderer.message.MinSize()

	//
	// Now we take some measurments for how much of the available space we want to use for the bubble vs how much
	// we need to reserve for space on the non-justified side, and calculate offsets we're going to use later
	//
	xOffset := renderer.horizontalPaddingMinimumOnOtherSize
	availableWidth := size.Width
	takenUsernameSpace := fyne.MeasureText(renderer.username.Text, theme.TextSize(), renderer.bubble.message.TextStyle).Width
	maximumTextWidth := takenUsernameSpace
	takenMessageSpace := fyne.MeasureText(renderer.longestLine, theme.TextSize(), renderer.bubble.message.TextStyle).Width
	if takenMessageSpace > takenUsernameSpace {
		maximumTextWidth = takenMessageSpace
	}
	if metadataSize.Width > maximumTextWidth {
		maximumTextWidth = metadataSize.Width
	}
	// Check if the longest line in the message is going to want to be larger than the space we've been told to
	// make a layout in.  If so then we're going to wrap and we can't add additional offset.
	//if maximumTextWidth < availableWidth {
	if maximumTextWidth+allHorizontalPadding < availableWidth {
		// We're not going to wrap the text, so the offset can expand to fill remaining space
		xOffset += availableWidth - maximumTextWidth - allHorizontalPadding
		//xOffset += availableWidth - (renderer.horizontalPaddingSideOfText + maximumTextWidth + renderer.horizontalPaddingSideOfText)
		bubbleX := renderer.horizontalPaddingSideOfBackground + xOffset
		bubbleWidth := renderer.horizontalPaddingSideOfText + maximumTextWidth + renderer.horizontalPaddingSideOfText
		endOfTheBubble := bubbleX + bubbleWidth
		missingGap := availableWidth - endOfTheBubble
		xOffset += missingGap
	} else {
		// Lastly, there's going to be padding on the justified side of the bubble.  Reduce the offset for that.
		xOffset -= renderer.horizontalPaddingSideOfBackground
	}
	//xOffset -= renderer.horizontalPaddingSideOfBackground

	//
	// Now, make the correct size bubble and place all the objects where they belong, adding the xOffset where needed for right justification
	//

	// Determine the size of the chat bubble
	renderer.background.Resize(fyne.Size{
		Height: renderer.verticalPaddingAboveUsername + usernameSize.Height + renderer.verticalPaddingAboveMessage + messageSize.Height + renderer.verticalPaddingAboveMetadata + metadataSize.Height + renderer.verticalPaddingAboveBackgroundEnd,
		Width:  renderer.horizontalPaddingSideOfText + maximumTextWidth + renderer.horizontalPaddingSideOfText,
	})

	// Put the username on the top of the bubble
	usernameX := renderer.horizontalPaddingSideOfBackground + renderer.horizontalPaddingSideOfText
	if renderer.bubble.outgoing {
		usernameX += xOffset
	}
	renderer.username.Move(fyne.Position{
		X: usernameX,
		Y: renderer.verticalPaddingAboveBackground + renderer.verticalPaddingAboveUsername,
	})

	// Put the message underneith the username
	messageX := renderer.horizontalPaddingSideOfBackground + renderer.horizontalPaddingSideOfText - theme.Padding() // subtract theme.Padding() to compensate for widget.Label's built-in padding, TODO: remove when using canvas.Text
	if renderer.bubble.outgoing {
		messageX += xOffset
	}
	renderer.message.Move(fyne.Position{
		X: messageX,
		Y: renderer.verticalPaddingAboveBackground + renderer.verticalPaddingAboveUsername + usernameSize.Height + renderer.verticalPaddingAboveMessage,
	})

	// Put the timestamp at the bottom left or bottom right depending on justification
	timestampX := float32(0)
	if renderer.bubble.outgoing {
		timestampX = availableWidth - renderer.horizontalPaddingSideOfBackground - renderer.horizontalPaddingSideOfText*2 - metadataSize.Width
	} else {
		timestampX = renderer.horizontalPaddingSideOfBackground + renderer.horizontalPaddingSideOfText/2
	}
	renderer.timestamp.Move(fyne.Position{
		X: timestampX,
		Y: renderer.verticalPaddingAboveBackground + renderer.verticalPaddingAboveUsername + usernameSize.Height + renderer.verticalPaddingAboveMessage + messageSize.Height + renderer.verticalPaddingAboveMetadata,
	})

	// Place the background
	bubbleX := renderer.horizontalPaddingSideOfBackground
	if renderer.bubble.outgoing {
		bubbleX += xOffset
	}
	renderer.background.Move(fyne.Position{
		X: bubbleX,
		Y: renderer.verticalPaddingAboveBackground,
	})
}

func (renderer *bubbleRenderer) MinSize() (size fyne.Size) {
	usernameSize := renderer.username.MinSize()
	messageSize := renderer.message.MinSize()
	metadataSize := renderer.timestamp.MinSize()

	allVerticalPadding := renderer.verticalPaddingAboveBackground +
		renderer.verticalPaddingAboveUsername +
		renderer.verticalPaddingAboveMessage +
		renderer.verticalPaddingAboveMetadata +
		renderer.verticalPaddingAboveBackgroundEnd +
		renderer.verticalPaddingAboveEnd

	return fyne.Size{
		Width:  renderer.horizontalPaddingSideOfBackground*2 + renderer.horizontalPaddingSideOfText*2 + messageSize.Width,
		Height: allVerticalPadding + usernameSize.Height + messageSize.Height + metadataSize.Height,
	}
}

func (renderer *bubbleRenderer) Refresh() {
	renderer.username.Text = renderer.bubble.username
	renderer.message = renderer.bubble.message
	renderer.longestLine = longestLine(renderer.message.Text)
	//r.updateIconAndText()
	//r.applyTheme() // TODO: check if these are important to implement
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
