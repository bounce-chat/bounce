package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type manageUsers struct {
	entry *widget.Entry
}

func (ui *ui) showManageUsers() {
	if fyne.CurrentDevice().IsMobile() {
		ui.state.viewStack = append(ui.state.viewStack, view{viewType: viewTypeManageUsers})
	}
	ui.state.currentView = viewTypeManageUsers
	ui.window.SetContent(ui.views.manageUsers)
}

func (ui *ui) buildManageUsers() {
	ui.widgets.manageUsers = &manageUsers{}

	closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		if fyne.CurrentDevice().IsMobile() {
			ui.mobileBack()
		} else {
			ui.showMainContainer()
		}
	})
	closeButton.Importance = widget.LowImportance

	closeBar := container.New(
		layout.NewBorderLayout(nil, nil, nil, closeButton),
		closeButton,
	)

	ui.widgets.manageUsers.entry = widget.NewEntry()
	filters := container.NewHBox(
		widget.NewCheck("Show Blocked", func(set bool) {}),
	)

	searchBar := container.New(
		layout.NewBorderLayout(nil, nil, nil, filters),
		filters,
		ui.widgets.manageUsers.entry,
	)
	ui.views.manageUsers = container.New(
		layout.NewBorderLayout(closeBar, nil, nil, nil),
		closeBar,
		container.New(
			layout.NewBorderLayout(searchBar, nil, nil, nil),
			searchBar,
			// TODO: users scroll
		),
	)
}
