package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
)

type statusChange struct {
	widget.BaseWidget
	id         uuid.UUID
	timestamp  int64
	action     *widget.RichText
	actionSize fyne.Size
	time       *canvas.Text
}

func newStatusChangeTemplate() *statusChange {
	st := &statusChange{
		id:        uuid.Nil,
		timestamp: 0,
		action: widget.NewRichText(
			&widget.TextSegment{
				Text: "",
				Style: widget.RichTextStyle{
					SizeName: theme.SizeNameText,
				},
			},
		),
		time: &canvas.Text{
			Text:     "",
			TextSize: theme.TextSize() * 0.6,
		},
	}

	st.action.Truncation = fyne.TextTruncateEllipsis

	st.ExtendBaseWidget(st)

	return st
}

func (st *statusChange) setData(id uuid.UUID, timestamp int64, str string) {
	st.id = id
	st.timestamp = timestamp
	st.action.Segments[0].(*widget.TextSegment).Text = str
	st.actionSize = fyne.MeasureText(
		str,
		theme.Size(st.action.Segments[0].(*widget.TextSegment).Style.SizeName),
		st.action.Segments[0].(*widget.TextSegment).Style.TextStyle,
	)
	st.actionSize.Width += theme.Padding() * 2
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
	str.st.action.Resize(fyne.Size{
		Height: size.Height,
		Width:  size.Width - theme.Padding()*2 - str.st.time.MinSize().Width,
	})

	if size.Width < str.st.actionSize.Width+theme.Padding()*2+str.st.time.MinSize().Width {
		str.st.action.Move(fyne.Position{
			X: 0,
			Y: 0,
		})

		str.st.time.Move(fyne.Position{
			X: size.Width - str.st.time.MinSize().Width,
			Y: str.st.actionSize.Height + theme.Padding()*2 - str.st.time.MinSize().Height,
		})
	} else {
		str.st.action.Move(fyne.Position{
			X: (size.Width-str.st.actionSize.Width)/2 - theme.Padding() - str.st.time.MinSize().Width/2,
			Y: 0,
		})

		str.st.time.Move(fyne.Position{
			X: (size.Width-str.st.time.MinSize().Width)/2 + theme.Padding() + str.st.actionSize.Width/2,
			Y: str.st.actionSize.Height + theme.Padding()*2 - str.st.time.MinSize().Height,
		})
	}
}

func (str *statusChangeRenderer) MinSize() fyne.Size {
	return fyne.Size{
		Width:  str.st.action.MinSize().Width + theme.Padding()*2 + str.st.time.MinSize().Width,
		Height: str.st.actionSize.Height + theme.Padding()*2,
	}
}

func (str *statusChangeRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{
		str.st.action,
		str.st.time,
	}
}

func (str *statusChangeRenderer) Refresh() {
	str.st.time.Color = theme.Color(theme.ColorNameForeground)
	str.st.time.Text = timestampString(str.st.timestamp)
	for _, obj := range str.Objects() {
		obj.Refresh()
	}
}
