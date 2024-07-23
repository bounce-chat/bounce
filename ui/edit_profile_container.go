package ui

import (
	"errors"
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

func (fyneUI *Fyne) showEditProfile() {
	if fyne.CurrentDevice().IsMobile() {
		fyneUI.viewStack = append(fyneUI.viewStack, view{viewType: viewTypeEditProfile})
	}
	fyneUI.profileIcon.Objects = []fyne.CanvasObject{newDefaultImage(fyneUI.profile.id, fyneUI.profile.initials, 128, func() {
		log.Info("user wants to change their profile picture")
	})}
	fyneUI.profileIcon.Refresh()

	fyneUI.profileNameEntry.Text = fyneUI.profile.getName()
	fyneUI.profileNameEntry.Refresh()
	fyneUI.profileNameEntry.FocusLost()

	fyneUI.profileOptions.Refresh()
	fyneUI.mainWindow.SetContent(fyneUI.editProfile)
	fyneUI.editProfile.Show()
}

func (fyneUI *Fyne) updateDeviceStatus() {
	currentDevicesList := container.NewVBox()
	for _, dev := range fyneUI.devices.all() {
		editDeviceDialog, state := func(dev chat.Device) (dialog.Dialog, int) {
			state := deviceStatusOffline
			if dev.Online {
				state = deviceStatusOnline
			}
			if dev.Local {
				state = deviceStatusLocal
			}

			shortName := dev.Name
			if shortName == "" {
				shortName = dev.Address[0:8] + "..."
			}

			nameLabel := widget.NewLabel("Name:")
			nameEntry := widget.NewEntry() // TODO: validate string?
			if dev.Name != "" {
				nameEntry.SetText(dev.Name)
			}

			createdAtLabel := widget.NewLabel("Created:")
			createdAtString := time.Unix(dev.CreatedAt, 0).Format("1/2 2006")

			var editDeviceDialog dialog.Dialog
			confirmRevokeDialog := dialog.NewConfirm(
				"Revoke Device?",
				"Are you sure you want to permanently revoke this device?",
				func(confirmed bool) {
					if confirmed {
						fyneUI.callbacks.RevokeDevice(dev.ID)
						editDeviceDialog.Hide()
						fyneUI.activeDialog = nil
						fyneUI.activeDialogCleanup = nil
					} else {
						fyneUI.activeDialog = editDeviceDialog
						fyneUI.activeDialogCleanup = nil
					}
				},
				fyneUI.mainWindow,
			)
			revokeButton := widget.NewButton("Revoke", func() {
				fyneUI.activeDialog = confirmRevokeDialog
				fyneUI.activeDialogCleanup = nil
				confirmRevokeDialog.Show()
			})
			revokeButton.Importance = widget.DangerImportance

			editDeviceContainer := container.NewVBox(
				container.New(
					layout.NewBorderLayout(nil, nil, nameLabel, nil),
					nameLabel,
					nameEntry,
				),
				container.New(
					layout.NewBorderLayout(nil, nil, createdAtLabel, nil),
					createdAtLabel,
					widget.NewLabel(createdAtString),
				),
				revokeButton,
			)
			editDeviceDialog = dialog.NewCustomConfirm(shortName, "Apply", "Cancel", editDeviceContainer, func(apply bool) {
				if apply {
					if dev.Name != nameEntry.Text {
						fyneUI.callbacks.RenameDevice(dev.ID, nameEntry.Text)
					}
				}
				fyneUI.activeDialog = nil
				fyneUI.activeDialogCleanup = nil
			}, fyneUI.mainWindow)

			return editDeviceDialog, state
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
					fyneUI.activeDialog = editDeviceDialog
					fyneUI.activeDialogCleanup = nil
					editDeviceDialog.Show()
				},
			),
		)
	}
	fyneUI.currentDevices.Content = currentDevicesList

	currentDevicesHeight := float32(0)
	for i, obj := range currentDevicesList.Objects {
		if i == 3 {
			break
		}
		currentDevicesHeight += obj.MinSize().Height
	}
	currentDevicesHeight += theme.Padding() * float32(len(currentDevicesList.Objects)+1)
	fyneUI.currentDevices.SetMinSize(fyne.Size{Height: currentDevicesHeight})

	fyneUI.currentDevices.Refresh()
	fyneUI.editProfile.Refresh()
}

func (fyneUI *Fyne) buildEditProfile() {
	//
	// Personal details section
	//
	fyneUI.profileIcon = container.NewCenter()
	fyneUI.profileNameEntry = widget.NewEntry()
	fyneUI.profileNameEntry.OnChanged = func(str string) {
		// Remove any leading whitespace
		str, trimmed := trimLeadingSpace(str)
		fyneUI.profileNameEntry.Text = str
		if trimmed != 0 {
			fyneUI.profileNameEntry.CursorRow = 0
			fyneUI.profileNameEntry.CursorColumn = 0
		}

		// Enforce length limit
		if utf8.RuneCountInString(str) > chat.MaximumNameLength {
			runes := []rune(str)
			truncated := runes[0:chat.MaximumNameLength]
			fyneUI.profileNameEntry.Text = string(truncated)

		}
		fyneUI.profileNameEntry.Refresh()
	}

	saveProfileButton := widget.NewButton("Update", func() {
		err := fyneUI.callbacks.UpdateProfileName(fyneUI.profileNameEntry.Text)
		if err != nil {
			dialog.ShowError(errors.New("error updating name: "+err.Error()), fyneUI.mainWindow)
		} else {
			fyneUI.showMainContainer()
		}
	})
	saveProfileButton.Importance = widget.HighImportance

	saveProfileButtonBar := container.New(
		layout.NewBorderLayout(nil, nil, nil, saveProfileButton),
		saveProfileButton,
	)

	fyneUI.profileOptions = container.NewVBox(
		fyneUI.profileIcon,
		fyneUI.profileNameEntry,
		saveProfileButtonBar,
	)

	//
	// Sync devices secttion
	//

	devicesLabel := widget.NewLabel("Devices")
	devicesLabel.TextStyle = fyne.TextStyle{Bold: true}

	fyneUI.currentDevices = container.NewVScroll(container.NewVBox())

	addDeviceButton := widget.NewButton("Add Device", func() {
		fyneUI.showDisplaySyncString()
	})
	addDeviceButton.Importance = widget.HighImportance
	addDeviceButtonBar := container.New(
		layout.NewBorderLayout(nil, nil, nil, addDeviceButton),
		addDeviceButton,
	)

	devicesContainer := container.NewVBox(
		fyneUI.currentDevices,
		addDeviceButtonBar,
	)

	//
	// Existing exports section
	//

	contactExportsLabel := widget.NewLabel("Contact Exports")
	contactExportsLabel.TextStyle = fyne.TextStyle{Bold: true}

	contactExportContainer := container.NewVScroll(widget.NewLabel("viewing past exports not implemented"))

	//
	// New exports section
	//

	exportLabel := widget.NewLabel("New Export")
	exportLabel.TextStyle = fyne.TextStyle{Bold: true}

	exportNameEntry := widget.NewEntry()
	exportExpirationSelect := widget.NewSelect(expirationSelections, nil)
	exportExpirationSelect.Selected = expirationOneHour.display
	oneTimeUse := widget.NewCheck("One-Time Use", nil)
	oneTimeUse.Checked = true

	exportContactForm := widget.NewForm(
		&widget.FormItem{
			Text:   "Name:",
			Widget: exportNameEntry,
		},
		&widget.FormItem{
			Text:   "Expires:",
			Widget: exportExpirationSelect,
		},
		&widget.FormItem{
			Text:   "",
			Widget: oneTimeUse,
		},
	)
	exportContactForm.SubmitText = "Export"

	fileSelector := dialog.NewFileSave(func(handler fyne.URIWriteCloser, err error) {
		if err != nil {
			dialog.ShowError(errors.New("error opening file: "+err.Error()), fyneUI.mainWindow)
			return
		}
		if handler == nil {
			return
		}

		expiration := int64(0)
		if exportExpirationSelect.Selected != expirationNever.display {
			increment, ok := expirationIncrements[exportExpirationSelect.Selected]
			if !ok {
				log.WithFields(log.Fields{
					"selection": exportExpirationSelect.Selected,
				}).Fatal("unsupported selection for export expiration")
			}
			expiration = time.Now().Unix() + increment
		}

		profileData := fyneUI.callbacks.ExportContact(exportNameEntry.Text, expiration, oneTimeUse.Checked)
		_, err = handler.Write(profileData)
		if err != nil {
			dialog.ShowError(errors.New("error writing file: "+err.Error()), fyneUI.mainWindow)
			return
		}
		err = handler.Close()
		if err != nil {
			dialog.ShowError(errors.New("error closing file: "+err.Error()), fyneUI.mainWindow)
			return
		}

		dialog.ShowInformation("Success", "Contact export written to "+handler.URI().Name(), fyneUI.mainWindow)
	}, fyneUI.mainWindow)

	exportContactForm.OnSubmit = func() {
		if exportNameEntry.Text == "" {
			dialog.ShowError(errors.New("Please select a name for this export"), fyneUI.mainWindow)
			return
		}
		fileSelector.SetFileName("profile.bounce") // TODO: set the user's current profile name
		fileSelector.Show()
	}

	exportContactMenu := container.NewVBox(
		exportLabel,
		exportContactForm,
	)

	//
	// Layout of all sections
	//

	closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		if fyne.CurrentDevice().IsMobile() {
			fyneUI.mobileBack()
		} else {
			fyneUI.showMainContainer()
		}
	})
	closeButton.Importance = widget.LowImportance

	closeBar := container.New(
		layout.NewBorderLayout(nil, nil, nil, closeButton),
		closeButton,
	)
	nonScrollingContainers := container.NewVBox(
		fyneUI.profileOptions,
		devicesLabel,
		devicesContainer,
		contactExportsLabel,
	)
	fyneUI.editProfile = container.New(
		layout.NewBorderLayout(closeBar, exportContactMenu, nil, nil),
		closeBar,
		exportContactMenu,
		container.New(
			layout.NewBorderLayout(nonScrollingContainers, nil, nil, nil),
			nonScrollingContainers, // TODO: a border layout with this at the top and the exports scroll taking up the remaining space
			contactExportContainer,
		),
	)
}
