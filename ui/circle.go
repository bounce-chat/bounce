package ui

import (
	"image"
	"image/color"
	"math"
)

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
