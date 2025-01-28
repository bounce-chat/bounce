package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	log "github.com/sirupsen/logrus"
)

type themedImage struct {
	widget.BaseWidget

	image *canvas.Image
	dark  fyne.Resource
	light fyne.Resource
}

func newThemedImage(dark, light fyne.Resource, width, height float32) *themedImage {
	ti := &themedImage{
		image: &canvas.Image{},
		dark:  dark,
		light: light,
	}

	ti.image.FillMode = canvas.ImageFillContain
	ti.image.Resize(fyne.NewSize(width, height))
	ti.image.SetMinSize(fyne.NewSize(width, height))
	ti.Refresh()

	ti.ExtendBaseWidget(ti)

	return ti
}

func (ti *themedImage) Refresh() {
	fv, ok := theme.Current().(*forcedVariant)
	if !ok {
		log.Warn("cannot use themedImage when not using the forcedVarient theme")
		return
	}
	if fv.variant == theme.VariantDark {
		ti.image.ScaleMode = canvas.ImageScaleSmooth
		ti.image.Resource = ti.dark
	} else {
		ti.image.ScaleMode = canvas.ImageScalePixels
		ti.image.Resource = ti.light
	}

	ti.image.Refresh()
}

func (ti *themedImage) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(ti.image)
}
