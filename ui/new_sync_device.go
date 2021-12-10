package ui

import (
	"errors"

	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
)

func (fyneUI *Fyne) showNewSyncDevice() {
	fyneUI.mainWindow.SetContent(fyneUI.newSyncDevice)
	fyneUI.newSyncDevice.Show()
}

func (fyneUI *Fyne) buildNewSyncDevice() {
	backButton := widget.NewButton("Back", func() {
		fyneUI.mainWindow.SetContent(fyneUI.newInstall)
		fyneUI.newInstall.Show()
	})
	actionButtons := container.New(
		layout.NewBorderLayout(nil, nil, backButton, nil),
		backButton,
	)

	sendingRequestLabel := widget.NewLabel("Sending Sync Request")
	sendingRequestProgress := widget.NewProgressBarInfinite()
	sendingStep := container.New(
		layout.NewBorderLayout(nil, nil, sendingRequestLabel, nil),
		sendingRequestLabel,
		sendingRequestProgress,
	)
	sendingStep.Hide()

	receivingResponseLabel := widget.NewLabel("Receiving Sync Response")
	receivingResponseProgress := widget.NewProgressBarInfinite()
	receivingStep := container.New(
		layout.NewBorderLayout(nil, nil, receivingResponseLabel, nil),
		receivingResponseLabel,
		receivingResponseProgress,
	)
	receivingStep.Hide()

	syncStringEntry := widget.NewEntry()
	syncStringEntry.OnSubmitted = func(str string) {
		sendingStep.Show()
		err := fyneUI.callbacks.RequestToSync(str)
		if err != nil {
			sendingStep.Hide()
			syncStringEntry.Text = ""
			syncStringEntry.Refresh()
			dialog.ShowError(errors.New("Error sending sync request: "+err.Error()), fyneUI.mainWindow)
		} else {
			sendingRequestProgress.Stop()
			receivingStep.Show()
		}
	}

	fyneUI.newSyncDevice = container.New(
		layout.NewBorderLayout(nil, actionButtons, nil, nil),
		actionButtons,
		container.NewVBox(
			widget.NewLabel("Paste in the string or scan the QR code to pair this device with another profile"),
			syncStringEntry,
			sendingStep,
			receivingStep,
		),
	)
}

func (fyneUI *Fyne) SyncDeviceRequestAccepted(id uuid.UUID, name string) {
	profile := &user{id: id, name: name}
	fyneUI.profile = profile
	fyneUI.users.add(profile)
	fyneUI.initialStateSet = true
	fyneUI.showMainContainer()
}

func (fyneUI *Fyne) SyncDeviceRequestRejected() {
	dialog.ShowError(errors.New("Sync request rejected, make sure you scan the device quickly"), fyneUI.mainWindow)
}
