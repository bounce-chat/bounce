package ui

import (
	"image"
	"image/color"
	"image/draw"
)

type circle struct {
	center image.Point
	radius int
}

func (c *circle) ColorModel() color.Model {
	return color.AlphaModel
}

func (c *circle) Bounds() image.Rectangle {
	return image.Rect(
		c.center.X-c.radius,
		c.center.Y-c.radius,
		c.center.X+c.radius,
		c.center.Y+c.radius,
	)
}

func (c *circle) At(x, y int) color.Color {
	dx := float64(x - c.center.X)
	dy := float64(y - c.center.Y)
	radius := float64(c.radius)

	if dx*dx+dy*dy < radius*radius {
		return color.Alpha{255}
	}
	return color.Alpha{0}
}

func makeCircle(src image.Image) image.Image {
	radius := 0
	if src.Bounds().Dx() > src.Bounds().Dy() {
		radius = src.Bounds().Dy() / 2
	} else {
		radius = src.Bounds().Dx() / 2
	}

	center := image.Point{
		X: src.Bounds().Dx() / 2,
		Y: src.Bounds().Dy() / 2,
	}

	c := &circle{
		center: center,
		radius: radius,
	}

	cropped := image.NewRGBA(c.Bounds())

	draw.DrawMask(
		cropped,
		src.Bounds(),
		src,
		image.Point{},
		c,
		image.Point{},
		draw.Over,
	)

	return cropped
}
