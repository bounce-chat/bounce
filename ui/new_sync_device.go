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
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
	log "github.com/sirupsen/logrus"
)

type newSyncDeviceWidgets struct {
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

	ui.newSyncDeviceWidgets = &newSyncDeviceWidgets{
		currentStep:         widget.NewLabel(""),
		progressBars:        progressBars,
		progressBar:         progressBar,
		infiniteProgressBar: infiniteProgressBar,
		backButton: widget.NewButton("Back", func() {
			if fyne.CurrentDevice().IsMobile() {
				ui.mobileBack()
			} else {
				ui.mainWindow.SetContent(ui.nameNewDevice)
				ui.nameNewDevice.Show()
			}
		}),
		deviceNameEntry: widget.NewEntry(),
		syncStringEntry: widget.NewEntry(),
	}
	ui.newSyncDeviceWidgets.currentStep.Alignment = fyne.TextAlignCenter

	syncStringEntryLabel := widget.NewLabel("Paste in the string to pair this device with an exiting profile")
	syncStringEntryLabel.Alignment = fyne.TextAlignCenter

	syncButton := widget.NewButton("Sync", func() {
		ui.newSyncDeviceWidgets.syncStringEntry.OnSubmitted(ui.newSyncDeviceWidgets.syncStringEntry.Text)
	})
	syncButton.Importance = widget.HighImportance
	ui.newSyncDeviceWidgets.syncStringInput = container.NewVBox(
		syncStringEntryLabel,
		container.New(
			layout.NewBorderLayout(nil, nil, nil, syncButton),
			syncButton,
			ui.newSyncDeviceWidgets.syncStringEntry,
		),
	)

	ui.newSyncDeviceWidgets.deviceNameEntry.OnChanged = func(str string) {
		// Remove any leading whitespace
		str, trimmed := trimLeadingSpace(strings.TrimSpace(str))
		ui.newSyncDeviceWidgets.deviceNameEntry.Text = str
		if trimmed != 0 {
			ui.newSyncDeviceWidgets.deviceNameEntry.CursorRow = 0
			ui.newSyncDeviceWidgets.deviceNameEntry.CursorColumn = 0
		}

		// Enforce length limit
		if utf8.RuneCountInString(str) > chat.MaximumNameLength {
			runes := []rune(str)
			truncated := runes[0:chat.MaximumNameLength]
			ui.newSyncDeviceWidgets.deviceNameEntry.Text = string(truncated)
		}
		ui.newSyncDeviceWidgets.deviceNameEntry.Refresh()
	}
}

func (ui *ui) showNameNewDevice() {
	if fyne.CurrentDevice().IsMobile() {
		ui.viewStack = append(ui.viewStack, view{viewType: viewTypeNameNewDevice})
	}
	ui.mainWindow.SetContent(ui.nameNewDevice)
	ui.nameNewDevice.Show()
	ui.mainWindow.Canvas().Focus(ui.newSyncDeviceWidgets.deviceNameEntry)
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
				ui.newSyncDeviceWidgets.deviceNameEntry,
			),
		),
	)

	hostname, err := os.Hostname()
	if err == nil {
		ui.newSyncDeviceWidgets.deviceNameEntry.PlaceHolder = hostname
	}
	ui.newSyncDeviceWidgets.deviceNameEntry.OnSubmitted = func(_ string) {
		ui.showNewSyncDevice()
	}

	backButton := widget.NewButton("Back", func() {
		if fyne.CurrentDevice().IsMobile() {
			ui.mobileBack()
		} else {
			ui.mainWindow.SetContent(ui.newInstall)
			ui.newInstall.Show()
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

	ui.nameNewDevice = container.New(
		layout.NewBorderLayout(header, actionButtons, nil, nil),
		header,
		actionButtons,
	)
}

func (ui *ui) showNewSyncDevice() {
	ui.newSyncDeviceWidgets.syncStringInput.Show()
	if fyne.CurrentDevice().IsMobile() {
		ui.viewStack = append(ui.viewStack, view{viewType: viewTypeNewSyncDevice})
	}
	ui.mainWindow.SetContent(ui.newSyncDevice)
	ui.newSyncDevice.Show()
}

func (ui *ui) buildNewSyncDevice() {
	logo := makeLogo(228, 167) // TODO: choose reasonable values here, https://github.com/fyne-io/fyne/blob/v2.0.3/cmd/fyne_demo/tutorials/welcome.go#L25
	header := container.NewVBox(
		container.NewCenter(logo),
		container.New(
			newPaddedCenterLayout(nil),
			ui.newSyncDeviceWidgets.syncStringInput,
		),
	)

	actionButtons := container.New(
		layout.NewBorderLayout(nil, nil, ui.newSyncDeviceWidgets.backButton, nil),
		ui.newSyncDeviceWidgets.backButton,
	)

	ui.newSyncDeviceWidgets.currentStep.Hide()
	ui.newSyncDeviceWidgets.infiniteProgressBar.Hide()
	ui.newSyncDeviceWidgets.progressBar.Hide()

	ui.newSyncDeviceWidgets.syncStringEntry.OnSubmitted = func(str string) {
		ui.newSyncDeviceWidgets.syncStringInput.Hide()
		ui.newSyncDeviceWidgets.backButton.Disable()
		ui.initialSyncIncomplete = true
		// TODO: change logo to loading version
		ui.newSyncDeviceWidgets.currentStep.Show()
		go func() {
			if ui.networkState == networkStateStarting || ui.networkState == networkStateOffline {
				fyne.Do(func() {
					ui.newSyncDeviceWidgets.currentStep.Text = "Waiting for network..."
					ui.newSyncDeviceWidgets.currentStep.Refresh()
					ui.newSyncDeviceWidgets.infiniteProgressBar.Show()
				})
				for {
					time.Sleep(500 * time.Millisecond) // TODO: use a callback for this?
					if ui.networkState == networkStateOnline {
						break
					}
				}
			}

			fyne.Do(func() {
				ui.newSyncDeviceWidgets.currentStep.Text = "Sending sync request..."
				ui.newSyncDeviceWidgets.currentStep.Refresh()
				ui.newSyncDeviceWidgets.infiniteProgressBar.Show()
			})

			err := ui.bounce.RequestToSync(str)
			fyne.Do(func() {
				if err != nil {
					ui.initialSyncIncomplete = false
					ui.newSyncDeviceWidgets.infiniteProgressBar.Hide()
					ui.newSyncDeviceWidgets.currentStep.Text = ""
					ui.newSyncDeviceWidgets.currentStep.Hide()
					ui.newSyncDeviceWidgets.syncStringEntry.Text = ""
					ui.newSyncDeviceWidgets.syncStringEntry.Refresh()
					ui.showDialog(dialog.NewError(errors.New("Error sending sync request: "+err.Error()), ui.mainWindow), nil)
					ui.newSyncDeviceWidgets.backButton.Enable()
					ui.newSyncDeviceWidgets.syncStringInput.Show()
				} else {
					ui.newSyncDeviceWidgets.currentStep.Text = "Waiting for sync response..."
					ui.newSyncDeviceWidgets.currentStep.Refresh()
				}
			})
		}()
	}

	ui.newSyncDevice = container.New(
		layout.NewBorderLayout(header, actionButtons, nil, nil),
		header,
		actionButtons,
		container.New(
			newPaddedCenterLayout(logo),
			container.NewVBox(
				container.NewCenter(ui.newSyncDeviceWidgets.currentStep),
				ui.newSyncDeviceWidgets.progressBars,
			),
		),
	)
}

func (ui *ui) SyncDeviceRequestAccepted(id uuid.UUID, name string, devices []chat.Device, references bool) {
	profile := makeUser(id, name)
	ui.profile = profile
	ui.users.add(profile)
	for i, _ := range devices {
		dev := devices[i]
		ui.devices.add(&dev)
	}
	ui.initialStateSet = true
	ui.updateDeviceStatus()

	if !references {
		fyne.DoAndWait(func() { ui.showMainContainer() })
	}
}

func (ui *ui) SyncDeviceRequestRejected(peer string) {
	fyne.DoAndWait(func() {
		ui.showDialog(dialog.NewError(errors.New("Sync request rejected, make sure you scan the device quickly"), ui.mainWindow), nil)
		ui.newSyncDeviceWidgets.currentStep.Text = ""
		ui.newSyncDeviceWidgets.currentStep.Refresh()
		ui.newSyncDeviceWidgets.currentStep.Hide()
		ui.newSyncDeviceWidgets.infiniteProgressBar.Hide()
		ui.newSyncDeviceWidgets.progressBar.Hide()
		ui.newSyncDeviceWidgets.backButton.Enable()
		ui.newSyncDeviceWidgets.syncStringInput.Show()
	})
}

func (ui *ui) InitialSyncStarting() {
	fyne.DoAndWait(func() {
		ui.newSyncDeviceWidgets.currentStep.Text = "Importing data..."
		ui.newSyncDeviceWidgets.currentStep.Refresh()
		ui.newSyncDeviceWidgets.infiniteProgressBar.Hide()
		ui.newSyncDeviceWidgets.progressBar.Show()
	})
}

func (ui *ui) InitialSyncProgress(p float64) {
	fyne.DoAndWait(func() { ui.newSyncDeviceWidgets.progressBar.SetValue(p) })
}

func (ui *ui) InitialSyncComplete() {
	var newDeviceName string
	fyne.DoAndWait(func() {
		ui.initialSyncIncomplete = false // Allow notifications and dialogs
		ui.showMainContainer()

		newDeviceName = ui.newSyncDeviceWidgets.deviceNameEntry.Text
	})

	if newDeviceName != "" {
		if ui.devices.local == nil {
			log.Error("local device is nil after initial sync")
		} else {
			err := ui.bounce.RenameDevice(ui.devices.local.ID, strings.TrimSpace(newDeviceName))
			if err != nil {
				fyne.DoAndWait(func() {
					ui.showDialog(dialog.NewError(errors.New("Error setting new device name: "+err.Error()), ui.mainWindow), nil)
				})
			}
		}
	}
}
