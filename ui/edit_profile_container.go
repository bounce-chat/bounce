package ui

import (
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
	log "github.com/sirupsen/logrus"
)

type expirationSelection struct {
	display       string
	unixIncrement int64
}

var expirationOneHour = expirationSelection{
	display:       "1 Hour",
	unixIncrement: int64(time.Duration(1 * time.Hour).Seconds()),
}

var expirationOneDay = expirationSelection{
	display:       "1 Day",
	unixIncrement: int64(time.Duration(24 * time.Hour).Seconds()),
}

var expirationOneWeek = expirationSelection{
	display:       "1 Week",
	unixIncrement: int64(time.Duration(24 * time.Hour * 7).Seconds()),
}

var expirationNever = expirationSelection{
	display: "Never",
}

var expirationSelections = []string{expirationOneHour.display, expirationOneDay.display, expirationOneWeek.display, expirationNever.display}
var expirationIncrements = map[string]int64{
	expirationOneHour.display: expirationOneHour.unixIncrement,
	expirationOneDay.display:  expirationOneDay.unixIncrement,
	expirationOneWeek.display: expirationOneWeek.unixIncrement,
}

func (ui *ui) showEditProfile() {
	if fyne.CurrentDevice().IsMobile() {
		ui.viewStack = append(ui.viewStack, view{viewType: viewTypeEditProfile})
	}

	ui.profileIcon.id = ui.profile.id
	ui.profileIcon.images = ui.profile.images
	initials, err := ui.profile.initials.Get()
	if err != nil {
		log.Fatal("data bindings are broken")
	}
	ui.profileIcon.foregroundText.Text = initials
	ui.profileIcon.Refresh()

	ui.profileNameEntry.Text = ui.profile.getName()
	ui.profileNameEntry.Refresh()
	ui.profileNameEntry.FocusLost()

	ui.profileOptions.Refresh()
	ui.mainWindow.SetContent(ui.editProfile)
	ui.editProfile.Show()
}

func (ui *ui) updateDeviceStatus() {
	currentDevicesList := container.NewVBox()
	for _, dev := range ui.devices.all() {
		editDeviceDialog, editDeviceCleanup, state := func(dev chat.Device) (dialog.Dialog, func(), int) {
			state := deviceStatusOffline
			if dev.Online {
				state = deviceStatusOnline
			}
			if dev.Local {
				state = deviceStatusLocal
			}

			shortName := dev.Name // TODO: truncate if it's too long
			if shortName == "" {
				shortName = dev.Address[0:8] + "..."
			}

			nameLabel := widget.NewLabel("Name:")
			nameEntry := widget.NewEntry()
			nameEntry.OnChanged = func(str string) {
				// Remove any leading whitespace
				str, trimmed := trimLeadingSpace(str)
				nameEntry.Text = str
				if trimmed != 0 {
					nameEntry.CursorRow = 0
					nameEntry.CursorColumn = 0
				}

				// Enforce length limit
				if utf8.RuneCountInString(str) > chat.MaximumNameLength {
					runes := []rune(str)
					truncated := runes[0:chat.MaximumNameLength]
					nameEntry.Text = string(truncated)
				}
				nameEntry.Refresh()
			}
			if dev.Name != "" {
				nameEntry.SetText(dev.Name)
			}

			addressLabel := widget.NewLabel("Address:")
			addressEntry := widget.NewEntry()
			addressEntry.SetText(dev.Address)
			addressEntry.Disable()

			lastSeenLabel := widget.NewLabel("Last Seen:")
			var lastSeenString string
			if !dev.Local {
				if dev.Online {
					lastSeenString = "now"
				} else if dev.LastSeen == 0 {
					lastSeenString = "never"
				} else {
					lastSeenString = time.Unix(dev.LastSeen, 0).Format("1/2 2006 3:04PM")
				}
			}

			createdAtLabel := widget.NewLabel("Created:")
			createdAtString := time.Unix(dev.CreatedAt, 0).Format("1/2 2006")

			var editDeviceDialog dialog.Dialog
			var showError error
			var hideEditDialog bool
			confirmRevokeCleanup := func() {
				if hideEditDialog {
					editDeviceDialog.Hide()
				}
				if showError != nil {
					ui.showDialog(dialog.NewError(showError, ui.mainWindow), nil)
				}
			}
			confirmRevokeDialog := dialog.NewConfirm(
				"Revoke Device?",
				"Are you sure you want to permanently revoke this device?",
				func(confirmed bool) {
					showError = nil
					hideEditDialog = confirmed
					if confirmed {
						showError = ui.bounce.RevokeDevice(dev.ID)
						if showError != nil {
							hideEditDialog = false
						}
					}
				},
				ui.mainWindow,
			)
			revokeButton := widget.NewButton("Revoke", func() {
				ui.showDialog(confirmRevokeDialog, confirmRevokeCleanup)
			})
			revokeButton.Importance = widget.DangerImportance

			editDeviceContainer := container.NewVBox(
				container.New(
					layout.NewBorderLayout(nil, nil, nameLabel, nil),
					nameLabel,
					nameEntry,
				),
				container.New(
					layout.NewBorderLayout(nil, nil, addressLabel, nil),
					addressLabel,
					addressEntry,
				),
			)
			if !dev.Local {
				editDeviceContainer.Add(
					container.New(
						layout.NewBorderLayout(nil, nil, lastSeenLabel, nil),
						lastSeenLabel,
						widget.NewLabel(lastSeenString),
					),
				)
			}
			editDeviceContainer.Add(
				container.New(
					layout.NewBorderLayout(nil, nil, createdAtLabel, nil),
					createdAtLabel,
					widget.NewLabel(createdAtString),
				),
			)
			editDeviceContainer.Add(
				revokeButton,
			)

			var applied bool
			editDeviceCleanup := func() {
				if !applied {
					nameEntry.Text = dev.Name
				}
				if showError != nil {
					ui.showDialog(dialog.NewError(showError, ui.mainWindow), nil)
				}
			}
			editDeviceDialog = dialog.NewCustomConfirm(shortName, "Apply", "Cancel", editDeviceContainer, func(apply bool) {
				applied = apply
				showError = nil
				if apply {
					if dev.Name != nameEntry.Text {
						showError = ui.bounce.RenameDevice(dev.ID, strings.TrimSpace(nameEntry.Text))
					}
				}
			}, ui.mainWindow)

			return editDeviceDialog, editDeviceCleanup, state
		}(*dev)

		currentDevicesList.Objects = append(
			currentDevicesList.Objects,
			newDeviceButton(
				dev.Name,
				dev.Address,
				state,
				false,
				false,
				func() {
					ui.showDialog(editDeviceDialog, editDeviceCleanup)
				},
			),
		)
	}
	ui.currentDevices.Content = currentDevicesList

	currentDevicesHeight := float32(0)
	for i, obj := range currentDevicesList.Objects {
		if i == 3 {
			break
		}
		currentDevicesHeight += obj.MinSize().Height
	}
	currentDevicesHeight += theme.Padding() * float32(len(currentDevicesList.Objects)+1)
	ui.currentDevices.SetMinSize(fyne.Size{Height: currentDevicesHeight})

	ui.currentDevices.Refresh()
	ui.editProfile.Refresh()
}

func (ui *ui) buildEditProfile() {
	//
	// Personal details section
	//
	ui.profileIcon = newDefaultImage(uuid.Nil, []uuid.UUID{}, binding.NewString(), 128, ui.bounce.GetFileData, func() {
		dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
			if reader == nil {
				return
			}

			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Debug("error selecting new profile image")
				return
			}

			var size int64
			size, reader, err = fileSizeInReader(reader)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error getting file size")
				reader.Close()
				return
			}
			if size > chat.EmbeddedFileLimit {
				ui.showDialog(dialog.NewError(errors.New("file is too large"), ui.mainWindow), nil)
				reader.Close()
				return
			}

			data, err := io.ReadAll(reader)
			reader.Close()
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Debug("error reading new profile image")
				return
			}

			// TODO: make sure data is a valid image, allow for editing, etc
			// TODO: don't actualy set it until they hit save?

			ui.bounce.UpdateProfileImage(data)
		}, ui.mainWindow).Show() // We do not use showDialog here because on mobile this uses a native intent
	})

	ui.profileNameEntry = widget.NewEntry()
	ui.profileNameEntry.OnChanged = func(str string) {
		// Remove any leading whitespace
		str, trimmed := trimLeadingSpace(str)
		ui.profileNameEntry.Text = str
		if trimmed != 0 {
			ui.profileNameEntry.CursorRow = 0
			ui.profileNameEntry.CursorColumn = 0
		}

		// Enforce length limit
		if utf8.RuneCountInString(str) > chat.MaximumNameLength {
			runes := []rune(str)
			truncated := runes[0:chat.MaximumNameLength]
			ui.profileNameEntry.Text = string(truncated)

		}
		ui.profileNameEntry.Refresh()
	}

	saveProfileButton := widget.NewButton("Update", func() {
		err := ui.bounce.UpdateProfileName(strings.TrimSpace(ui.profileNameEntry.Text))
		if err != nil {
			ui.showDialog(dialog.NewError(errors.New("error updating name: "+err.Error()), ui.mainWindow), nil)
		} else {
			ui.showMainContainer()
			t, ok := ui.threads.get(ui.activeThread)
			if ok {
				ui.mainWindow.Canvas().Focus(t.getEntry())
			}
		}
	})
	saveProfileButton.Importance = widget.HighImportance

	saveProfileButtonBar := container.New(
		layout.NewBorderLayout(nil, nil, nil, saveProfileButton),
		saveProfileButton,
	)

	ui.profileOptions = container.NewVBox(
		container.NewCenter(ui.profileIcon),
		ui.profileNameEntry,
		saveProfileButtonBar,
	)

	//
	// Sync devices secttion
	//

	devicesLabel := widget.NewLabel("Devices")
	devicesLabel.TextStyle = fyne.TextStyle{Bold: true}

	ui.currentDevices = container.NewVScroll(container.NewVBox())

	addDeviceButton := widget.NewButton("Add Device", func() {
		ui.showDisplaySyncString()
	})
	addDeviceButton.Importance = widget.HighImportance
	addDeviceButtonBar := container.New(
		layout.NewBorderLayout(nil, nil, nil, addDeviceButton),
		addDeviceButton,
	)

	devicesContainer := container.NewVBox(
		ui.currentDevices,
		addDeviceButtonBar,
	)

	//
	// Layout of all sections
	//

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
	nonScrollingContainers := container.NewVBox(
		ui.profileOptions,
		devicesLabel,
		devicesContainer,
	)
	ui.editProfile = container.New(
		layout.NewBorderLayout(closeBar, nil, nil, nil),
		closeBar,
		nonScrollingContainers,
	)
}
