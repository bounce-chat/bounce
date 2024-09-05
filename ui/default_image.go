package ui

import (
	"image"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

func newDefaultImage(id uuid.UUID, text binding.String, size float32, clicked func()) *defaultImage {
	str, err := text.Get()
	if err != nil {
		log.Fatal("data bindings are broken")
	}

	di := &defaultImage{
		size: size,
		foregroundText: &canvas.Text{
			Text:     str,
			TextSize: size / 2,
		},
		backgroundColor: canvas.NewImageFromImage(makeCircle(&colorRectangle{
			rect:  image.Rect(0, 0, int(size)*8, int(size)*8),
			color: uuidToColor(id),
		})),
		clicked: clicked,
	}

	text.AddListener(binding.NewDataListener(func() {
		str, err := text.Get()
		if err != nil {
			log.Fatal("data bindings are broken")
		}
		di.foregroundText.Text = str
		di.foregroundText.Refresh()
		di.Refresh()
	}))

	if fyne.CurrentApp().Settings().ThemeVariant() == theme.VariantLight {
		di.foregroundText.Color = color.RGBA{0xff, 0xff, 0xff, 0xff}
	}

	di.ExtendBaseWidget(di)
	return di
}

type defaultImage struct {
	widget.BaseWidget
	size            float32
	foregroundText  *canvas.Text
	backgroundColor *canvas.Image
	clicked         func()
}

func (di *defaultImage) Tapped(*fyne.PointEvent) {
	if di.clicked != nil {
		di.clicked()
	}
}

func (di *defaultImage) CreateRenderer() fyne.WidgetRenderer {
	di.ExtendBaseWidget(di)

	dir := &defaultImageRenderer{
		di: di,
	}

	return dir
}

type defaultImageRenderer struct {
	di *defaultImage
}

func (dir *defaultImageRenderer) Destroy() {}

func (dir *defaultImageRenderer) Layout(size fyne.Size) {
	textSize := dir.di.foregroundText.MinSize()

	leftoverWidth := dir.di.size - textSize.Width
	leftoverHeight := dir.di.size - textSize.Height

	dir.di.foregroundText.Move(fyne.Position{
		X: leftoverWidth / 2,
		Y: leftoverHeight / 2,
	})

	dir.di.backgroundColor.Resize(fyne.Size{Width: dir.di.size, Height: dir.di.size})
}

func (dir *defaultImageRenderer) MinSize() fyne.Size {
	return fyne.Size{Width: dir.di.size, Height: dir.di.size}
}

func (dir *defaultImageRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{
		dir.di.backgroundColor,
		dir.di.foregroundText,
	}
}

func (dir *defaultImageRenderer) Refresh() {}

//
// A color rectange is a single color in a rectangle image
//

type colorRectangle struct {
	rect  image.Rectangle
	color color.RGBA
}

func (cr *colorRectangle) ColorModel() color.Model {
	return color.RGBAModel
}

func (cr *colorRectangle) Bounds() image.Rectangle {
	return cr.rect
}

func (cr *colorRectangle) At(x, y int) color.Color {
	return cr.color
}

//
// Deterministically generate a color from a UUID
//
func uuidToColor(id uuid.UUID) color.RGBA {
	return color.RGBA{
		R: id[0] % 0xcc,
		G: id[1] % 0xcc,
		B: id[2] % 0xcc,
		A: 0xff,
	}
}
