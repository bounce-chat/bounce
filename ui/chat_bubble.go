package ui

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type chatBubble struct {
	widget.BaseWidget
	id        string
	createdAt int64
	username  string // TODO: needs to update when user changes name.  user.name should be a binding.String
	message   *widget.Label
	outgoing  bool
}

func newChatBubble(username, message string, outgoing bool) *chatBubble {
	// TODO: I'm just using a widget.Label because it comes with wrapping out of the box, and all the wrapping code
	// isn't exported and non-trivial to copy out.  A canvas.Text would probably be better as I would have more
	// control over the presentation of the font and could hopefully enable click and drag selection for copying
	// text, but how to do this remains an open question.  Also, using a label comes with padding out of the box
	// that I don't want.
	messageLabel := widget.NewLabel(message)
	messageLabel.Wrapping = fyne.TextWrapWord

	bubble := &chatBubble{
		username: username,
		message:  messageLabel,
		outgoing: outgoing,
	}

	bubble.ExtendBaseWidget(bubble)
	return bubble
}

func (bubble *chatBubble) CreateRenderer() fyne.WidgetRenderer {
	bubble.ExtendBaseWidget(bubble)
	usernameText := canvas.NewText(bubble.username, theme.ForegroundColor())
	usernameText.TextStyle.Bold = true

	// Incoming messages have a grey background and justify to the left
	background := &canvas.Rectangle{FillColor: color.NRGBA{0x20, 0x20, 0x20, 0xff}}
	if bubble.outgoing {
		// Sent messages have a blue background
		background = &canvas.Rectangle{FillColor: color.NRGBA{0, 0x23, 0x75, 0xff}}
	}

	renderer := &bubbleRenderer{
		background:                        background,
		bubble:                            bubble,
		username:                          usernameText,
		message:                           bubble.message,
		longestLine:                       longestLine(bubble.message.Text), // We have to know the longest line for width calculation, more on that later
		verticalPaddingAboveBackground:    theme.Padding() * 2,
		verticalPaddingAboveUsername:      theme.Padding() * 2,
		verticalPaddingAboveMessage:       theme.Padding() * 2,
		verticalPaddingAboveMetadata:      theme.Padding() * 2,
		verticalPaddingAboveBackgroundEnd: theme.Padding() * 2,
		verticalPaddingAboveEnd:           theme.Padding() * 2,
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
	longestLine string
	background  *canvas.Rectangle
	bubble      *chatBubble
	layout      fyne.Layout
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
	//iconSize := fyne.NewSize(theme.IconInlineSize(), theme.IconInlineSize())

	usernameSize := renderer.username.MinSize()
	//metadataSize :=

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
	// items like the username and metadata
	renderer.message.Resize(
		size.Subtract(fyne.Size{
			Width:  allHorizontalPadding,
			Height: allVerticalPadding + usernameSize.Height,
		}),
	)

	// Now let's figure out how far we're going to move the chat bubble over from the side it isn't attached to.
	// TODO: explain this better
	xOffset := renderer.horizontalPaddingMinimumOnOtherSize
	availableWidth := size.Width
	takenUsernameSpace := fyne.MeasureText(renderer.username.Text, theme.TextSize(), renderer.bubble.message.TextStyle).Width
	maximumTextWidth := takenUsernameSpace
	takenMessageSpace := fyne.MeasureText(renderer.longestLine, theme.TextSize(), renderer.bubble.message.TextStyle).Width
	if takenMessageSpace > takenUsernameSpace {
		maximumTextWidth = takenMessageSpace
	}
	// Check if the longest line in the message is going to want to be larger than the space we've been told to
	// layout in.  If so then we're going to wrap and we can't add additional offset.
	if maximumTextWidth < availableWidth {
		// We're not going to wrap the text, so the offset can expand to fill remaining space
		xOffset += availableWidth - maximumTextWidth - allHorizontalPadding
	}
	// Lastly, there's going to be padding on the justified side of the bubble.  Reduce the offset for that.
	xOffset -= renderer.horizontalPaddingSideOfBackground

	// Put the username on the top of the bubble
	renderer.username.Move(fyne.Position{
		X: xOffset + renderer.horizontalPaddingSideOfBackground + renderer.horizontalPaddingSideOfText,
		Y: renderer.verticalPaddingAboveBackground + renderer.verticalPaddingAboveUsername,
	})

	// Put the message underneith the username
	renderer.message.Move(fyne.Position{
		X: xOffset + renderer.horizontalPaddingSideOfBackground + renderer.horizontalPaddingSideOfText,
		Y: renderer.verticalPaddingAboveBackground + renderer.verticalPaddingAboveUsername + usernameSize.Height + renderer.verticalPaddingAboveMessage,
	})

	// TODO: place the timestamp

	// Determine the size of the chat bubble
	messageSize := renderer.message.MinSize()
	renderer.background.Resize(fyne.Size{
		Height: renderer.verticalPaddingAboveUsername + usernameSize.Height + renderer.verticalPaddingAboveMessage + messageSize.Height + renderer.verticalPaddingAboveBackgroundEnd, // TODO: + metadataSize.Height
		Width:  size.Width - xOffset - renderer.horizontalPaddingSideOfText + maximumTextWidth + renderer.horizontalPaddingSideOfText,
	})

	// Place the background
	renderer.background.Move(fyne.Position{
		X: xOffset + renderer.horizontalPaddingSideOfBackground,
		Y: renderer.verticalPaddingAboveBackground,
	})

}

func (renderer *bubbleRenderer) MinSize() (size fyne.Size) {
	usernameSize := renderer.username.MinSize()
	messageSize := renderer.message.MinSize()

	allVerticalPadding := renderer.verticalPaddingAboveBackground +
		renderer.verticalPaddingAboveUsername +
		renderer.verticalPaddingAboveMessage +
		renderer.verticalPaddingAboveMetadata +
		renderer.verticalPaddingAboveBackgroundEnd +
		renderer.verticalPaddingAboveEnd

	return fyne.Size{
		Width:  renderer.horizontalPaddingSideOfBackground*2 + renderer.horizontalPaddingSideOfText*2 + messageSize.Width,
		Height: allVerticalPadding + usernameSize.Height + messageSize.Height, // TODO: +metadataSize.Height
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
	// TODO: return renderer.bubble.objects?
	return []fyne.CanvasObject{
		renderer.background,
		renderer.username,
		renderer.message,
	}
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
