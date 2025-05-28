package ui

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"math"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

var colorCache = map[uuid.UUID]color.RGBA{}
var colorCacheMutex sync.Mutex

func newDefaultImage(id uuid.UUID, text binding.String, size float32, fileGetter func(uuid.UUID) ([]byte, error), clicked func()) *defaultImage {
	str, err := text.Get()
	if err != nil {
		log.Fatal("data bindings are broken")
	}

	di := &defaultImage{
		id:   id,
		size: size,
		foregroundText: &canvas.Text{
			Text:     str,
			TextSize: size / 2,
			Color:    color.RGBA{0xff, 0xff, 0xff, 0xff},
		},
		backgroundColor: canvas.NewImageFromImage(makeCircle(&colorRectangle{
			rect:  image.Rect(0, 0, int(size)*8, int(size)*8),
			color: uuidToColor(id),
		})),
		fileGetter: fileGetter,
		clicked:    clicked,
		imageCache: make(map[uuid.UUID]*canvas.Image),
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

	di.ExtendBaseWidget(di)
	return di
}

type defaultImage struct {
	widget.BaseWidget
	id              uuid.UUID
	size            float32
	foregroundText  *canvas.Text
	backgroundColor *canvas.Image
	images          []uuid.UUID
	fileGetter      func(uuid.UUID) ([]byte, error)
	clicked         func()
	imageCache      map[uuid.UUID]*canvas.Image
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

func (dir *defaultImageRenderer) Refresh() {
	for i := len(dir.di.images) - 1; i >= 0; i-- {
		id := dir.di.images[i]

		if cachedImage, ok := dir.di.imageCache[id]; ok {
			dir.di.backgroundColor = cachedImage
			break
		}

		originalImage, err := dir.di.fileGetter(id)
		if err != nil {
			log.WithFields(log.Fields{
				"file_id": id,
				"error":   err.Error(),
			}).Debug("error loading image")
			continue
		}
		if len(originalImage) == 0 {
			log.WithFields(log.Fields{
				"file_id": id,
			}).Warn("image file has no content")
			continue
		}

		goImg, _, err := image.Decode(bytes.NewReader(originalImage))
		if err != nil {
			log.WithFields(log.Fields{
				"error":   err.Error(),
				"file_id": id,
			}).Warn("error decoding image")
			continue
		}
		dir.di.backgroundColor = canvas.NewImageFromImage(makeCircle(goImg))
		dir.di.imageCache[id] = dir.di.backgroundColor
		dir.di.foregroundText.Hide()
		break
	}

	if len(dir.di.images) == 0 {
		dir.di.foregroundText.Show()
		dir.di.backgroundColor = canvas.NewImageFromImage(makeCircle(&colorRectangle{
			rect:  image.Rect(0, 0, int(dir.di.size)*8, int(dir.di.size)*8),
			color: uuidToColor(dir.di.id),
		}))
	}

	for _, obj := range dir.Objects() {
		obj.Refresh()
	}
}

//
// A color rectange is a single color in a rectangle image
//

type colorRectangle struct {
	rect  image.Rectangle
	color color.Color
}

func (cr *colorRectangle) ColorModel() color.Model {
	return color.RGBAModel /// TODO: match color?
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
	colorCacheMutex.Lock()
	defer colorCacheMutex.Unlock()

	c, ok := colorCache[id]
	if ok {
		return c
	}

	h := float64(binary.BigEndian.Uint16(id[0:2]) % 360)
	s := (float64(id[3]%10 + 65)) / 100
	v := 0.8
	r, g, b := hsvToRGB(h, s, v)

	c = color.RGBA{
		R: r,
		G: g,
		B: b,
		A: 0xff,
	}
	colorCache[id] = c

	return c
}

func hsvToRGB(h, s, v float64) (uint8, uint8, uint8) {
	c := v * s
	x := c * (1 - math.Abs(math.Mod(float64(h/60), 2)-1))
	m := v - c

	rPrime := float64(0)
	gPrime := float64(0)
	bPrime := float64(0)
	if h < 60 {
		rPrime = c
		gPrime = x
	} else if h < 120 {
		rPrime = x
		gPrime = c
	} else if h < 180 {
		gPrime = c
		bPrime = x
	} else if h < 240 {
		gPrime = x
		bPrime = c
	} else if h < 300 {
		rPrime = x
		bPrime = c
	} else {
		rPrime = c
		bPrime = x
	}

	r := (rPrime + m) * 255
	g := (gPrime + m) * 255
	b := (bPrime + m) * 255

	return uint8(r), uint8(g), uint8(b)
}
