package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	log "github.com/sirupsen/logrus"
)

func (fyneUI *Fyne) showDisplaySyncString() {
	if fyne.CurrentDevice().IsMobile() {
		fyneUI.viewStack = append(fyneUI.viewStack, view{viewType: viewTypeDisplaySyncString})
	}
	err := fyneUI.syncString.Set(fyneUI.callbacks.GetNewSyncString())
	if err != nil {
		log.Fatal("data bindings are broken")
	}

	// TODO: update the QR code data

	fyneUI.mainWindow.SetContent(fyneUI.displaySyncString)
	fyneUI.displaySyncString.Show()
}

func (fyneUI *Fyne) buildDisplaySyncString() {
	title := widget.NewLabel("Scan or paste this on the new device:")

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

	fyneUI.displaySyncString = container.New(
		layout.NewBorderLayout(closeBar, nil, nil, nil),
		closeBar,
		container.New(
			layout.NewBorderLayout(title, nil, nil, nil),
			title,
			container.NewVBox(
				widget.NewEntryWithData(fyneUI.syncString),
			),
		),
	)
}

func (fyneUI *Fyne) NewSyncDeviceAdded() {
	fyne.DoAndWait(func() {
		fyneUI.showDialog(dialog.NewInformation("New sync device", "A new device has been paired to your profile", fyneUI.mainWindow), nil)
	})
}
