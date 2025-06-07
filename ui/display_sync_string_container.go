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

func (ui *ui) showDisplaySyncString() {
	if fyne.CurrentDevice().IsMobile() {
		ui.viewStack = append(ui.viewStack, view{viewType: viewTypeDisplaySyncString})
	}
	err := ui.syncString.Set(ui.bounce.GetNewSyncString())
	if err != nil {
		log.Fatal("data bindings are broken")
	}

	// TODO: update the QR code data

	ui.mainWindow.SetContent(ui.displaySyncString)
	ui.displaySyncString.Show()
}

func (ui *ui) buildDisplaySyncString() {
	title := widget.NewLabel("Scan or paste this on the new device:")

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

	ui.displaySyncString = container.New(
		layout.NewBorderLayout(closeBar, nil, nil, nil),
		closeBar,
		container.New(
			layout.NewBorderLayout(title, nil, nil, nil),
			title,
			container.NewVBox(
				widget.NewEntryWithData(ui.syncString),
			),
		),
	)
}

func (ui *ui) NewSyncDeviceAdded() {
	fyne.DoAndWait(func() {
		ui.showDialog(dialog.NewInformation("New sync device", "A new device has been paired to your profile", ui.mainWindow), nil)
	})
}
