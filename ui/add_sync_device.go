package ui

import (
	"bytes"
	"errors"
	"image"
	"strings"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/sensor"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	go_qr "github.com/piglig/go-qr"
	"github.com/rymdport/go-qrcode"
	log "github.com/sirupsen/logrus"
)

var stopNewEncryptedDeviceCameraProcessing chan bool
var newEncryptedDeviceScanner = make(chan image.Image)

type addSyncDevice struct {
	standardHeader  *widget.Button
	encryptedHeader *widget.Button
	encryptedEntry  *widget.Entry
	entry           *widget.Entry
	qrCode          *canvas.Image
	currentStep     *widget.Label
	progressBar     *widget.ProgressBarInfinite
	videoStream     *canvas.Image
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
	cameraDevice, isCameraDevice := fyne.CurrentDevice().(sensor.CameraDevice)
	ui.widgets.addSyncDevice = &addSyncDevice{}
	ui.buildDisplayStandardSyncString()
	ui.buildInputEncryptedSyncString()

	closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		if fyne.CurrentDevice().IsMobile() {
			ui.mobileBack()
		} else {
			if cameraDevice, isCameraDevice := fyne.CurrentDevice().(sensor.CameraDevice); isCameraDevice {
				if cameraIsRunning {
					cameraDevice.StopPreview()
					cameraIsRunning = false
					select {
					case stopNewSyncDeviceCameraProcessing <- true:
					default:
					}
				}
			}
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
		if isCameraDevice && !cameraIsRunning {
			cameraDevice.StartPreview()
			cameraIsRunning = true
			go ui.processCameraFramesForNewEncryptedDevice()
		}
		ui.widgets.addSyncDevice.encryptedHeader.Importance = widget.HighImportance
		ui.widgets.addSyncDevice.encryptedHeader.Refresh()
		ui.widgets.addSyncDevice.standardHeader.Importance = widget.MediumImportance
		ui.widgets.addSyncDevice.standardHeader.Refresh()

		ui.containers.addSyncDeviceContent.Objects = []fyne.CanvasObject{ui.containers.inputEncryptedSyncString}
		ui.containers.addSyncDeviceContent.Refresh()
		if !isCameraDevice {
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
	cameraDevice, isCameraDevice := fyne.CurrentDevice().(sensor.CameraDevice)
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
					ui.showDialog(dialog.NewError(errors.New("Error sending management request: "+err.Error()), ui.window), func() {
						if isCameraDevice && !cameraIsRunning {
							cameraDevice.StartPreview()
							cameraIsRunning = true
							go ui.processCameraFramesForNewEncryptedDevice()
						}
					})
				} else {
					ui.widgets.addSyncDevice.currentStep.Text = "Waiting for response..."
					ui.widgets.addSyncDevice.currentStep.Refresh()
					ui.views.addSyncDevice.Refresh()
				}
			})
		}()
	}

	if isCameraDevice {
		size := float32(300)

		ui.widgets.addSyncDevice.videoStream = &canvas.Image{
			FillMode: canvas.ImageFillCover,
		}
		ui.widgets.addSyncDevice.videoStream.Resize(fyne.Size{Height: size, Width: size})
		ui.widgets.addSyncDevice.videoStream.SetMinSize(fyne.Size{Height: size, Width: size})

		guideImage, _ := assets.ReadFile("assets/qr-guide.png")
		guide := canvas.NewImageFromReader(bytes.NewReader(guideImage), "qr-guide.png")
		guide.Translucency = 0.5
		guide.Resize(fyne.Size{Height: size, Width: size})
		guide.SetMinSize(fyne.Size{Height: size, Width: size})

		var haveResult atomic.Bool
		go func() {
			for frame := range newEncryptedDeviceScanner {
				text, err := go_qr.Decode(frame)
				if err == nil {
					fyne.Do(func() {
						if haveResult.Load() {
							return
						}
						haveResult.Store(true)
						if cameraIsRunning {
							cameraDevice.StopPreview()
							cameraIsRunning = false
							select {
							case stopNewEncryptedDeviceCameraProcessing <- true:
							default:
							}
						}
						ui.widgets.addSyncDevice.encryptedEntry.SetText(text)
						ui.widgets.addSyncDevice.encryptedEntry.OnSubmitted(text)
						haveResult.Store(false)
					})
				}
			}
		}()

		ui.containers.inputEncryptedSyncString = container.New(
			layout.NewBorderLayout(title, nil, nil, nil),
			title,
			container.NewVBox(
				container.NewCenter(container.NewStack(
					ui.widgets.addSyncDevice.videoStream,
					guide,
				)),
				ui.widgets.addSyncDevice.encryptedEntry,
				ui.widgets.addSyncDevice.currentStep,
				ui.widgets.addSyncDevice.progressBar,
			),
		)
	} else {
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
	cameraDevice, isCameraDevice := fyne.CurrentDevice().(sensor.CameraDevice)
	fyne.DoAndWait(func() {
		ui.showDialog(dialog.NewError(errors.New("Request to manage a new encrypted device was rejected"), ui.window), func() {
			if isCameraDevice && !cameraIsRunning {
				cameraDevice.StartPreview()
				cameraIsRunning = true
				go ui.processCameraFramesForNewEncryptedDevice()
			}
		})
	})
}

func (ui *ui) processCameraFramesForNewEncryptedDevice() {
	cameraDevice, isCameraDevice := fyne.CurrentDevice().(sensor.CameraDevice)
	if !isCameraDevice {
		return
	}
	preview := cameraDevice.Preview()

	for {
		select {
		case img := <-preview:
			fyne.Do(func() {
				if ui.widgets.newSyncDevice.videoStream == nil {
					return
				}
				ui.widgets.addSyncDevice.videoStream.Image = img
				ui.widgets.addSyncDevice.videoStream.Refresh()
			})
			select {
			case newEncryptedDeviceScanner <- img:
			default:
			}
		case <-stopNewEncryptedDeviceCameraProcessing:
			return
		}
	}
}
