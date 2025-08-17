package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

func newSettingsButton(icon *canvas.Image, text string, size float32, clicked func()) *settingsButton {
	icon.FillMode = canvas.ImageFillContain
	icon.Resize(fyne.NewSize(size, size))
	icon.SetMinSize(fyne.NewSize(size, size))

	sb := &settingsButton{
		icon: icon,
		text: widget.NewRichText(
			&widget.TextSegment{
				Text: text,
				Style: widget.RichTextStyle{
					SizeName: theme.SizeNameHeadingText,
				},
			},
		),
		clicked: clicked,
	}

	sb.text.Truncation = fyne.TextTruncateEllipsis
	sb.background = canvas.NewRectangle(color.Transparent)
	sb.background.CornerRadius = theme.InputRadiusSize()

	sb.ExtendBaseWidget(sb)
	return sb
}

type settingsButton struct {
	widget.BaseWidget
	icon       *canvas.Image
	text       *widget.RichText
	background *canvas.Rectangle
	hovered    bool
	clicked    func()
}

func (sb *settingsButton) Tapped(*fyne.PointEvent) {
	if sb.clicked != nil {
		sb.clicked()
	}
}

func (sb *settingsButton) MouseIn(*desktop.MouseEvent) {
	sb.hovered = true

	sb.applyTheme()
}

func (sb *settingsButton) MouseMoved(*desktop.MouseEvent) {
}

func (sb *settingsButton) MouseOut() {
	sb.hovered = false

	sb.applyTheme()
}

func (sb *settingsButton) applyTheme() {
	sb.background.FillColor = sb.buttonColor()
	sb.background.CornerRadius = theme.InputRadiusSize()
	sb.background.Refresh()
	sb.Refresh()
}

func (sb *settingsButton) buttonColor() color.Color {
	if sb.hovered {
		bg := theme.ButtonColor()
		return blendColor(bg, theme.HoverColor())
	}
	return color.Transparent
}

func (sb *settingsButton) CreateRenderer() fyne.WidgetRenderer {
	sb.ExtendBaseWidget(sb)

	sbr := &settingsButtonRenderer{
		sb: sb,
		objects: []fyne.CanvasObject{
			sb.background,
			sb.icon,
			sb.text,
		},
	}

	return sbr
}

type settingsButtonRenderer struct {
	sb      *settingsButton
	objects []fyne.CanvasObject
}

func (sbr *settingsButtonRenderer) Destroy() {}

func (sbr *settingsButtonRenderer) Layout(size fyne.Size) {
	sbr.sb.background.Resize(size)

	iconSize := sbr.sb.icon.MinSize()

	sbr.sb.text.Resize(fyne.Size{
		Height: size.Height,
		Width:  size.Width - theme.Padding() - iconSize.Width - theme.Padding(),
	})

	leftoverIconHeight := size.Height - iconSize.Height
	sbr.sb.icon.Move(fyne.Position{
		X: theme.Padding(),
		Y: leftoverIconHeight / 2,
	})

	textSize := sbr.sb.text.MinSize()
	leftoverNameHeight := size.Height - textSize.Height
	sbr.sb.text.Move(fyne.Position{
		X: theme.Padding() + iconSize.Width,
		Y: leftoverNameHeight / 2,
	})
}

func (sbr *settingsButtonRenderer) MinSize() fyne.Size {
	minHeight := sbr.sb.icon.MinSize().Height + theme.Padding()*2
	textHeight := sbr.sb.text.MinSize().Height
	if textHeight > minHeight {
		minHeight = textHeight
	}

	return fyne.Size{
		Width:  theme.Padding() + sbr.sb.icon.MinSize().Width + sbr.sb.text.MinSize().Width + theme.Padding()*4,
		Height: minHeight,
	}
}

func (sbr *settingsButtonRenderer) Objects() []fyne.CanvasObject {
	return sbr.objects
}

func (sbr *settingsButtonRenderer) Refresh() {
	for _, obj := range sbr.Objects() {
		obj.Refresh()
	}
}
