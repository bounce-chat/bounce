package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

var minWidthOfLongActionInStatusChange = float32(256)

type statusChange struct {
	widget.BaseWidget
	id           uuid.UUID
	timestamp    int64
	actionString string
	fullAction   *canvas.Text
	action       *canvas.Text
	time         *canvas.Text
}

func newStatusChangeTemplate() *statusChange {
	st := &statusChange{
		id:           uuid.Nil,
		timestamp:    0,
		actionString: "",
		action: &canvas.Text{
			Text:     "",
			TextSize: theme.TextSize(),
		},
		fullAction: &canvas.Text{
			Text:     "",
			TextSize: theme.TextSize(),
		},
		time: &canvas.Text{
			Text:     "",
			TextSize: theme.TextSize() * 0.6,
		},
	}

	st.ExtendBaseWidget(st)

	return st
}

func (st *statusChange) setData(id uuid.UUID, timestamp int64, str string) {
	st.id = id
	st.timestamp = timestamp
	st.actionString = str
	st.fullAction.Text = str
	st.fullAction.Refresh()

	if st.Size().Width > 0 {
		widthAvailableForAction := st.Size().Width - theme.Padding() - st.time.MinSize().Width
		fullActionWidth := st.fullAction.MinSize().Width
		if fullActionWidth > widthAvailableForAction {
			percentAvailable := widthAvailableForAction / fullActionWidth
			numberOfCharactersThatCanFit := int(percentAvailable * float32(len(st.actionString)))
			truncatedText := st.actionString[0:numberOfCharactersThatCanFit]
			if len(truncatedText) > 3 {
				truncatedText = truncatedText[0:len(truncatedText)-4] + "..."
			}
			st.action.Text = truncatedText
			st.action.Refresh()
		} else {
			st.action.Text = st.actionString
			st.action.Refresh()
		}
	}

	st.time.Text = timestampString(timestamp)
	st.time.Refresh()
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
	if str.MinSize().Width > size.Width {
		log.WithFields(log.Fields{
			"size":     size,
			"min_size": str.MinSize(),
		}).Warn("refusing to layout into size smaller than minsize")
		return
	}
	widthAvailableForAction := size.Width - theme.Padding() - str.st.time.MinSize().Width
	fullActionWidth := str.st.fullAction.MinSize().Width
	totalWidth := str.MinSize().Width
	if fullActionWidth > widthAvailableForAction {
		percentAvailable := widthAvailableForAction / fullActionWidth
		numberOfCharactersThatCanFit := int(percentAvailable * float32(len(str.st.actionString)))
		truncatedText := str.st.actionString[0:numberOfCharactersThatCanFit]
		if len(truncatedText) > 3 {
			truncatedText = truncatedText[0:len(truncatedText)-4] + "..."
		}
		str.st.action.Text = truncatedText
		str.st.action.Refresh()
		totalWidth = size.Width
	} else {
		str.st.action.Text = str.st.actionString
		str.st.action.Refresh()
		totalWidth = str.st.fullAction.MinSize().Width + theme.Padding() + str.st.time.MinSize().Width
	}

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
	fullActionWidth := str.st.fullAction.MinSize().Width
	actionWidth := fullActionWidth
	if fullActionWidth > minWidthOfLongActionInStatusChange {
		actionWidth = minWidthOfLongActionInStatusChange
	}

	return fyne.Size{
		Width:  actionWidth + theme.Padding() + str.st.time.MinSize().Width,
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
