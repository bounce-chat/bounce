package ui

import (
	"bytes"
	"errors"
	"image"
	"os"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/sensor"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/bounce-chat/bounce/chat"
	go_qr "github.com/piglig/go-qr"
	log "github.com/sirupsen/logrus"
)

var stopNewSyncDeviceCameraProcessing chan bool
var newSyncDeviceScanner = make(chan image.Image)

type newSyncDevice struct {
	currentStep         *widget.Label
	progressBars        *fyne.Container
	progressBar         *widget.ProgressBar
	infiniteProgressBar *widget.ProgressBarInfinite
	backButton          *widget.Button
	deviceNameEntry     *widget.Entry
	syncStringEntry     *widget.Entry
	syncStringInput     *fyne.Container
	videoStream         *canvas.Image
}

func (ui *ui) buildNewSyncDeviceWidgets() {
	cameraDevice, isCameraDevice := fyne.CurrentDevice().(sensor.CameraDevice)

	progressBar := widget.NewProgressBar()
	infiniteProgressBar := widget.NewProgressBarInfinite()
	progressBars := container.NewStack(
		infiniteProgressBar,
		progressBar,
	)

	ui.widgets.newSyncDevice = &newSyncDevice{
		currentStep:         widget.NewLabel(""),
		progressBars:        progressBars,
		progressBar:         progressBar,
		infiniteProgressBar: infiniteProgressBar,
		backButton: widget.NewButton("Back", func() {
			ui.widgets.newSyncDevice.infiniteProgressBar.Hide()
			ui.widgets.newSyncDevice.currentStep.Text = ""
			ui.widgets.newSyncDevice.currentStep.Hide()
			ui.widgets.newSyncDevice.syncStringEntry.Text = ""
			ui.widgets.newSyncDevice.syncStringEntry.Refresh()
			ui.widgets.newSyncDevice.syncStringInput.Show()
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
				ui.showNewSyncDevice()
			}
		}),
		deviceNameEntry: widget.NewEntry(),
		syncStringEntry: widget.NewEntry(),
	}
	ui.widgets.newSyncDevice.currentStep.Alignment = fyne.TextAlignCenter

	syncStringEntryLabel := widget.NewLabel("Paste in the string to pair this device with an exiting profile")
	syncStringEntryLabel.Alignment = fyne.TextAlignCenter

	syncButton := widget.NewButton("Sync", func() {
		ui.widgets.newSyncDevice.syncStringEntry.OnSubmitted(ui.widgets.newSyncDevice.syncStringEntry.Text)
	})
	syncButton.Importance = widget.HighImportance

	if isCameraDevice {
		size := float32(300)

		ui.widgets.newSyncDevice.videoStream = &canvas.Image{
			FillMode: canvas.ImageFillCover,
		}
		ui.widgets.newSyncDevice.videoStream.Resize(fyne.Size{size, size})
		ui.widgets.newSyncDevice.videoStream.SetMinSize(fyne.Size{size, size})

		guideImage, _ := assets.ReadFile("assets/qr-guide.png")
		guide := canvas.NewImageFromReader(bytes.NewReader(guideImage), "qr-guide.png")
		guide.Translucency = 0.5
		guide.Resize(fyne.Size{size, size})
		guide.SetMinSize(fyne.Size{size, size})

		var haveResult atomic.Bool
		go func() {
			for frame := range newSyncDeviceScanner {
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
							case stopNewSyncDeviceCameraProcessing <- true:
							default:
							}
						}
						ui.widgets.newSyncDevice.syncStringEntry.SetText(text)
						ui.widgets.newSyncDevice.syncStringEntry.OnSubmitted(text)
						haveResult.Store(false)
					})
				}
			}
		}()

		ui.widgets.newSyncDevice.syncStringInput = container.NewVBox(
			syncStringEntryLabel,
			container.NewCenter(container.NewStack(
				ui.widgets.newSyncDevice.videoStream,
				guide,
			)),
			container.New(
				layout.NewBorderLayout(nil, nil, nil, syncButton),
				syncButton,
				ui.widgets.newSyncDevice.syncStringEntry,
			),
		)
	} else {
		ui.widgets.newSyncDevice.syncStringInput = container.NewVBox(
			syncStringEntryLabel,
			container.New(
				layout.NewBorderLayout(nil, nil, nil, syncButton),
				syncButton,
				ui.widgets.newSyncDevice.syncStringEntry,
			),
		)
	}

	ui.widgets.newSyncDevice.deviceNameEntry.OnChanged = func(str string) {
		// Remove any leading whitespace
		str, trimmed := trimLeadingSpace(str)
		ui.widgets.newSyncDevice.deviceNameEntry.Text = str
		if trimmed != 0 {
			ui.widgets.newSyncDevice.deviceNameEntry.CursorRow = 0
			ui.widgets.newSyncDevice.deviceNameEntry.CursorColumn = 0
		}

		// Enforce length limit
		if utf8.RuneCountInString(str) > chat.MaximumNameLength {
			runes := []rune(str)
			truncated := runes[0:chat.MaximumNameLength]
			ui.widgets.newSyncDevice.deviceNameEntry.Text = string(truncated)
		}
		ui.widgets.newSyncDevice.deviceNameEntry.Refresh()
	}
}

func (ui *ui) showNameNewDevice() {
	if fyne.CurrentDevice().IsMobile() {
		ui.state.viewStack = append(ui.state.viewStack, view{viewType: viewTypeNameNewDevice})
	}
	ui.state.currentView = viewTypeNameNewDevice
	ui.window.SetContent(ui.views.nameNewDevice)
	ui.views.nameNewDevice.Show()
	ui.window.Canvas().Focus(ui.widgets.newSyncDevice.deviceNameEntry)
}

func (ui *ui) buildNameNewDevice() {
	nameLabel := widget.NewLabel("Give this new device a name")
	nameLabel.Alignment = fyne.TextAlignCenter
	header := container.NewVBox(
		container.NewCenter(makeLogo(228, 167)), // TODO: choose reasonable values here, https://github.com/fyne-io/fyne/blob/v2.0.3/cmd/fyne_demo/tutorials/welcome.go#L25
		container.New(
			newPaddedCenterLayout(nil),
			container.NewVBox(
				nameLabel,
				ui.widgets.newSyncDevice.deviceNameEntry,
			),
		),
	)

	hostname, err := os.Hostname()
	if err == nil {
		ui.widgets.newSyncDevice.deviceNameEntry.PlaceHolder = hostname
	}
	ui.widgets.newSyncDevice.deviceNameEntry.OnSubmitted = func(_ string) {
		ui.showNewSyncDevice()
	}

	backButton := widget.NewButton("Back", func() {
		if fyne.CurrentDevice().IsMobile() {
			ui.mobileBack()
		} else {
			ui.window.SetContent(ui.views.newInstall)
			ui.views.newInstall.Show()
		}
	})

	nextButton := widget.NewButton("Next", func() {
		ui.showNewSyncDevice()
	})
	nextButton.Importance = widget.HighImportance

	actionButtons := container.New(
		layout.NewBorderLayout(nil, nil, backButton, nextButton),
		backButton,
		nextButton,
	)

	ui.views.nameNewDevice = container.New(
		layout.NewBorderLayout(header, actionButtons, nil, nil),
		header,
		actionButtons,
	)
}

func (ui *ui) showNewSyncDevice() {
	ui.widgets.newSyncDevice.syncStringInput.Show()
	if fyne.CurrentDevice().IsMobile() {
		ui.state.viewStack = append(ui.state.viewStack, view{viewType: viewTypeNewSyncDevice})
	}
	ui.state.currentView = viewTypeNewSyncDevice
	ui.window.SetContent(ui.views.newSyncDevice)
	ui.window.Canvas().Focus(ui.widgets.newSyncDevice.syncStringEntry)

	ui.widgets.newSyncDevice.syncStringInput.Show()
	ui.widgets.newSyncDevice.backButton.Enable()
	ui.widgets.newSyncDevice.infiniteProgressBar.Hide()
	ui.widgets.newSyncDevice.currentStep.Text = ""
	ui.widgets.newSyncDevice.currentStep.Refresh()

	cameraDevice, isCameraDevice := fyne.CurrentDevice().(sensor.CameraDevice)
	if isCameraDevice {
		cameraDevice.StartPreview()
		cameraIsRunning = true
		go ui.processCameraFramesForNewSyncDevice()
	}
}

func (ui *ui) buildNewSyncDevice() {
	cameraDevice, isCameraDevice := fyne.CurrentDevice().(sensor.CameraDevice)
	logo := makeLogo(228, 167) // TODO: choose reasonable values here, https://github.com/fyne-io/fyne/blob/v2.0.3/cmd/fyne_demo/tutorials/welcome.go#L25
	header := container.NewVBox(
		container.NewCenter(logo),
		container.New(
			newPaddedCenterLayout(nil),
			ui.widgets.newSyncDevice.syncStringInput,
		),
	)

	actionButtons := container.New(
		layout.NewBorderLayout(nil, nil, ui.widgets.newSyncDevice.backButton, nil),
		ui.widgets.newSyncDevice.backButton,
	)

	ui.widgets.newSyncDevice.currentStep.Hide()
	ui.widgets.newSyncDevice.infiniteProgressBar.Hide()
	ui.widgets.newSyncDevice.progressBar.Hide()

	ui.widgets.newSyncDevice.syncStringEntry.OnSubmitted = func(str string) {
		ui.widgets.newSyncDevice.syncStringInput.Hide()
		ui.state.initialSyncIncomplete = true
		// TODO: change logo to loading version
		ui.widgets.newSyncDevice.currentStep.Show()
		go func() {
			if ui.state.networkState == networkStateStarting || ui.state.networkState == networkStateOffline {
				fyne.Do(func() {
					ui.widgets.newSyncDevice.currentStep.Text = "Waiting for network..."
					ui.widgets.newSyncDevice.currentStep.Refresh()
					ui.widgets.newSyncDevice.infiniteProgressBar.Show()
				})
				for {
					time.Sleep(500 * time.Millisecond) // TODO: use a callback for this?
					if ui.state.networkState == networkStateOnline {
						break
					}
				}
			}

			fyne.Do(func() {
				ui.widgets.newSyncDevice.currentStep.Text = "Sending sync request, this may take a minute..."
				ui.widgets.newSyncDevice.currentStep.Refresh()
				ui.widgets.newSyncDevice.infiniteProgressBar.Show()
			})

			err := ui.bounce.RequestToSync(str)
			if err == nil {
				fyne.Do(func() {
					ui.widgets.newSyncDevice.currentStep.Text = "Waiting for sync response..."
					ui.widgets.newSyncDevice.currentStep.Refresh()
				})
			} else {
				fyne.Do(func() {
					ui.state.initialSyncIncomplete = false
					ui.widgets.newSyncDevice.infiniteProgressBar.Hide()
					ui.widgets.newSyncDevice.currentStep.Text = ""
					ui.widgets.newSyncDevice.currentStep.Hide()
					ui.widgets.newSyncDevice.syncStringEntry.Text = ""
					ui.widgets.newSyncDevice.syncStringEntry.Refresh()
					ui.widgets.newSyncDevice.backButton.Enable()
					ui.widgets.newSyncDevice.syncStringInput.Show()
					ui.showDialog(dialog.NewError(errors.New("Error sending sync request: "+err.Error()), ui.window), func() {
						if isCameraDevice {
							cameraDevice.StartPreview()
							cameraIsRunning = true
							go ui.processCameraFramesForNewSyncDevice()
						}
					})
				})
			}
		}()
	}

	ui.views.newSyncDevice = container.New(
		layout.NewBorderLayout(header, actionButtons, nil, nil),
		header,
		actionButtons,
		container.New(
			newPaddedCenterLayout(logo),
			container.NewVBox(
				container.NewCenter(ui.widgets.newSyncDevice.currentStep),
				ui.widgets.newSyncDevice.progressBars,
			),
		),
	)
}

func (ui *ui) SyncDeviceRequestAccepted(profile chat.User, devices []chat.Device, references bool) {
	u := makeUser(profile)
	ui.state.profile = u
	ui.users.add(u)

	for i, _ := range devices {
		dev := devices[i]
		ui.devices.add(&dev)
	}
	ui.state.initialStateSet = true
	fyne.DoAndWait(func() { ui.updateDeviceStatus() })

	if !references {
		fyne.DoAndWait(func() { ui.showMainContainer() })
	}
}

func (ui *ui) SyncDeviceRequestRejected(peer string) {
	cameraDevice, isCameraDevice := fyne.CurrentDevice().(sensor.CameraDevice)
	fyne.DoAndWait(func() {
		ui.showDialog(dialog.NewError(errors.New("Sync request rejected, make sure you scan the device quickly"), ui.window), func() {
			if isCameraDevice && !cameraIsRunning {
				cameraDevice.StartPreview()
				cameraIsRunning = true
				go ui.processCameraFramesForNewSyncDevice()
			}
		})
		ui.widgets.newSyncDevice.currentStep.Text = ""
		ui.widgets.newSyncDevice.currentStep.Refresh()
		ui.widgets.newSyncDevice.currentStep.Hide()
		ui.widgets.newSyncDevice.infiniteProgressBar.Hide()
		ui.widgets.newSyncDevice.progressBar.Hide()
		ui.widgets.newSyncDevice.backButton.Enable()
		ui.widgets.newSyncDevice.syncStringInput.Show()
	})
}

func (ui *ui) InitialSyncStarting() {
	fyne.DoAndWait(func() {
		ui.widgets.newSyncDevice.currentStep.Text = "Importing data..."
		ui.widgets.newSyncDevice.currentStep.Refresh()
		ui.widgets.newSyncDevice.infiniteProgressBar.Hide()
		ui.widgets.newSyncDevice.progressBar.Show()
	})
}

func (ui *ui) InitialSyncProgress(p float64) {
	fyne.DoAndWait(func() { ui.widgets.newSyncDevice.progressBar.SetValue(p) })
}

func (ui *ui) InitialSyncComplete() {
	var newDeviceName string
	fyne.DoAndWait(func() {
		ui.state.initialSyncIncomplete = false // Allow notifications and dialogs
		ui.showMainContainer()

		newDeviceName = strings.TrimSpace(ui.widgets.newSyncDevice.deviceNameEntry.Text)
		if newDeviceName == "" {
			newDeviceName = strings.TrimSpace(ui.widgets.newSyncDevice.deviceNameEntry.PlaceHolder)
		}
	})

	if newDeviceName != "" {
		if ui.devices.local == nil {
			log.Error("local device is nil after initial sync")
		} else {
			err := ui.bounce.RenameDevice(ui.devices.local.ID, strings.TrimSpace(newDeviceName))
			if err != nil {
				fyne.DoAndWait(func() {
					ui.showDialog(dialog.NewError(errors.New("Error setting new device name: "+err.Error()), ui.window), nil)
				})
			}
		}
	}
}

func (ui *ui) processCameraFramesForNewSyncDevice() {
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
				ui.widgets.newSyncDevice.videoStream.Image = img
				ui.widgets.newSyncDevice.videoStream.Refresh()
			})
			select {
			case newSyncDeviceScanner <- img:
			default:
			}
		case <-stopNewSyncDeviceCameraProcessing:
			return
		}
	}
}
