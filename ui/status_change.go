package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
)

type statusChange struct {
	widget.BaseWidget
	id        uuid.UUID
	timestamp int64
	label     *widget.Label
}

func newStatusChange(id uuid.UUID, timestamp int64, str string) *statusChange {
	baseLabel := widget.NewLabel(str)
	baseLabel.Alignment = fyne.TextAlignCenter

	st := &statusChange{
		id:        id,
		timestamp: timestamp,
		label:     baseLabel,
	}

	st.ExtendBaseWidget(st)

	return st
}

func (st *statusChange) CreateRenderer() fyne.WidgetRenderer {
	return st.label.CreateRenderer()
}
