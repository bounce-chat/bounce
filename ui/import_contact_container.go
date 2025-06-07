package ui

import (
	"errors"
	"fmt"
	"io"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

func (ui *ui) showImportContact() {
	if fyne.CurrentDevice().IsMobile() {
		ui.viewStack = append(ui.viewStack, view{viewType: viewTypeImportContact})
	}
	ui.mainWindow.SetContent(ui.importContact)
	ui.importContact.Show()
}

func (ui *ui) buildImportContact() {
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

	var showError error
	var showInformation dialog.Dialog
	dialogCleanup := func() {
		if showError != nil {
			ui.showDialog(dialog.NewError(showError, ui.mainWindow), nil)
		} else if showInformation != nil {
			ui.showDialog(showInformation, nil)
		}
	}
	fileSelector := dialog.NewFileOpen(func(handler fyne.URIReadCloser, err error) {
		showError = nil
		showInformation = nil
		// TODO: handle the passed error
		if handler == nil {
			// Selection canceled
			return
		}
		data, err := io.ReadAll(handler)
		if err != nil {
			// TODO: log
			showError = errors.New("error reading file: " + err.Error())
			return
		}
		newUser, err := ui.bounce.ImportUser(data)
		if err != nil {
			showError = errors.New("error importing contact: " + err.Error())
			return
		} else {
			// TODO: add this user to the store as a pending user, waiting on confirmation
			//ui.users.add(&user{id: newUser.ID, name: newUser.Name}) // TODO: user store should use exported type
			showInformation = dialog.NewInformation("Success", fmt.Sprintf("%s has been imported", newUser.Name), ui.mainWindow)
			// TODO: offer to delete file?
		}
		uName := binding.NewString()
		uName.Set(newUser.Name)
		ui.users.add(&user{id: newUser.ID, name: uName})
	}, ui.mainWindow)

	ui.importContact = container.New( // TODO: scan QR code, have the engine respond will full contact details if QR secret is sent within 30 mins
		layout.NewBorderLayout(closeBar, nil, nil, nil),
		closeBar,
		container.NewCenter(
			widget.NewButton("Click here to select a contact file", func() { ui.showDialog(fileSelector, dialogCleanup) }),
		),
	)
}
