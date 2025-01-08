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

var minWidthOfLongNameInDeviceButton = float32(128)

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
		size:       size,
		nameString: displayName,
		name: &canvas.Text{
			Text:     displayName,
			TextSize: theme.TextSize(),
		},
		fullName: &canvas.Text{
			Text:     displayName,
			TextSize: theme.TextSize(),
		},
		clicked: clicked,
	}
	if !displayNameSet {
		db.name.TextStyle = fyne.TextStyle{
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

	db.background = canvas.NewRectangle(color.Transparent)
	db.background.CornerRadius = theme.InputRadiusSize()

	db.ExtendBaseWidget(db)
	return db
}

type deviceButton struct {
	widget.BaseWidget
	icon       fyne.CanvasObject
	size       float32
	nameString string
	name       *canvas.Text
	fullName   *canvas.Text
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

// MouseIn is called when a desktop pointer enters the widget
func (db *deviceButton) MouseIn(*desktop.MouseEvent) {
	db.hovered = true

	db.applyTheme()
}

// MouseMoved is called when a desktop pointer hovers over the widget
func (db *deviceButton) MouseMoved(*desktop.MouseEvent) {
}

// MouseOut is called when a desktop pointer exits the widget
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
	}

	return dbr
}

type deviceButtonRenderer struct {
	db *deviceButton
}

func (dbr *deviceButtonRenderer) Destroy() {}

func (dbr *deviceButtonRenderer) Layout(size fyne.Size) {
	if dbr.MinSize().Width > size.Width {
		log.WithFields(log.Fields{
			"size":     size,
			"min_size": dbr.MinSize(),
		}).Warn("refusing to layout into size smaller than minsize")
		return
	}
	widthAvailableForName := size.Width - dbr.db.icon.MinSize().Width - theme.Padding() - theme.Padding()*5 - dbr.db.features.MinSize().Width
	fullNameWidth := dbr.db.fullName.MinSize().Width
	if fullNameWidth > widthAvailableForName {
		percentAvailable := widthAvailableForName / fullNameWidth
		numberOfCharactersThatCanFit := int(percentAvailable * float32(len(dbr.db.nameString)))
		truncatedText := dbr.db.nameString[0:numberOfCharactersThatCanFit]
		if len(truncatedText) > 3 {
			truncatedText = truncatedText[0:len(truncatedText)-4] + "..."
		}
		dbr.db.name.Text = truncatedText
		dbr.db.name.Refresh()
	} else {
		dbr.db.name.Text = dbr.db.nameString
		dbr.db.name.Refresh()
	}

	dbr.db.background.Resize(size)

	iconSize := fyne.Size{
		Width:  dbr.db.size,
		Height: dbr.db.size,
	}
	dbr.db.icon.Resize(iconSize)

	//iconSize := dbr.db.icon.MinSize()
	leftoverIconHeight := size.Height - iconSize.Height
	dbr.db.icon.Move(fyne.Position{
		X: 0,
		Y: leftoverIconHeight / 2,
	})

	nameSize := dbr.db.name.MinSize()
	leftoverNameHeight := size.Height - nameSize.Height
	dbr.db.name.Move(fyne.Position{
		X: iconSize.Width + theme.Padding(),
		Y: leftoverNameHeight / 2,
	})

	featuresSize := dbr.db.features.MinSize()
	leftoverFeaturesHeight := size.Height - featuresSize.Height
	dbr.db.features.Move(fyne.Position{
		X: size.Width - theme.Padding() - featuresSize.Width,
		Y: leftoverFeaturesHeight / 2,
	})
}

func (dbr *deviceButtonRenderer) MinSize() fyne.Size {
	fullNameWidth := dbr.db.fullName.MinSize().Width
	minNameWidth := fullNameWidth
	if fullNameWidth > minWidthOfLongNameInDeviceButton {
		minNameWidth = minWidthOfLongNameInDeviceButton
	}
	return fyne.Size{
		Width:  dbr.db.size + theme.Padding() + minNameWidth + theme.Padding()*5 + dbr.db.features.MinSize().Width,
		Height: dbr.db.size,
	}
}

func (dbr *deviceButtonRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{
		dbr.db.background,
		dbr.db.icon,
		dbr.db.name,
		dbr.db.features,
	}
}

func (dbr *deviceButtonRenderer) Refresh() {
	for _, obj := range dbr.Objects() {
		obj.Refresh()
	}
}
