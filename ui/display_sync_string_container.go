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

type displaySyncString struct {
	entry  *widget.Entry
	qrCode *canvas.Image
}

func (ui *ui) showDisplaySyncString() {
	if fyne.CurrentDevice().IsMobile() {
		ui.state.viewStack = append(ui.state.viewStack, view{viewType: viewTypeDisplaySyncString})
	}
	ui.state.currentView = viewTypeDisplaySyncString
	newDeviceString := ui.bounce.GetNewSyncString()
	ui.widgets.displaySyncString.entry.SetText(newDeviceString)

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
			ui.widgets.displaySyncString.qrCode.Image = qrImg
			ui.widgets.displaySyncString.qrCode.Refresh()
		}
	}

	ui.window.SetContent(ui.views.displaySyncString)
}

func (ui *ui) buildDisplaySyncString() {
	ui.widgets.displaySyncString = &displaySyncString{}

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

	ui.widgets.displaySyncString.qrCode = &canvas.Image{
		FillMode: canvas.ImageFillContain,
	}
	ui.widgets.displaySyncString.qrCode.SetMinSize(fyne.Size{Height: 256, Width: 256})

	ui.widgets.displaySyncString.entry = widget.NewEntry()
	ui.widgets.displaySyncString.entry.ActionItem = widget.NewButtonWithIcon("", theme.ContentCopyIcon(), func() { ui.window.Clipboard().SetContent(ui.widgets.displaySyncString.entry.Text) })

	ui.views.displaySyncString = container.New(
		layout.NewBorderLayout(closeBar, nil, nil, nil),
		closeBar,
		container.New(
			layout.NewBorderLayout(title, nil, nil, nil),
			title,
			container.NewVBox(
				ui.widgets.displaySyncString.qrCode,
				ui.widgets.displaySyncString.entry,
			),
		),
	)
}

func (ui *ui) NewSyncDeviceAdded() {
	fyne.DoAndWait(func() {
		if ui.state.currentView == viewTypeDisplaySyncString {
			if fyne.CurrentDevice().IsMobile() {
				ui.mobileBack()
			} else {
				ui.showEditProfile()
			}
		}
		ui.showDialog(dialog.NewInformation("New sync device", "A new device has been paired to your profile", ui.window), nil)
	})
}
