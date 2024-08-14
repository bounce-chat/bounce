package ui

import (
	"fyne.io/fyne/v2"
	log "github.com/sirupsen/logrus"
)

type paddedCenterLayout struct {
	topOffset   fyne.CanvasObject
	sidePadding float32
}

func newPaddedCenterLayout(topOffset fyne.CanvasObject) fyne.Layout {
	return &paddedCenterLayout{
		topOffset:   topOffset,
		sidePadding: 0.3,
	}
}

func (pcl *paddedCenterLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if pcl.sidePadding >= 0.5 {
		log.WithFields(log.Fields{
			"padding": pcl.sidePadding,
		}).Error("cannot use padded center layout with side padding >= 0.5")
		pcl.sidePadding = 0
	}
	portion := size.Width * pcl.sidePadding
	childWidth := size.Width - 2*portion

	for _, child := range objects {
		childMin := child.MinSize()
		if childMin.Width < childWidth {
			childMin.Width = childWidth
		}
		height := float32(size.Height-childMin.Height) / 2
		if pcl.topOffset != nil {
			height -= pcl.topOffset.Size().Height
			if height < 0 {
				height = 0
			}
		}
		child.Resize(childMin)
		child.Move(fyne.NewPos(float32(size.Width-childMin.Width)/2, height))
	}
}

func (pcl *paddedCenterLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	minSize := fyne.NewSize(0, 0)
	for _, child := range objects {
		if !child.Visible() {
			continue
		}

		minSize = minSize.Max(child.MinSize())
	}

	return minSize
}
