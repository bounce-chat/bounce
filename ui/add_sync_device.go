package ui

import (
	"bytes"
	"errors"
	"image"
	"strings"
	"time"

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

type addSyncDevice struct {
	standardHeader  *widget.Button
	encryptedHeader *widget.Button
	encryptedEntry  *widget.Entry
	entry           *widget.Entry
	qrCode          *canvas.Image
	currentStep     *widget.Label
	progressBar     *widget.ProgressBarInfinite
}

func (ui *ui) showDisplaySyncString() {
	if fyne.CurrentDevice().IsMobile() {
		ui.state.viewStack = append(ui.state.viewStack, view{viewType: viewTypeDisplaySyncString})
	}
	ui.state.currentView = viewTypeDisplaySyncString
	newDeviceString := ui.bounce.GetNewSyncString()
	ui.widgets.addSyncDevice.entry.SetText(newDeviceString)

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
			ui.widgets.addSyncDevice.qrCode.Image = qrImg
			ui.widgets.addSyncDevice.qrCode.Refresh()
		}
	}

	ui.widgets.addSyncDevice.encryptedEntry.Text = ""
	ui.widgets.addSyncDevice.encryptedEntry.Refresh()
	ui.widgets.addSyncDevice.progressBar.Hide()
	ui.widgets.addSyncDevice.currentStep.Text = ""
	ui.widgets.addSyncDevice.currentStep.Refresh()
	ui.widgets.addSyncDevice.currentStep.Hide()

	ui.widgets.addSyncDevice.standardHeader.Importance = widget.HighImportance
	ui.widgets.addSyncDevice.standardHeader.Refresh()
	ui.widgets.addSyncDevice.encryptedHeader.Importance = widget.MediumImportance
	ui.widgets.addSyncDevice.encryptedHeader.Refresh()
	ui.containers.addSyncDeviceContent.Objects = []fyne.CanvasObject{ui.views.addSyncDevice}
	ui.containers.addSyncDeviceContent.Refresh()
	ui.window.SetContent(ui.containers.syncDeviceOptions)
}

func (ui *ui) buildDisplaySyncString() {
	ui.widgets.addSyncDevice = &addSyncDevice{}
	ui.buildDisplayStandardSyncString()
	ui.buildInputEncryptedSyncString()

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

	ui.widgets.addSyncDevice.standardHeader = widget.NewButton("Standard", func() {
		ui.widgets.addSyncDevice.standardHeader.Importance = widget.HighImportance
		ui.widgets.addSyncDevice.standardHeader.Refresh()
		ui.widgets.addSyncDevice.encryptedHeader.Importance = widget.MediumImportance
		ui.widgets.addSyncDevice.encryptedHeader.Refresh()

		ui.containers.addSyncDeviceContent.Objects = []fyne.CanvasObject{ui.views.addSyncDevice}
		ui.containers.addSyncDeviceContent.Refresh()
	})
	ui.widgets.addSyncDevice.standardHeader.Importance = widget.HighImportance
	ui.widgets.addSyncDevice.encryptedHeader = widget.NewButton("Encrypted", func() {
		ui.widgets.addSyncDevice.encryptedHeader.Importance = widget.HighImportance
		ui.widgets.addSyncDevice.encryptedHeader.Refresh()
		ui.widgets.addSyncDevice.standardHeader.Importance = widget.MediumImportance
		ui.widgets.addSyncDevice.standardHeader.Refresh()

		ui.containers.addSyncDeviceContent.Objects = []fyne.CanvasObject{ui.containers.inputEncryptedSyncString}
		ui.containers.addSyncDeviceContent.Refresh()
		if !fyne.CurrentDevice().IsMobile() {
			ui.window.Canvas().Focus(ui.widgets.addSyncDevice.encryptedEntry)
		}
	})
	ui.widgets.addSyncDevice.encryptedHeader.Importance = widget.MediumImportance
	header := container.NewVBox(
		closeBar,
		container.NewCenter(container.NewHBox(
			ui.widgets.addSyncDevice.standardHeader,
			ui.widgets.addSyncDevice.encryptedHeader,
		)),
	)
	ui.containers.addSyncDeviceContent = container.NewStack()
	ui.containers.syncDeviceOptions = container.New(
		layout.NewBorderLayout(header, nil, nil, nil),
		header,
		ui.containers.addSyncDeviceContent,
	)
}

func (ui *ui) buildDisplayStandardSyncString() {
	title := widget.NewLabel("Scan or paste this on the new device:")

	ui.widgets.addSyncDevice.qrCode = &canvas.Image{
		FillMode: canvas.ImageFillContain,
	}
	ui.widgets.addSyncDevice.qrCode.SetMinSize(fyne.Size{Height: 256, Width: 256})

	ui.widgets.addSyncDevice.entry = widget.NewEntry()
	ui.widgets.addSyncDevice.entry.ActionItem = widget.NewButtonWithIcon("", theme.ContentCopyIcon(), func() { ui.window.Clipboard().SetContent(ui.widgets.addSyncDevice.entry.Text) })

	ui.views.addSyncDevice = container.New(
		layout.NewBorderLayout(title, nil, nil, nil),
		title,
		container.NewVBox(
			ui.widgets.addSyncDevice.qrCode,
			ui.widgets.addSyncDevice.entry,
		),
	)
}

func (ui *ui) buildInputEncryptedSyncString() {
	title := widget.NewLabel("Paste the sync string from the new encrypted device here:")

	ui.widgets.addSyncDevice.currentStep = widget.NewLabel("")
	ui.widgets.addSyncDevice.progressBar = widget.NewProgressBarInfinite()
	ui.widgets.addSyncDevice.encryptedEntry = widget.NewEntry()
	ui.widgets.addSyncDevice.encryptedEntry.OnSubmitted = func(str string) {
		ui.widgets.addSyncDevice.progressBar.Start()
		ui.widgets.addSyncDevice.progressBar.Show()
		go func() {
			if ui.state.networkState == networkStateStarting || ui.state.networkState == networkStateOffline {
				fyne.Do(func() {
					ui.widgets.addSyncDevice.currentStep.Text = "Waiting for network..."
					ui.widgets.addSyncDevice.currentStep.Refresh()
					ui.widgets.addSyncDevice.currentStep.Show()
				})
				for {
					time.Sleep(500 * time.Millisecond) // TODO: use a callback for this?
					if ui.state.networkState == networkStateOnline {
						break
					}
				}
			}

			fyne.Do(func() {
				ui.widgets.addSyncDevice.currentStep.Text = "Sending management request..."
				ui.widgets.addSyncDevice.currentStep.Refresh()
				ui.widgets.addSyncDevice.currentStep.Show()
			})
			err := ui.bounce.RequestToManageEncryptedDevice(strings.TrimSpace(str))
			fyne.Do(func() {
				if err != nil {
					ui.widgets.addSyncDevice.encryptedEntry.Text = ""
					ui.widgets.addSyncDevice.progressBar.Hide()
					ui.widgets.addSyncDevice.currentStep.Hide()
					ui.containers.scanUser.Refresh()
					ui.showDialog(dialog.NewError(errors.New("Error sending management request: "+err.Error()), ui.window), nil)
				} else {
					ui.widgets.addSyncDevice.currentStep.Text = "Waiting for response..."
					ui.widgets.addSyncDevice.currentStep.Refresh()
					ui.views.addSyncDevice.Refresh()
				}
			})
		}()
	}
	ui.widgets.addSyncDevice.encryptedEntry.ActionItem = widget.NewButtonWithIcon("", theme.MediaPhotoIcon(), func() {
		str, err := ui.scanQR()
		if str != "" && err == nil {
			ui.widgets.addSyncDevice.encryptedEntry.OnSubmitted(str)
		}
	})
	if !fyne.CurrentDevice().IsMobile() {
		ui.widgets.addSyncDevice.encryptedEntry.ActionItem.(*widget.Button).Disable()
	}

	ui.containers.inputEncryptedSyncString = container.New(
		layout.NewBorderLayout(title, nil, nil, nil),
		title,
		container.NewVBox(
			ui.widgets.addSyncDevice.encryptedEntry,
			ui.widgets.addSyncDevice.currentStep,
			ui.widgets.addSyncDevice.progressBar,
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

func (ui *ui) EncryptedDeviceAdded() {
	fyne.DoAndWait(func() {
		ui.widgets.addSyncDevice.currentStep.Text = "Success!"
		ui.widgets.addSyncDevice.currentStep.Refresh()
		ui.widgets.addSyncDevice.progressBar.Stop()
		ui.widgets.addSyncDevice.progressBar.Hide()
		if ui.state.currentView == viewTypeDisplaySyncString {
			if fyne.CurrentDevice().IsMobile() {
				ui.mobileBack() // Exit to edit profile
			} else {
				ui.showEditProfile()
			}
		}
		ui.showDialog(dialog.NewInformation("Device Added", "New encrypted device added to your profile", ui.window), nil)
	})
}

func (ui *ui) EncryptedDeviceRejected() {
	fyne.DoAndWait(func() {
		ui.showDialog(dialog.NewError(errors.New("Request to manage a new encrypted device was rejected"), ui.window), nil)
	})
}
