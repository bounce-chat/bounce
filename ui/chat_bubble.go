package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type chatBubble struct {
	widget.BaseWidget
	//objects   []fyne.CanvasObject
	id        string
	createdAt int64
	username  string // TODO: needs to update when user changes name.  user.name should be a binding.String
	message   string
}

func newChatBubble(username, message string) *chatBubble { // TODO: take a chat.Message?  need to be able to get display name that way
	bubble := &chatBubble{
		username: username,
		message:  message,
	}

	bubble.ExtendBaseWidget(bubble)
	return bubble
}

// CreateRenderer is a private method to Fyne which links this widget to its renderer
func (bubble *chatBubble) CreateRenderer() fyne.WidgetRenderer {
	bubble.ExtendBaseWidget(bubble)
	usernameText := canvas.NewText(bubble.username, theme.ForegroundColor())
	usernameText.TextStyle.Bold = true

	//background := canvas.NewRectangle(theme.ButtonColor()) // TODO: bubble color comes from if it's incoming or outgoing
	background := &canvas.Rectangle{FillColor: color.NRGBA{0, 0x23, 0x75, 0xff}}
	//tapBG := canvas.NewRectangle(color.Transparent)
	//b.tapAnim = newButtonTapAnimation(tapBG, b)
	//b.tapAnim.Curve = fyne.AnimationEaseOut
	//objects := []fyne.CanvasObject{
	//	background,
	//tapBG,
	//	usernameText,
	//}
	//shadowLevel := widget.ButtonLevel
	//if b.Importance == LowImportance {
	//	shadowLevel = widget.BaseLevel
	//}
	renderer := &bubbleRenderer{
		//ShadowingRenderer: widget.NewShadowingRenderer(objects, shadowLevel),
		background: background,
		//tapBG:             tapBG,
		bubble:   bubble,
		username: usernameText,
		layout:   layout.NewHBoxLayout(), // TODO: hmmmm
	}
	//renderer.updateIconAndText()
	//renderer.applyTheme()
	return renderer
}

type bubbleRenderer struct {
	//*widget.ShadowingRenderer // TODO: I want this?

	username   *canvas.Text
	background *canvas.Rectangle
	bubble     *chatBubble
	layout     fyne.Layout
}

func (renderer *bubbleRenderer) Layout(size fyne.Size) {
	var inset fyne.Position
	bgSize := size
	//if r.button.Importance != LowImportance {
	inset = fyne.NewPos(theme.Padding()/2, theme.Padding()/2)
	bgSize = size.Subtract(fyne.NewSize(theme.Padding(), theme.Padding()))
	//}
	//r.LayoutShadow(bgSize, inset)

	renderer.background.Move(inset)
	renderer.background.Resize(bgSize)

	//iconSize := fyne.NewSize(theme.IconInlineSize(), theme.IconInlineSize())
	usernameSize := renderer.username.MinSize()
	padding := renderer.padding()
	renderer.username.Move(fyne.Position{
		X: (padding.Width / 2),
		Y: (size.Height - usernameSize.Height) / 2,
	}) //alignedPosition(renderer.button.Alignment, padding, usernameSize, size))
	renderer.username.Resize(usernameSize)
}

func (renderer *bubbleRenderer) MinSize() (size fyne.Size) {
	usernameSize := renderer.username.MinSize()
	size.Width = usernameSize.Width
	size.Height = usernameSize.Height
	size = size.Add(renderer.padding())
	return
}

func (renderer *bubbleRenderer) Refresh() {
	renderer.username.Text = renderer.bubble.username
	//r.updateIconAndText()
	//r.applyTheme()
	//r.background.Refresh()
	renderer.Layout(renderer.bubble.Size())
	//canvas.Refresh(renderer.bubble.super())
}

func (renderer *bubbleRenderer) Destroy() {
}

func (renderer *bubbleRenderer) Objects() []fyne.CanvasObject {
	//return renderer.bubble.objects
	return []fyne.CanvasObject{
		renderer.background,
		renderer.username,
	}
}

func (renderer *bubbleRenderer) padding() fyne.Size {
	return fyne.NewSize(theme.Padding()*6, theme.Padding()*4)
}
