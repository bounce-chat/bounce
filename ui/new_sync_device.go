package ui

import (
	"errors"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/hkparker/bounce/chat"
	log "github.com/sirupsen/logrus"
)

type newSyncDevice struct {
	currentStep         *widget.Label
	progressBars        *fyne.Container
	progressBar         *widget.ProgressBar
	infiniteProgressBar *widget.ProgressBarInfinite
	backButton          *widget.Button
	deviceNameEntry     *widget.Entry
	syncStringEntry     *widget.Entry
	syncStringInput     *fyne.Container
}

func (ui *ui) buildNewSyncDeviceWidgets() {
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
			if fyne.CurrentDevice().IsMobile() {
				ui.mobileBack()
			} else {
				ui.window.SetContent(ui.views.nameNewDevice)
				ui.views.nameNewDevice.Show()
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
	ui.widgets.newSyncDevice.syncStringInput = container.NewVBox(
		syncStringEntryLabel,
		container.New(
			layout.NewBorderLayout(nil, nil, nil, syncButton),
			syncButton,
			ui.widgets.newSyncDevice.syncStringEntry,
		),
	)

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
}

func (ui *ui) buildNewSyncDevice() {
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
		ui.widgets.newSyncDevice.backButton.Disable()
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
				ui.widgets.newSyncDevice.currentStep.Text = "Sending sync request..."
				ui.widgets.newSyncDevice.currentStep.Refresh()
				ui.widgets.newSyncDevice.infiniteProgressBar.Show()
			})

			err := ui.bounce.RequestToSync(str)
			fyne.Do(func() {
				if err != nil {
					ui.state.initialSyncIncomplete = false
					ui.widgets.newSyncDevice.infiniteProgressBar.Hide()
					ui.widgets.newSyncDevice.currentStep.Text = ""
					ui.widgets.newSyncDevice.currentStep.Hide()
					ui.widgets.newSyncDevice.syncStringEntry.Text = ""
					ui.widgets.newSyncDevice.syncStringEntry.Refresh()
					ui.showDialog(dialog.NewError(errors.New("Error sending sync request: "+err.Error()), ui.window), nil)
					ui.widgets.newSyncDevice.backButton.Enable()
					ui.widgets.newSyncDevice.syncStringInput.Show()
				} else {
					ui.widgets.newSyncDevice.currentStep.Text = "Waiting for sync response..."
					ui.widgets.newSyncDevice.currentStep.Refresh()
				}
			})
		}()
	}
	ui.widgets.newSyncDevice.syncStringEntry.ActionItem = widget.NewButtonWithIcon("", theme.MediaPhotoIcon(), func() {
		str, err := ui.scanQR()
		if str != "" && err == nil {
			ui.widgets.newSyncDevice.syncStringEntry.OnSubmitted(str)
		}
	})

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
	fyne.DoAndWait(func() {
		ui.showDialog(dialog.NewError(errors.New("Sync request rejected, make sure you scan the device quickly"), ui.window), nil)
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
