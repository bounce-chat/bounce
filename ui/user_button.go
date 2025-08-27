package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

func newUserButton(icon *defaultImage, name string, admin bool, clicked func()) *userButton {
	// TODO: create the icon here by getting it from the user?

	ub := &userButton{
		icon: icon,
		name: widget.NewRichText(
			&widget.TextSegment{
				Text: name,
				Style: widget.RichTextStyle{
					SizeName: theme.SizeNameText,
				},
			},
		),
		clicked: clicked,
	}
	ub.admin = &canvas.Text{
		Text:     "",
		TextSize: theme.TextSize() * 0.75,
		TextStyle: fyne.TextStyle{
			Italic: true,
		},
	}
	if admin {
		ub.admin.Text = "admin"
	}

	ub.name.Truncation = fyne.TextTruncateEllipsis
	ub.background = canvas.NewRectangle(color.Transparent)
	ub.background.CornerRadius = theme.InputRadiusSize()

	ub.ExtendBaseWidget(ub)
	return ub
}

type userButton struct {
	widget.BaseWidget
	icon       *defaultImage
	name       *widget.RichText
	admin      *canvas.Text
	background *canvas.Rectangle
	hovered    bool
	clicked    func()
}

func (ub *userButton) Tapped(*fyne.PointEvent) {
	if ub.clicked != nil {
		ub.clicked()
	}
}

func (ub *userButton) MouseIn(*desktop.MouseEvent) {
	if ub.clicked == nil {
		return
	}
	ub.hovered = true

	ub.applyTheme()
}

func (ub *userButton) MouseMoved(*desktop.MouseEvent) {
}

func (ub *userButton) MouseOut() {
	if ub.clicked == nil {
		return
	}
	ub.hovered = false

	ub.applyTheme()
}

func (ub *userButton) applyTheme() {
	ub.background.FillColor = ub.buttonColor()
	ub.background.CornerRadius = theme.InputRadiusSize()
	ub.background.Refresh()
	ub.Refresh()
}

func (ub *userButton) buttonColor() color.Color {
	if ub.hovered {
		bg := theme.ButtonColor()
		return blendColor(bg, theme.HoverColor())
	}
	return color.Transparent
}

func blendColor(under, over color.Color) color.Color {
	// This alpha blends with the over operator, and accounts for RGBA() returning alpha-premultiplied values
	dstR, dstG, dstB, dstA := under.RGBA()
	srcR, srcG, srcB, srcA := over.RGBA()

	srcAlpha := float32(srcA) / 0xFFFF
	dstAlpha := float32(dstA) / 0xFFFF

	outAlpha := srcAlpha + dstAlpha*(1-srcAlpha)
	outR := srcR + uint32(float32(dstR)*(1-srcAlpha))
	outG := srcG + uint32(float32(dstG)*(1-srcAlpha))
	outB := srcB + uint32(float32(dstB)*(1-srcAlpha))
	// We create an RGBA64 here because the color components are already alpha-premultiplied 16-bit values (they're just stored in uint32s).
	return color.RGBA64{R: uint16(outR), G: uint16(outG), B: uint16(outB), A: uint16(outAlpha * 0xFFFF)}

}

func (ub *userButton) CreateRenderer() fyne.WidgetRenderer {
	ub.ExtendBaseWidget(ub)

	ubr := &userButtonRenderer{
		ub: ub,
		objects: []fyne.CanvasObject{
			ub.background,
			ub.icon,
			ub.name,
			ub.admin,
		},
	}

	return ubr
}

type userButtonRenderer struct {
	ub      *userButton
	objects []fyne.CanvasObject
}

func (ubr *userButtonRenderer) Destroy() {}

func (ubr *userButtonRenderer) Layout(size fyne.Size) {
	ubr.ub.background.Resize(size)

	iconSize := ubr.ub.icon.MinSize()
	adminSize := ubr.ub.admin.MinSize()

	ubr.ub.name.Resize(fyne.Size{
		Height: size.Height,
		Width:  size.Width - theme.Padding() - iconSize.Width - theme.Padding() - adminSize.Width,
	})

	leftoverIconHeight := size.Height - iconSize.Height
	ubr.ub.icon.Move(fyne.Position{
		X: theme.Padding(),
		Y: leftoverIconHeight / 2,
	})

	nameSize := ubr.ub.name.MinSize()
	leftoverNameHeight := size.Height - nameSize.Height
	ubr.ub.name.Move(fyne.Position{
		X: theme.Padding() + iconSize.Width,
		Y: leftoverNameHeight / 2,
	})

	leftoverAdminHeight := size.Height - adminSize.Height
	ubr.ub.admin.Move(fyne.Position{
		X: size.Width - theme.Padding() - adminSize.Width,
		Y: leftoverAdminHeight / 2,
	})
}

func (ubr *userButtonRenderer) MinSize() fyne.Size {
	minHeight := ubr.ub.icon.MinSize().Height + theme.Padding()*2
	textHeight := ubr.ub.name.MinSize().Height
	if textHeight > minHeight {
		minHeight = textHeight
	}

	return fyne.Size{
		Width:  theme.Padding() + ubr.ub.icon.MinSize().Width + ubr.ub.name.MinSize().Width + theme.Padding()*5 + ubr.ub.admin.MinSize().Width,
		Height: minHeight,
	}
}

func (ubr *userButtonRenderer) Objects() []fyne.CanvasObject {
	return ubr.objects
}

func (ubr *userButtonRenderer) Refresh() {
	for _, obj := range ubr.Objects() {
		obj.Refresh()
	}
}
