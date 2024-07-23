package ui

import (
	"errors"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

func (fyneUI *Fyne) showAddUser() {
	if fyne.CurrentDevice().IsMobile() {
		fyneUI.viewStack = append(fyneUI.viewStack, view{viewType: viewTypeAddUser})
	}
	fyneUI.mainWindow.SetContent(fyneUI.addUser)
	fyneUI.addUser.Show()
}

func (fyneUI *Fyne) buildAddUser() {
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

	sendingRequestLabel := widget.NewLabel("Sending Friend Request")
	sendingRequestProgress := widget.NewProgressBarInfinite()
	sendingStep := container.New(
		layout.NewBorderLayout(nil, nil, sendingRequestLabel, nil),
		sendingRequestLabel,
		sendingRequestProgress,
	)
	sendingStep.Hide()

	receivingResponseLabel := widget.NewLabel("Receiving Response")
	receivingResponseProgress := widget.NewProgressBarInfinite()
	receivingStep := container.New(
		layout.NewBorderLayout(nil, nil, receivingResponseLabel, nil),
		receivingResponseLabel,
		receivingResponseProgress,
	)
	receivingStep.Hide()

	userStringEntry := widget.NewEntry()
	userStringEntry.OnSubmitted = func(str string) {
		sendingStep.Show()
		err := fyneUI.callbacks.RequestToAddUser(str)
		if err != nil {
			sendingStep.Hide()
			userStringEntry.Text = ""
			userStringEntry.Refresh()
			dialog.ShowError(errors.New("Error sending friend request: "+err.Error()), fyneUI.mainWindow)
		} else {
			sendingRequestProgress.Stop()
			receivingStep.Show()
		}
	}

	fyneUI.addUser = container.New(
		layout.NewBorderLayout(closeBar, nil, nil, nil),
		closeBar,
		container.NewVBox(
			widget.NewLabel("Paste in the string or scan the QR code to add friend"),
			userStringEntry,
			sendingStep,
			receivingStep,
		),
	)
}

func (fyneUI *Fyne) AddUserRequestRejected(peer string) {
	dialog.ShowError(errors.New("Friend request rejected, make sure you scan the device quickly"), fyneUI.mainWindow)
}
