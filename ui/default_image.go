package ui

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"math"
	"strconv"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
	"github.com/bounce-chat/bounce/chat"
	log "github.com/sirupsen/logrus"
)

var colorCache = map[chat.ID]color.RGBA{}
var colorCacheMutex sync.Mutex

var imageCache = map[string]image.Image{}
var imageCacheMutex sync.Mutex

type defaultImage struct {
	widget.BaseWidget
	id                    chat.ID
	size                  float32
	foregroundText        *canvas.Text
	backgroundColor       *canvas.Image
	images                []chat.ID
	fileGetter            func(chat.ID) ([]byte, error)
	clicked               func()
	newBackgroundCallback func()
	// generation counts calls to setBackground. A background load captures the value current when it
	// started and applies its result only if it is still current: list rows reuse one defaultImage for
	// many ids, so a load that finishes after the widget has been pointed at someone else must not paint
	// the previous occupant's picture over the current one.
	generation uint64
}

func newDefaultImage(id chat.ID, images []chat.ID, str string, size float32, fileGetter func(chat.ID) ([]byte, error), clicked func()) *defaultImage {
	di := &defaultImage{
		id:              id,
		size:            size,
		backgroundColor: &canvas.Image{},
		foregroundText: &canvas.Text{
			Text:     str,
			TextSize: size / 2,
			Color:    color.RGBA{0xff, 0xff, 0xff, 0xff},
		},
		fileGetter: fileGetter,
		clicked:    clicked,
		images:     images,
	}
	di.setBackground()

	di.ExtendBaseWidget(di)
	return di
}

func (di *defaultImage) setString(str string) {
	di.foregroundText.Text = str
	di.foregroundText.Refresh()
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
		objects: []fyne.CanvasObject{
			di.backgroundColor,
			di.foregroundText,
		},
	}

	return dir
}

// setBackground shows the newest of di.images, or the coloured initial until one has been loaded. It must
// be called on the UI thread, which is what lets generation be read and written without a lock.
func (di *defaultImage) setBackground() {
	di.generation++

	// If this image should have a non-default image, try to load it from cache
	if len(di.images) > 0 {
		chosen := -1
		// Set the first cached image, if any are available
		for i := len(di.images) - 1; i >= 0; i-- {
			id := di.images[i]

			imageCacheMutex.Lock()
			cacheKey := id.String() + strconv.FormatFloat(float64(di.size), 'f', -1, 32)
			cachedImage, ok := imageCache[cacheKey]
			imageCacheMutex.Unlock()
			if ok {
				di.backgroundColor.Image = cachedImage
				di.foregroundText.Hide()
				chosen = i
				break
			}
		}

		// If we didn't set the last image, go try to get and set the first available image and cache it.
		// The load works from a snapshot of the fields it needs, since the UI thread may reassign them
		// before it finishes.
		if chosen != len(di.images)-1 {
			di.setDefaultBackground()
			images := make([]chat.ID, len(di.images))
			copy(images, di.images)
			go di.setFirstAvailableImage(di.generation, images, di.size, di.fileGetter)
		}
	} else {
		di.setDefaultBackground()
	}
}

func (di *defaultImage) setDefaultBackground() {
	// Either there are no images set, or none of them are in the cache right now.  Default
	// to a solid color with text.  Also, cache that solid color circle if needed.
	di.foregroundText.Show()

	imageCacheMutex.Lock()
	cacheKey := di.id.String() + "-default-" + strconv.FormatFloat(float64(di.size), 'f', -1, 32)
	cachedImage, ok := imageCache[cacheKey]
	imageCacheMutex.Unlock()
	if ok {
		if di.newBackgroundCallback != nil {
			di.newBackgroundCallback()
		}
		di.backgroundColor.Image = cachedImage
		return
	}
	di.backgroundColor.Image = makeCircle(&colorRectangle{
		rect:  image.Rect(0, 0, int(di.size)*8, int(di.size)*8),
		color: idToColor(di.id),
	})

	imageCacheMutex.Lock()
	imageCache[cacheKey] = di.backgroundColor.Image
	imageCacheMutex.Unlock()

	di.Refresh()
	if di.newBackgroundCallback != nil {
		di.newBackgroundCallback()
	}
}

// setFirstAvailableImage loads and caches the newest image in images that can be decoded, then shows it
// if generation is still di's current one. It runs off the UI thread and reads nothing from di until the
// fyne.Do, so it takes everything it needs as arguments rather than through the receiver.
func (di *defaultImage) setFirstAvailableImage(generation uint64, images []chat.ID, size float32, fileGetter func(chat.ID) ([]byte, error)) {
	for i := len(images) - 1; i >= 0; i-- {
		id := images[i]

		originalImage, err := fileGetter(id)
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
		croppedImage := makeCircle(goImg)
		cacheKey := id.String() + strconv.FormatFloat(float64(size), 'f', -1, 32)
		imageCacheMutex.Lock()
		imageCache[cacheKey] = croppedImage
		imageCacheMutex.Unlock()
		fyne.Do(func() {
			// Cached for next time, but this widget has moved on; its current occupant either found
			// its own image in the cache or has a load of its own in flight.
			if di.generation != generation {
				return
			}
			di.backgroundColor.Image = croppedImage
			di.foregroundText.Hide()
			di.Refresh()
			if di.newBackgroundCallback != nil {
				di.newBackgroundCallback()
			}
		})
		return
	}
}

type defaultImageRenderer struct {
	di      *defaultImage
	objects []fyne.CanvasObject
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
	return dir.objects
}

func (dir *defaultImageRenderer) Refresh() {
	for _, obj := range dir.Objects() {
		obj.Refresh()
	}
}

//
// A color rectangle is a single color in a rectangle image
//

type colorRectangle struct {
	rect  image.Rectangle
	color color.Color
}

func (cr *colorRectangle) ColorModel() color.Model {
	return color.RGBAModel // TODO: match color?
}

func (cr *colorRectangle) Bounds() image.Rectangle {
	return cr.rect
}

func (cr *colorRectangle) At(x, y int) color.Color {
	return cr.color
}

// Deterministically generate a color from an ID
func idToColor(id chat.ID) color.RGBA {
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

func makeCircle(src image.Image) image.Image {
	bounds := src.Bounds()
	dx := bounds.Dx()
	dy := bounds.Dy()

	radius := dx / 2
	if dy < dx {
		radius = dy / 2
	}

	centerX := bounds.Min.X + dx/2
	centerY := bounds.Min.Y + dy/2

	cropRect := image.Rect(centerX-radius, centerY-radius, centerX+radius, centerY+radius)
	cropped := image.NewRGBA(cropRect)

	srcRGBA, isRGBA := src.(*image.RGBA)
	radiusSq := radius * radius

	for y := cropRect.Min.Y; y < cropRect.Max.Y; y++ {
		distY := y - centerY
		distYSq := distY * distY

		destRowStart := (y - cropRect.Min.Y) * cropped.Stride
		var srcRowStart int
		if isRGBA {
			srcRowStart = (y - bounds.Min.Y) * srcRGBA.Stride
		}

		for x := cropRect.Min.X; x < cropRect.Max.X; x++ {
			distX := x - centerX
			if (distX*distX)+distYSq < radiusSq {
				destIdx := destRowStart + (x-cropRect.Min.X)*4
				if isRGBA {
					srcIdx := srcRowStart + (x-bounds.Min.X)*4
					copy(cropped.Pix[destIdx:destIdx+4], srcRGBA.Pix[srcIdx:srcIdx+4])
				} else {
					r, g, b, a := src.At(x, y).RGBA()
					cropped.Pix[destIdx+0] = uint8(r >> 8)
					cropped.Pix[destIdx+1] = uint8(g >> 8)
					cropped.Pix[destIdx+2] = uint8(b >> 8)
					cropped.Pix[destIdx+3] = uint8(a >> 8)
				}
			}
		}
	}

	return cropped
}
