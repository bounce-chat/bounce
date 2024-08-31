package ui

import (
	"fyne.io/fyne/v2"
	log "github.com/sirupsen/logrus"
)

type autoscollLayout struct {
	lastHeight float32
}

func (mal *autoscollLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) != 1 {
		log.WithFields(log.Fields{
			"oject_count": len(objects),
		}).Fatal("cannot create autoscollLayout with multiple objects")
	}

	embedded := objects[0]
	embedded.Move(fyne.Position{0, 0})
	embedded.Resize(size)

	//s, ok := embedded.(*container.Scroll)
	//s, ok := embedded.(*widget.List)
	//if !ok {
	//	log.Fatal("cannot create autoscollLayout with anything other than container.Scroll")
	//}

	/*
		if mal.lastHeight != 0 {
			if size.Height < mal.lastHeight {
				diff := mal.lastHeight - size.Height

				s.Offset.Y += diff
				s.Refresh()
			} else {
				diff := size.Height - mal.lastHeight

				if s.Content.Size().Height-mal.lastHeight == s.Offset.Y+diff {
					s.ScrollToBottom()
				} else {
					s.Offset.Y -= diff
					s.Refresh()
				}
			}
		}
	*/

	mal.lastHeight = size.Height
}

func (mal *autoscollLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) != 1 {
		log.WithFields(log.Fields{
			"oject_count": len(objects),
		}).Fatal("cannot create autoscollLayout with multiple objects")
	}

	return objects[0].MinSize()
}
