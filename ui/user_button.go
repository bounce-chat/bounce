package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	log "github.com/sirupsen/logrus"
)

var minWidthOfLongNameInUserButton = float32(128)

func newUserButton(icon *defaultImage, name string, admin bool, clicked func()) *userButton {
	// TODO: create the icon here by getting it from the user?

	ub := &userButton{
		icon:       icon,
		nameString: name,
		name: &canvas.Text{
			Text:     name,
			TextSize: theme.TextSize(),
		},
		fullName: &canvas.Text{
			Text:     name,
			TextSize: theme.TextSize(),
		},
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

	ub.background = canvas.NewRectangle(color.Transparent)
	ub.background.CornerRadius = theme.InputRadiusSize()

	ub.ExtendBaseWidget(ub)
	return ub
}

type userButton struct {
	widget.BaseWidget
	icon       *defaultImage
	nameString string
	name       *canvas.Text
	fullName   *canvas.Text
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

// MouseIn is called when a desktop pointer enters the widget
func (ub *userButton) MouseIn(*desktop.MouseEvent) {
	ub.hovered = true

	ub.applyTheme()
}

// MouseMoved is called when a desktop pointer hovers over the widget
func (ub *userButton) MouseMoved(*desktop.MouseEvent) {
}

// MouseOut is called when a desktop pointer exits the widget
func (ub *userButton) MouseOut() {
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
	}

	return ubr
}

type userButtonRenderer struct {
	ub *userButton
}

func (ubr *userButtonRenderer) Destroy() {}

func (ubr *userButtonRenderer) Layout(size fyne.Size) {
	if ubr.MinSize().Width > size.Width {
		log.WithFields(log.Fields{
			"size":     size,
			"min_size": ubr.MinSize(),
		}).Warn("refusing to layout into size smaller than minsize")
		return
	}
	widthAvailableForName := size.Width - ubr.ub.icon.MinSize().Width - theme.Padding() - theme.Padding()*5 - ubr.ub.admin.MinSize().Width
	fullNameWidth := ubr.ub.fullName.MinSize().Width
	if fullNameWidth > widthAvailableForName {
		percentAvailable := widthAvailableForName / fullNameWidth
		numberOfCharactersThatCanFit := int(percentAvailable * float32(len(ubr.ub.nameString)))
		truncatedText := ubr.ub.nameString[0:numberOfCharactersThatCanFit]
		if len(truncatedText) > 3 {
			truncatedText = truncatedText[0:len(truncatedText)-4] + "..."
		}
		ubr.ub.name.Text = truncatedText
		ubr.ub.name.Refresh()
	} else {
		ubr.ub.name.Text = ubr.ub.nameString
		ubr.ub.name.Refresh()
	}

	ubr.ub.background.Resize(size)

	iconSize := ubr.ub.icon.MinSize()
	leftoverIconHeight := size.Height - iconSize.Height
	ubr.ub.icon.Move(fyne.Position{
		X: 0,
		Y: leftoverIconHeight / 2,
	})

	nameSize := ubr.ub.name.MinSize()
	leftoverNameHeight := size.Height - nameSize.Height
	ubr.ub.name.Move(fyne.Position{
		X: iconSize.Width + theme.Padding(),
		Y: leftoverNameHeight / 2,
	})

	adminSize := ubr.ub.admin.MinSize()
	leftoverAdminHeight := size.Height - adminSize.Height
	ubr.ub.admin.Move(fyne.Position{
		X: size.Width - theme.Padding() - adminSize.Width,
		Y: leftoverAdminHeight / 2,
	})
}

func (ubr *userButtonRenderer) MinSize() fyne.Size {
	fullNameWidth := ubr.ub.fullName.MinSize().Width
	minNameWidth := fullNameWidth
	if fullNameWidth > minWidthOfLongNameInUserButton {
		minNameWidth = minWidthOfLongNameInUserButton
	}
	return fyne.Size{
		Width:  ubr.ub.icon.MinSize().Width + theme.Padding() + minNameWidth + theme.Padding()*5 + ubr.ub.admin.MinSize().Width,
		Height: ubr.ub.icon.MinSize().Height,
	}
}

func (ubr *userButtonRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{
		ubr.ub.background,
		ubr.ub.icon,
		ubr.ub.name,
		ubr.ub.admin,
	}
}

func (ubr *userButtonRenderer) Refresh() {}
