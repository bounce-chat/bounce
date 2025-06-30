package ui

import (
	"image"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	log "github.com/sirupsen/logrus"
)

var deviceStatusLocal = 0
var deviceStatusOffline = 1
var deviceStatusOnline = 2

func newDeviceButton(name, address string, deviceStatus int, encrypted, hosted bool, clicked func()) *deviceButton {
	// TODO: create the icon here by getting it from the user?

	displayNameSet := true
	displayName := name
	if displayName == "" {
		displayNameSet = false
		displayName = address
	}

	var statusColor color.Color
	switch deviceStatus {
	case deviceStatusLocal:
		statusColor = theme.Color(colorNameDeviceLocal)
	case deviceStatusOffline:
		statusColor = theme.Color(colorNameDeviceOffline)
	case deviceStatusOnline:
		statusColor = theme.Color(colorNameDeviceOnline)
	default:
		log.WithFields(log.Fields{
			"status": deviceStatus,
		}).Error("unsupported device status while creating device button")
	}

	size := theme.IconInlineSize()
	db := &deviceButton{
		icon: canvas.NewImageFromImage(makeCircle(&colorRectangle{
			rect:  image.Rect(0, 0, int(size)*8, int(size)*8),
			color: statusColor,
		})),
		size: size,
		name: widget.NewRichText(
			&widget.TextSegment{
				Text: displayName,
				Style: widget.RichTextStyle{
					SizeName: theme.SizeNameText,
				},
			},
		),
		clicked: clicked,
	}
	if !displayNameSet {
		db.name.Segments[0].(*widget.TextSegment).Style.TextStyle = fyne.TextStyle{
			Italic: true,
		}
	}

	db.features = &canvas.Text{
		Text:     "",
		TextSize: theme.TextSize() * 0.75,
		TextStyle: fyne.TextStyle{
			Italic: true,
		},
	}
	if deviceStatus == deviceStatusLocal {
		db.features.Text = "current device"
	} else if hosted {
		db.features.Text = "hosted"
	} else if encrypted {
		db.features.Text = "encrypted"
	}

	db.name.Truncation = fyne.TextTruncateEllipsis
	db.background = canvas.NewRectangle(color.Transparent)
	db.background.CornerRadius = theme.InputRadiusSize()

	db.ExtendBaseWidget(db)
	return db
}

type deviceButton struct {
	widget.BaseWidget
	icon       fyne.CanvasObject
	size       float32
	name       *widget.RichText
	features   *canvas.Text
	background *canvas.Rectangle
	hovered    bool
	clicked    func()
}

func (db *deviceButton) Tapped(*fyne.PointEvent) {
	if db.clicked != nil {
		db.clicked()
	}
}

func (db *deviceButton) MouseIn(*desktop.MouseEvent) {
	db.hovered = true

	db.applyTheme()
}

func (db *deviceButton) MouseMoved(*desktop.MouseEvent) {
}

func (db *deviceButton) MouseOut() {
	db.hovered = false

	db.applyTheme()
}

func (db *deviceButton) applyTheme() {
	db.background.FillColor = db.buttonColor()
	db.background.CornerRadius = theme.InputRadiusSize()
	db.background.Refresh()
	db.Refresh()
}

func (db *deviceButton) buttonColor() color.Color {
	if db.hovered {
		bg := theme.ButtonColor()
		return blendColor(bg, theme.HoverColor())
	}
	return color.Transparent
}

func (db *deviceButton) CreateRenderer() fyne.WidgetRenderer {
	db.ExtendBaseWidget(db)

	dbr := &deviceButtonRenderer{
		db: db,
		objects: []fyne.CanvasObject{
			db.background,
			db.icon,
			db.name,
			db.features,
		},
	}

	return dbr
}

type deviceButtonRenderer struct {
	db      *deviceButton
	objects []fyne.CanvasObject
}

func (dbr *deviceButtonRenderer) Destroy() {}

func (dbr *deviceButtonRenderer) Layout(size fyne.Size) {
	dbr.db.background.Resize(size)

	iconSize := fyne.Size{
		Width:  dbr.db.size,
		Height: dbr.db.size,
	}
	nameSize := dbr.db.name.MinSize()
	featuresSize := dbr.db.features.MinSize()

	dbr.db.icon.Resize(iconSize)
	dbr.db.name.Resize(fyne.Size{
		Height: size.Height,
		Width:  size.Width - theme.Padding() - iconSize.Width - featuresSize.Width - theme.Padding(),
	})

	leftoverIconHeight := size.Height - iconSize.Height
	dbr.db.icon.Move(fyne.Position{
		X: theme.Padding(),
		Y: leftoverIconHeight / 2,
	})

	leftoverNameHeight := size.Height - nameSize.Height
	dbr.db.name.Move(fyne.Position{
		X: theme.Padding() + iconSize.Width + theme.Padding(),
		Y: leftoverNameHeight / 2,
	})

	leftoverFeaturesHeight := size.Height - featuresSize.Height
	dbr.db.features.Move(fyne.Position{
		X: size.Width - theme.Padding() - featuresSize.Width,
		Y: leftoverFeaturesHeight / 2,
	})
}

func (dbr *deviceButtonRenderer) MinSize() fyne.Size {
	return fyne.Size{
		Width:  theme.Padding() + dbr.db.size + theme.Padding() + dbr.db.name.MinSize().Width + theme.Padding()*5 + dbr.db.features.MinSize().Width,
		Height: dbr.db.name.MinSize().Height,
	}
}

func (dbr *deviceButtonRenderer) Objects() []fyne.CanvasObject {
	return dbr.objects
}

func (dbr *deviceButtonRenderer) Refresh() {
	for _, obj := range dbr.Objects() {
		obj.Refresh()
	}
}
