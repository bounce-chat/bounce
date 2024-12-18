package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type clickableImage struct {
	widget.BaseWidget
	image        *canvas.Image
	text         *canvas.Text
	animateHover bool
	hovered      bool
	background   *canvas.Rectangle
	clicked      func()
}

func newClickableImage(text string, resource fyne.Resource, width, height float32, animateHover bool, onClicked func()) *clickableImage {
	img := canvas.NewImageFromResource(resource)
	img.FillMode = canvas.ImageFillContain
	img.SetMinSize(fyne.NewSize(width, height))

	ci := &clickableImage{
		image: img,
		text: &canvas.Text{
			Alignment: fyne.TextAlignCenter,
			Text:      text,
			TextSize:  theme.TextHeadingSize(),
		},
		animateHover: animateHover,
		clicked:      onClicked,
	}

	if text == "" {
		ci.text.Hide()
	}

	ci.background = canvas.NewRectangle(color.Transparent)
	ci.background.CornerRadius = theme.InputRadiusSize()

	ci.ExtendBaseWidget(ci)

	return ci
}

func (ci *clickableImage) Tapped(*fyne.PointEvent) {
	if ci.clicked != nil {
		ci.clicked()
	}
}

func (ci *clickableImage) MouseIn(*desktop.MouseEvent) {
	if !ci.animateHover {
		return
	}
	ci.hovered = true

	ci.applyTheme()
}

func (ci *clickableImage) MouseMoved(*desktop.MouseEvent) {
}

func (ci *clickableImage) MouseOut() {
	if !ci.animateHover {
		return
	}
	ci.hovered = false

	ci.applyTheme()
}

func (ci *clickableImage) applyTheme() {
	ci.background.FillColor = ci.buttonColor()
	ci.background.CornerRadius = theme.InputRadiusSize()
	ci.background.Refresh()
	ci.Refresh()
}

func (ci *clickableImage) buttonColor() color.Color {
	if ci.hovered {
		bg := theme.ButtonColor()
		return blendColor(bg, theme.HoverColor())
	}
	return color.Transparent
}

func (ci *clickableImage) CreateRenderer() fyne.WidgetRenderer {
	ci.ExtendBaseWidget(ci)

	cir := &clickableImageRenderer{
		ci: ci,
	}

	return cir
}

type clickableImageRenderer struct {
	ci *clickableImage
}

func (cir *clickableImageRenderer) Destroy() {}

func (cir *clickableImageRenderer) Layout(size fyne.Size) {
	cir.ci.background.Resize(size)
	cir.ci.image.Resize(cir.ci.image.MinSize())

	y := size.Height/2 - cir.ci.image.MinSize().Height/2

	if cir.ci.text.Visible() {
		y -= cir.ci.text.MinSize().Height / 2
	}
	cir.ci.image.Move(fyne.Position{
		X: size.Width/2 - cir.ci.image.MinSize().Width/2,
		Y: y,
	})

	if cir.ci.text.Visible() {
		cir.ci.text.Move(fyne.Position{
			X: size.Width / 2,
			Y: cir.ci.image.MinSize().Height + (size.Height-cir.ci.image.MinSize().Height)/2 + theme.Padding() - cir.ci.text.MinSize().Height/2,
		})
	}
}

func (cir *clickableImageRenderer) MinSize() fyne.Size {
	minWidth := cir.ci.image.MinSize().Width
	textWidth := cir.ci.image.MinSize().Width
	if textWidth > minWidth {
		minWidth = textWidth
	}

	height := theme.Padding() + cir.ci.image.MinSize().Height + theme.Padding()
	if cir.ci.text.Visible() {
		height += theme.Padding() + cir.ci.text.MinSize().Height
	}

	return fyne.Size{
		Width:  minWidth,
		Height: height,
	}
}

func (cir *clickableImageRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{
		cir.ci.background,
		cir.ci.image,
		cir.ci.text,
	}
}

func (cir *clickableImageRenderer) Refresh() {}
