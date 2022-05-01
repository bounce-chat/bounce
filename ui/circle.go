package ui

import (
	"image"
	"image/color"
	"math"

	"fyne.io/fyne/v2/canvas"
)

func todoImage() *canvas.Image {
	return canvas.NewImageFromImage(makeCircle(&im{
		rect: image.Rect(0, 0, 512, 512),
	}))
}

type im struct {
	rect image.Rectangle
}

func (i *im) ColorModel() color.Model {
	return color.RGBAModel
}

func (i *im) Bounds() image.Rectangle {
	return i.rect
}

func (i *im) At(x, y int) color.Color {
	return color.RGBA{
		R: 0xaa,
		G: 0xbb,
		B: 0xcc,
		A: 0xff,
	}
}

type circle struct {
	original image.Image
}

func (c *circle) ColorModel() color.Model {
	return c.original.ColorModel()
}

func (c *circle) Bounds() image.Rectangle {
	return c.original.Bounds()
}

func (c *circle) At(x, y int) color.Color {
	bounds := c.original.Bounds()
	radius := float64(0)
	if bounds.Dx() > bounds.Dy() {
		radius = float64(bounds.Dy()) / float64(2)
	} else {
		radius = float64(bounds.Dx()) / float64(2)
	}
	centerX := bounds.Dx() / 2
	centerY := bounds.Dy() / 2
	distance := math.Sqrt(math.Pow(float64(x-centerX), 2) + math.Pow(float64(y-centerY), 2))
	if distance > radius {
		return color.RGBA{} // TODO: match the mode
	}
	return c.original.At(x, y)
}

func makeCircle(original image.Image) image.Image {
	return &circle{
		original: original,
	}
}
