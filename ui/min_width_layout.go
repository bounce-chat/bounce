package ui

import (
	"fyne.io/fyne/v2"
)

type minWidthLayout struct {
	minWidth float32
}

func newMinWidthLayout(minWidth float32) fyne.Layout {
	return &minWidthLayout{
		minWidth: minWidth,
	}
}

func (mwl *minWidthLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, child := range objects {
		childWidth := child.MinSize().Width
		if mwl.minWidth > childWidth {
			childWidth = mwl.minWidth
		}
		child.Resize(fyne.Size{Width: childWidth, Height: child.MinSize().Height})
		child.Move(fyne.NewPos(0, 0))
	}
}

func (mwl *minWidthLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	anyVisible := false
	for _, child := range objects {
		if child.Visible() {
			anyVisible = true
			break
		}
	}
	if !anyVisible {
		return fyne.NewSize(0, 0)
	}

	minSize := fyne.NewSize(mwl.minWidth, 0)
	for _, child := range objects {
		if !child.Visible() {
			continue
		}

		minSize = minSize.Max(child.MinSize())
	}

	return minSize
}
