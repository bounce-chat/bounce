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

func (ui *ui) showAddUser() {
	if fyne.CurrentDevice().IsMobile() {
		ui.viewStack = append(ui.viewStack, view{viewType: viewTypeAddUser})
	}
	ui.mainWindow.SetContent(ui.addUser)
	ui.addUser.Show()
}

func (ui *ui) buildAddUser() {
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
		err := ui.bounce.RequestToAddUser(str)
		if err != nil {
			sendingStep.Hide()
			userStringEntry.Text = ""
			userStringEntry.Refresh()
			ui.showDialog(dialog.NewError(errors.New("Error sending friend request: "+err.Error()), ui.mainWindow), nil)
		} else {
			sendingRequestProgress.Stop()
			receivingStep.Show()
		}
	}

	ui.addUser = container.New(
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

func (ui *ui) AddUserRequestRejected(peer string) {
	fyne.DoAndWait(func() {
		ui.showDialog(dialog.NewError(errors.New("Friend request rejected, make sure you scan the device quickly"), ui.mainWindow), nil)
	})
}
