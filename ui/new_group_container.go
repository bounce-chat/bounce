package ui

import (
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

func (fyneUI *Fyne) showNewGroup() {
	fyneUI.clearNewGroupSelectors()
	fyneUI.mainWindow.SetContent(fyneUI.newGroup)
	fyneUI.newGroup.Show()
}

func (fyneUI *Fyne) clearNewGroupSelectors() {
	// Empty the currently selected users
	// Refresh the list of available users to all users that aren't us
}

func (fyneUI *Fyne) buildNewGroup() {
	closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		fyneUI.showMainContainer()
	})
	closeButton.Importance = widget.LowImportance

	closeBar := container.New(
		layout.NewBorderLayout(nil, nil, nil, closeButton),
		closeButton,
	)

	groupNameEntry := widget.NewEntry()
	fyneUI.newGroup = container.New(
		layout.NewBorderLayout(closeBar, nil, nil, nil),
		closeBar,
		container.New(
			layout.NewBorderLayout(groupNameEntry, nil, nil, nil),
			groupNameEntry,
			container.NewHBox(
				widget.NewLabel("currently selected users"),
				widget.NewLabel("all available users"),
			),
		),
	)
}
