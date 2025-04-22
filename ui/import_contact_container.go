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

func (fyneUI *Fyne) showImportContact() {
	if fyne.CurrentDevice().IsMobile() {
		fyneUI.viewStack = append(fyneUI.viewStack, view{viewType: viewTypeImportContact})
	}
	fyneUI.mainWindow.SetContent(fyneUI.importContact)
	fyneUI.importContact.Show()
}

func (fyneUI *Fyne) buildImportContact() {
	closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		if fyne.CurrentDevice().IsMobile() {
			fyneUI.mobileBack()
		} else {
			fyneUI.showMainContainer()
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
			fyneUI.showDialog(dialog.NewError(showError, fyneUI.mainWindow), nil)
		} else if showInformation != nil {
			fyneUI.showDialog(showInformation, nil)
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
		newUser, err := fyneUI.callbacks.ImportUser(data)
		if err != nil {
			showError = errors.New("error importing contact: " + err.Error())
			return
		} else {
			// TODO: add this user to the store as a pending user, waiting on confirmation
			//fyneUI.users.add(&user{id: newUser.ID, name: newUser.Name}) // TODO: user store should use exported type
			showInformation = dialog.NewInformation("Success", fmt.Sprintf("%s has been imported", newUser.Name), fyneUI.mainWindow)
			// TODO: offer to delete file?
		}
		uName := binding.NewString()
		uName.Set(newUser.Name)
		fyneUI.users.add(&user{id: newUser.ID, name: uName})
	}, fyneUI.mainWindow)

	fyneUI.importContact = container.New( // TODO: scan QR code, have the engine respond will full contact details if QR secret is sent within 30 mins
		layout.NewBorderLayout(closeBar, nil, nil, nil),
		closeBar,
		container.NewCenter(
			widget.NewButton("Click here to select a contact file", func() { fyneUI.showDialog(fileSelector, dialogCleanup) }),
		),
	)
}
