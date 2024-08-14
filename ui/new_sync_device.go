package ui

import (
	"errors"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
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

func (fyneUI *Fyne) buildNewSyncDeviceWidgets() {
	progressBar := widget.NewProgressBar()
	infiniteProgressBar := widget.NewProgressBarInfinite()
	progressBars := container.NewStack(
		infiniteProgressBar,
		progressBar,
	)

	fyneUI.newSyncDeviceWidgets = &newSyncDeviceWidgets{
		currentStep:         widget.NewLabel(""),
		progressBars:        progressBars,
		progressBar:         progressBar,
		infiniteProgressBar: infiniteProgressBar,
		backButton: widget.NewButton("Back", func() {
			if fyne.CurrentDevice().IsMobile() {
				fyneUI.mobileBack()
			} else {
				fyneUI.mainWindow.SetContent(fyneUI.nameNewDevice)
				fyneUI.nameNewDevice.Show()
			}
		}),
		deviceNameEntry: widget.NewEntry(),
		syncStringEntry: widget.NewEntry(),
	}
	fyneUI.newSyncDeviceWidgets.currentStep.Alignment = fyne.TextAlignCenter

	syncStringEntryLabel := widget.NewLabel("Paste in the string to pair this device with an exiting profile")
	syncStringEntryLabel.Alignment = fyne.TextAlignCenter

	syncButton := widget.NewButton("Sync", func() {
		fyneUI.newSyncDeviceWidgets.syncStringEntry.OnSubmitted(fyneUI.newSyncDeviceWidgets.syncStringEntry.Text)
	})
	syncButton.Importance = widget.HighImportance
	fyneUI.newSyncDeviceWidgets.syncStringInput = container.NewVBox(
		syncStringEntryLabel,
		container.New(
			layout.NewBorderLayout(nil, nil, nil, syncButton),
			syncButton,
			fyneUI.newSyncDeviceWidgets.syncStringEntry,
		),
	)

	fyneUI.newSyncDeviceWidgets.deviceNameEntry.OnChanged = func(str string) {
		// Remove any leading whitespace
		str, trimmed := trimLeadingSpace(str)
		fyneUI.newSyncDeviceWidgets.deviceNameEntry.Text = str
		if trimmed != 0 {
			fyneUI.newSyncDeviceWidgets.deviceNameEntry.CursorRow = 0
			fyneUI.newSyncDeviceWidgets.deviceNameEntry.CursorColumn = 0
		}

		// Enforce length limit
		if utf8.RuneCountInString(str) > chat.MaximumNameLength {
			runes := []rune(str)
			truncated := runes[0:chat.MaximumNameLength]
			fyneUI.newSyncDeviceWidgets.deviceNameEntry.Text = string(truncated)
		}
		fyneUI.newSyncDeviceWidgets.deviceNameEntry.Refresh()
	}
}

func (fyneUI *Fyne) showNameNewDevice() {
	if fyne.CurrentDevice().IsMobile() {
		fyneUI.viewStack = append(fyneUI.viewStack, view{viewType: viewTypeNameNewDevice})
	}
	fyneUI.mainWindow.SetContent(fyneUI.nameNewDevice)
	fyneUI.nameNewDevice.Show()
	fyneUI.mainWindow.Canvas().Focus(fyneUI.newSyncDeviceWidgets.deviceNameEntry)
}

func (fyneUI *Fyne) buildNameNewDevice() {
	logo := canvas.NewImageFromResource(newEmbeddedResource("assets/logo.png"))
	logo.FillMode = canvas.ImageFillContain
	// TODO: choose reasonable values here
	// https://github.com/fyne-io/fyne/blob/v2.0.3/cmd/fyne_demo/tutorials/welcome.go#L25
	logo.SetMinSize(fyne.NewSize(228, 167))

	nameLabel := widget.NewLabel("Give this new device a name")
	nameLabel.Alignment = fyne.TextAlignCenter
	header := container.NewVBox(
		container.NewCenter(logo),
		container.New(
			newPaddedCenterLayout(nil),
			container.NewVBox(
				nameLabel,
				fyneUI.newSyncDeviceWidgets.deviceNameEntry,
			),
		),
	)

	hostname, err := os.Hostname()
	if err == nil {
		fyneUI.newSyncDeviceWidgets.deviceNameEntry.PlaceHolder = hostname
	}
	fyneUI.newSyncDeviceWidgets.deviceNameEntry.OnSubmitted = func(_ string) {
		fyneUI.showNewSyncDevice()
	}

	backButton := widget.NewButton("Back", func() {
		if fyne.CurrentDevice().IsMobile() {
			fyneUI.mobileBack()
		} else {
			fyneUI.mainWindow.SetContent(fyneUI.newInstall)
			fyneUI.newInstall.Show()
		}
	})

	nextButton := widget.NewButton("Next", func() {
		fyneUI.showNewSyncDevice()
	})
	nextButton.Importance = widget.HighImportance

	actionButtons := container.New(
		layout.NewBorderLayout(nil, nil, backButton, nextButton),
		backButton,
		nextButton,
	)

	fyneUI.nameNewDevice = container.New(
		layout.NewBorderLayout(header, actionButtons, nil, nil),
		header,
		actionButtons,
	)
}

func (fyneUI *Fyne) showNewSyncDevice() {
	fyneUI.newSyncDeviceWidgets.syncStringInput.Show()
	if fyne.CurrentDevice().IsMobile() {
		fyneUI.viewStack = append(fyneUI.viewStack, view{viewType: viewTypeNewSyncDevice})
	}
	fyneUI.mainWindow.SetContent(fyneUI.newSyncDevice)
	fyneUI.newSyncDevice.Show()
}

func (fyneUI *Fyne) buildNewSyncDevice() {
	logo := canvas.NewImageFromResource(newEmbeddedResource("assets/logo.png"))
	logo.FillMode = canvas.ImageFillContain
	// TODO: choose reasonable values here
	// https://github.com/fyne-io/fyne/blob/v2.0.3/cmd/fyne_demo/tutorials/welcome.go#L25
	logo.SetMinSize(fyne.NewSize(228, 167))
	header := container.NewVBox(
		container.NewCenter(logo),
		container.New(
			newPaddedCenterLayout(nil),
			fyneUI.newSyncDeviceWidgets.syncStringInput,
		),
	)

	actionButtons := container.New(
		layout.NewBorderLayout(nil, nil, fyneUI.newSyncDeviceWidgets.backButton, nil),
		fyneUI.newSyncDeviceWidgets.backButton,
	)

	fyneUI.newSyncDeviceWidgets.currentStep.Hide()
	fyneUI.newSyncDeviceWidgets.infiniteProgressBar.Hide()
	fyneUI.newSyncDeviceWidgets.progressBar.Hide()

	fyneUI.newSyncDeviceWidgets.syncStringEntry.OnSubmitted = func(str string) {
		fyneUI.newSyncDeviceWidgets.syncStringInput.Hide()
		fyneUI.newSyncDeviceWidgets.backButton.Disable()
		fyneUI.initialSyncIncomplete = true
		// TODO: change logo to loading version

		fyneUI.newSyncDeviceWidgets.currentStep.Show()
		if fyneUI.networkState == networkStateOffline {
			fyneUI.newSyncDeviceWidgets.currentStep.Text = "Waiting for network..."
			fyneUI.newSyncDeviceWidgets.currentStep.Refresh()
			fyneUI.newSyncDeviceWidgets.infiniteProgressBar.Show()
			for {
				time.Sleep(500 * time.Millisecond) // TODO: use a callback for this?
				if fyneUI.networkState == networkStateOnline {
					break
				}
			}
		}

		fyneUI.newSyncDeviceWidgets.currentStep.Text = "Sending sync request..."
		fyneUI.newSyncDeviceWidgets.currentStep.Refresh()
		fyneUI.newSyncDeviceWidgets.infiniteProgressBar.Show()

		err := fyneUI.callbacks.RequestToSync(str)
		if err != nil {
			fyneUI.initialSyncIncomplete = false
			fyneUI.newSyncDeviceWidgets.infiniteProgressBar.Hide()
			fyneUI.newSyncDeviceWidgets.currentStep.Text = ""
			fyneUI.newSyncDeviceWidgets.currentStep.Hide()
			fyneUI.newSyncDeviceWidgets.syncStringEntry.Text = ""
			fyneUI.newSyncDeviceWidgets.syncStringEntry.Refresh()
			dialog.ShowError(errors.New("Error sending sync request: "+err.Error()), fyneUI.mainWindow)
			fyneUI.newSyncDeviceWidgets.backButton.Enable()
			fyneUI.newSyncDeviceWidgets.syncStringInput.Show()
		} else {
			fyneUI.newSyncDeviceWidgets.currentStep.Text = "Waiting for sync response..."
			fyneUI.newSyncDeviceWidgets.currentStep.Refresh()
		}
	}

	fyneUI.newSyncDevice = container.New(
		layout.NewBorderLayout(header, actionButtons, nil, nil),
		header,
		actionButtons,
		container.New(
			newPaddedCenterLayout(logo),
			container.NewVBox(
				container.NewCenter(fyneUI.newSyncDeviceWidgets.currentStep),
				fyneUI.newSyncDeviceWidgets.progressBars,
			),
		),
	)
}

func (fyneUI *Fyne) SyncDeviceRequestAccepted(id uuid.UUID, name string, devices []chat.Device, references bool) {
	profile := makeUser(id, name)
	fyneUI.profile = profile
	fyneUI.users.add(profile)
	for i, _ := range devices {
		dev := devices[i]
		fyneUI.devices.add(&dev)
	}
	fyneUI.initialStateSet = true
	fyneUI.updateDeviceStatus()

	if !references {
		fyneUI.showMainContainer()
	}
}

func (fyneUI *Fyne) SyncDeviceRequestRejected(peer string) {
	dialog.ShowError(errors.New("Sync request rejected, make sure you scan the device quickly"), fyneUI.mainWindow)
	fyneUI.newSyncDeviceWidgets.currentStep.Text = ""
	fyneUI.newSyncDeviceWidgets.currentStep.Refresh()
	fyneUI.newSyncDeviceWidgets.currentStep.Hide()
	fyneUI.newSyncDeviceWidgets.infiniteProgressBar.Hide()
	fyneUI.newSyncDeviceWidgets.progressBar.Hide()
	fyneUI.newSyncDeviceWidgets.backButton.Enable()
	fyneUI.newSyncDeviceWidgets.syncStringInput.Show()
}

func (fyneUI *Fyne) InitialSyncStarting() {
	fyneUI.newSyncDeviceWidgets.currentStep.Text = "Importing data..."
	fyneUI.newSyncDeviceWidgets.currentStep.Refresh()
	fyneUI.newSyncDeviceWidgets.infiniteProgressBar.Hide()
	fyneUI.newSyncDeviceWidgets.progressBar.Show()
}

func (fyneUI *Fyne) InitialSyncProgress(p float64) {
	fyneUI.newSyncDeviceWidgets.progressBar.SetValue(p)
}

func (fyneUI *Fyne) InitialSyncComplete() {
	fyneUI.initialSyncIncomplete = false // Allow notifications and dialogs
	fyneUI.showMainContainer()

	newDeviceName := fyneUI.newSyncDeviceWidgets.deviceNameEntry.Text
	if newDeviceName != "" {
		if fyneUI.devices.local == nil {
			log.Error("local device is nil after initial sync")
		} else {
			err := fyneUI.callbacks.RenameDevice(fyneUI.devices.local.ID, strings.TrimSpace(newDeviceName))
			if err != nil {
				dialog.ShowError(errors.New("Error setting new device name: "+err.Error()), fyneUI.mainWindow)
			}
		}
	}
}
