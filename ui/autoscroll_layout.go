package ui

import (
	"fyne.io/fyne/v2"
	log "github.com/sirupsen/logrus"
)

type autoscollLayout struct {
	lastHeight float32
}

func (asl *autoscollLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) != 1 {
		log.WithFields(log.Fields{
			"oject_count": len(objects),
		}).Fatal("cannot create autoscollLayout with multiple objects")
	}

	embedded := objects[0]
	embedded.Move(fyne.Position{0, 0})
	embedded.Resize(size)

	s, ok := embedded.(*chatHistory)
	if !ok {
		log.Fatal("cannot create autoscollLayout with anything other than chatHisotry")
	}

	if asl.lastHeight != 0 {
		if size.Height < asl.lastHeight {
			diff := asl.lastHeight - size.Height

			s.ScrollToOffset(s.GetScrollOffset() + diff)
		} else {
			diff := size.Height - asl.lastHeight

			if s.contentHeight()-asl.lastHeight != s.GetScrollOffset()+diff {
				s.ScrollToOffset(s.GetScrollOffset() - diff)
			}
		}
	}

	asl.lastHeight = size.Height
	s.Refresh()
}

func (asl *autoscollLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) != 1 {
		log.WithFields(log.Fields{
			"oject_count": len(objects),
		}).Fatal("cannot create autoscollLayout with multiple objects")
	}

	return objects[0].MinSize()
}
