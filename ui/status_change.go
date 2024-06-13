package ui

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

type statusChange struct {
	widget.BaseWidget
	id        uuid.UUID
	timestamp int64
	action    *canvas.Text
	time      *canvas.Text
}

func newStatusChange(id uuid.UUID, timestamp int64, str string) *statusChange {
	st := &statusChange{
		id:        id,
		timestamp: timestamp,
		action: &canvas.Text{
			Text:     str,
			TextSize: theme.TextSize(),
		},
		time: &canvas.Text{
			Text:     time.Unix(timestamp, 0).Format("1/2 15:04"),
			TextSize: theme.TextSize() * 0.6,
		},
	}

	st.ExtendBaseWidget(st)

	return st
}

func (st *statusChange) CreateRenderer() fyne.WidgetRenderer {
	st.ExtendBaseWidget(st)

	str := &statusChangeRenderer{
		st: st,
	}

	return str
}

type statusChangeRenderer struct {
	st *statusChange
}

func (str *statusChangeRenderer) Destroy() {}

func (str *statusChangeRenderer) Layout(size fyne.Size) {
	totalWidth := str.MinSize().Width
	leftoverWidth := size.Width - totalWidth
	if leftoverWidth < 0 {
		log.WithFields(log.Fields{
			"id": str.st.id,
		}).Debug("refusing to layout status change in space smaller than minsize")
		return
	}
	offset := leftoverWidth / 2

	leftoverActionHeight := size.Height - str.st.action.MinSize().Height
	str.st.action.Move(fyne.Position{
		X: offset,
		Y: theme.Padding() + leftoverActionHeight/2,
	})

	str.st.time.Move(fyne.Position{
		X: offset + str.st.action.MinSize().Width + theme.Padding(),
		Y: size.Height - str.st.time.MinSize().Height,
	})
}

func (str *statusChangeRenderer) MinSize() fyne.Size {
	return fyne.Size{
		Width:  str.st.action.MinSize().Width + theme.Padding() + str.st.time.MinSize().Width,
		Height: str.st.action.MinSize().Height + theme.Padding()*2,
	}
}

func (str *statusChangeRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{
		str.st.action,
		str.st.time,
	}
}

func (str *statusChangeRenderer) Refresh() {}
