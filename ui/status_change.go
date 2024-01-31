package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
)

type statusChange struct {
	widget.BaseWidget
	id    uuid.UUID
	label *widget.Label
}

func newStatusChange(str string) *statusChange {
	baseLabel := widget.NewLabel(str)
	baseLabel.Alignment = fyne.TextAlignCenter

	st := &statusChange{
		label: baseLabel,
	}

	st.ExtendBaseWidget(st)

	return st
}

func (st *statusChange) CreateRenderer() fyne.WidgetRenderer {
	return st.label.CreateRenderer()
}
