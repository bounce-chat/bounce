package ui

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type chatBubble struct {
	widget.BaseWidget
	id        string
	createdAt int64
	username  string        // TODO: needs to update when user changes name.  user.name should be a binding.String
	message   *widget.Label //string
}

func newChatBubble(username, message string) *chatBubble { // TODO: take a chat.Message?  need to be able to get display name that way
	messageLabel := widget.NewLabel(message)
	messageLabel.Wrapping = fyne.TextWrapWord
	bubble := &chatBubble{
		username: username,
		message:  messageLabel,
	}

	bubble.ExtendBaseWidget(bubble)
	return bubble
}

func (bubble *chatBubble) CreateRenderer() fyne.WidgetRenderer {
	bubble.ExtendBaseWidget(bubble)
	usernameText := canvas.NewText(bubble.username, theme.ForegroundColor())
	usernameText.TextStyle.Bold = true

	//messageText := canvas.NewText(bubble.message, theme.ForegroundColor())

	background := &canvas.Rectangle{FillColor: color.NRGBA{0, 0x23, 0x75, 0xff}} // TODO: bubble color comes from if it's incoming or outgoing

	renderer := &bubbleRenderer{
		//ShadowingRenderer: widget.NewShadowingRenderer(objects, shadowLevel),
		background: background,
		//tapBG:             tapBG,
		bubble:      bubble,
		username:    usernameText,
		message:     bubble.message, //Text,
		longestLine: longestLine(bubble.message.Text),
		layout:      layout.NewHBoxLayout(), // TODO: hmmmm
	}
	//renderer.updateIconAndText()
	//renderer.applyTheme()
	return renderer
}

type bubbleRenderer struct {
	username *canvas.Text
	//message    *canvas.Text
	message     *widget.Label
	longestLine string
	background  *canvas.Rectangle
	bubble      *chatBubble
	layout      fyne.Layout
}

func (renderer *bubbleRenderer) Layout(size fyne.Size) {
	//iconSize := fyne.NewSize(theme.IconInlineSize(), theme.IconInlineSize())

	usernameSize := renderer.username.MinSize()

	// Minimum buffer
	xOffset := float32(30)

	renderer.message.Resize(
		size.Subtract(fyne.Size{
			Width:  renderer.padding()*4 + xOffset,
			Height: renderer.padding() * 7,
		}),
	)
	messageSize := renderer.message.MinSize() // TODO: word wrapping works with a label.  But I don't want to use a label?

	availableWidth := size.Width
	takenSpace := fyne.MeasureText(renderer.username.Text, theme.TextSize(), renderer.bubble.message.TextStyle).Width
	takenMessageSpace := fyne.MeasureText(renderer.longestLine, theme.TextSize(), renderer.bubble.message.TextStyle).Width
	if takenMessageSpace > takenSpace {
		takenSpace = takenMessageSpace
	}

	if takenSpace < availableWidth {
		// We're not going to wrap the text
		xOffset += availableWidth - takenSpace - renderer.padding()*7
	}

	// Put the username on the top
	renderer.username.Move(fyne.Position{
		X: renderer.padding()*2 + xOffset,
		Y: renderer.padding() * 2,
	})

	// Put the message underneight it
	renderer.message.Move(fyne.Position{
		X: renderer.padding()*2 + xOffset,
		Y: (renderer.padding() * 3) + usernameSize.Height,
	})

	// Determine the size of the chat bubble
	renderer.background.Resize(fyne.Size{
		Height: usernameSize.Height + messageSize.Height + renderer.padding()*3,
		Width:  size.Width - xOffset - renderer.padding(), //*2, //+ renderer.padding()*4,
	})

	// Place the background
	renderer.background.Move(fyne.Position{
		X: renderer.padding() + xOffset,
		Y: renderer.padding(),
	})

}

func (renderer *bubbleRenderer) MinSize() (size fyne.Size) {
	usernameSize := renderer.username.MinSize()
	messageSize := renderer.message.MinSize()

	size.Width = renderer.message.MinSize().Width + renderer.padding()*4 // 100 //usernameSize.Width + messageSize.Width
	size.Height = usernameSize.Height + messageSize.Height + renderer.padding()*5
	//size = size.Add(renderer..paddingSize))
	//size = size.Add(renderer.paddingSize())
	return
}

func (renderer *bubbleRenderer) Refresh() {
	renderer.username.Text = renderer.bubble.username
	renderer.message = renderer.bubble.message
	renderer.longestLine = longestLine(renderer.message.Text)
	//r.updateIconAndText()
	//r.applyTheme()
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
	lines := strings.Split(message, "\n") // OS specific?  Maybe do this https://stackoverflow.com/a/49963413
	longest := ""
	for _, line := range lines {
		if len(line) > len(longest) {
			longest = line
		}
	}
	return longest
}
