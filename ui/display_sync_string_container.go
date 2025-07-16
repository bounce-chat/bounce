package ui

import (
	"bytes"
	"image"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/rymdport/go-qrcode"
	log "github.com/sirupsen/logrus"
)

func (ui *ui) showDisplaySyncString() {
	if fyne.CurrentDevice().IsMobile() {
		ui.viewStack = append(ui.viewStack, view{viewType: viewTypeDisplaySyncString})
	}
	ui.currentView = viewTypeDisplaySyncString
	newDeviceString := ui.bounce.GetNewSyncString()
	ui.syncStringEntry.SetText(newDeviceString)

	qrData, err := qrcode.Encode(newDeviceString, qrcode.Medium, 256)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error QR encoding add device string")
	} else {
		qrImg, _, err := image.Decode(bytes.NewReader(qrData))
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error decoding QR data as image")
		} else {
			ui.addDeviceQRCode.Image = qrImg
			ui.addDeviceQRCode.Refresh()
		}
	}

	ui.mainWindow.SetContent(ui.displaySyncString)
	ui.displaySyncString.Show()
}

func (ui *ui) buildDisplaySyncString() {
	title := widget.NewLabel("Scan or paste this on the new device:")

	closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		if fyne.CurrentDevice().IsMobile() {
			ui.mobileBack()
		} else {
			ui.showEditProfile()
		}
	})
	closeButton.Importance = widget.LowImportance

	closeBar := container.New(
		layout.NewBorderLayout(nil, nil, nil, closeButton),
		closeButton,
	)

	ui.addDeviceQRCode = &canvas.Image{
		FillMode: canvas.ImageFillContain,
	}
	ui.addDeviceQRCode.SetMinSize(fyne.Size{Height: 256, Width: 256})

	ui.syncStringEntry = widget.NewEntry()
	ui.syncStringEntry.ActionItem = widget.NewButtonWithIcon("", theme.ContentCopyIcon(), func() { ui.mainWindow.Clipboard().SetContent(ui.syncStringEntry.Text) })

	ui.displaySyncString = container.New(
		layout.NewBorderLayout(closeBar, nil, nil, nil),
		closeBar,
		container.New(
			layout.NewBorderLayout(title, nil, nil, nil),
			title,
			container.NewVBox(
				ui.addDeviceQRCode,
				ui.syncStringEntry,
			),
		),
	)
}

func (ui *ui) NewSyncDeviceAdded() {
	fyne.DoAndWait(func() {
		if ui.currentView == viewTypeDisplaySyncString {
			if fyne.CurrentDevice().IsMobile() {
				ui.mobileBack()
			} else {
				ui.showEditProfile()
			}
		}
		ui.showDialog(dialog.NewInformation("New sync device", "A new device has been paired to your profile", ui.mainWindow), nil)
	})
}
