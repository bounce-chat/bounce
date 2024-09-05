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
	s, ok := embedded.(*List)
	if !ok {
		log.Fatal("cannot create autoscollLayout with anything other than container.Scroll")
	}

	if mal.lastHeight != 0 {
		//log.WithFields(log.Fields{
		//	"mal.lastHeight": mal.lastHeight,
		//	"size.Height":    size.Height,
		//}).Warn("updating from last height to a new height")
		if size.Height < mal.lastHeight {
			diff := mal.lastHeight - size.Height

			//s.OffsetY += diff
			//s.Offset.Y += diff
			//s.Refresh()
			s.ScrollToOffset(s.GetScrollOffset() + diff)
		} else {
			diff := size.Height - mal.lastHeight

			//if s.Content.Size().Height-mal.lastHeight == s.Offset.Y+diff {
			//if s.scrollerHeight()-mal.lastHeight == s.GetScrollOffset()+diff {
			//if s.Size().Height-mal.lastHeight == s.GetScrollOffset()+diff {
			if s.contentHeight()-mal.lastHeight == s.GetScrollOffset()+diff {
				//log.WithFields(log.Fields{
				//	"s.Size().Height":                s.Size().Height,
				//	"mal.LastHeight":                 mal.lastHeight,
				//	"s.GetScrollOffset()":            s.GetScrollOffset(),
				//	"diff":                           diff,
				//	"s.Size().Height-mal.lastHeight": s.Size().Height - mal.lastHeight,
				//	"s.GetScrollOffset()+diff":       s.GetScrollOffset() + diff,
				//	"s.Size().Height-mal.lastHeight == s.GetScrollOffset()+diff": s.Size().Height-mal.lastHeight == s.GetScrollOffset()+diff,
				//}).Warn("laying out auto-scroll layout")
				s.ScrollToBottom()
			} else {
				//s.Offset.Y -= diff
				//s.OffsetY -= diff
				//s.Refresh()
				s.ScrollToOffset(s.GetScrollOffset() - diff)
			}
		}
	}

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
