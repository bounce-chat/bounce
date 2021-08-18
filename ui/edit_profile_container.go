package ui

import (
	"errors"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
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
	display:       "Never",
	unixIncrement: 0,
}

var expirationSelections = []string{expirationOneHour.display, expirationOneDay.display, expirationOneWeek.display, expirationNever.display}
var expirationIncrements = map[string]int64{
	expirationOneHour.display: expirationOneHour.unixIncrement,
	expirationOneDay.display:  expirationOneDay.unixIncrement,
	expirationOneWeek.display: expirationOneWeek.unixIncrement,
	expirationNever.display:   expirationNever.unixIncrement,
}

func (fyneUI *Fyne) showEditProfile() {
	fyneUI.mainWindow.SetContent(fyneUI.editProfile)
	fyneUI.editProfile.Show()
}

func (fyneUI *Fyne) buildEditProfile() {
	//
	// Personal details section
	//

	// TODO: click to change profile image
	profileIcon := canvas.NewImageFromResource(newEmbeddedResource("assets/not_found.png"))
	profileIcon.FillMode = canvas.ImageFillContain
	profileIcon.SetMinSize(fyne.NewSize(64, 64))

	profileNameEntry := widget.NewEntry()
	currentProfileName := "Get the name from the database" //thread.name.Get() // TODO: load from DB but bind in UI
	//if err != nil {
	//	log.WithFields(log.Fields{
	//		"error": err.Error(),
	//	}).Fatal("data bindings are broken")
	//}
	profileNameEntry.Text = currentProfileName

	saveProfileButton := widget.NewButton("Update", func() {})
	saveProfileButton.Importance = widget.HighImportance

	saveProfileButtonBar := container.New(
		layout.NewBorderLayout(nil, nil, nil, saveProfileButton),
		saveProfileButton,
	)

	profileOptions := container.NewVBox(
		profileIcon,
		profileNameEntry,
		saveProfileButtonBar,
	)

	//
	// Sync devices secttion
	//

	devicesLabel := widget.NewLabel("Devices")
	devicesLabel.TextStyle = fyne.TextStyle{Bold: true}

	//
	// Existing exports section
	//

	//widget.NewLabel("Contact Exports")

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

		profileData := fyneUI.onExportContact(exportNameEntry.Text, expiration, oneTimeUse.Checked)
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
		fyneUI.showMainContainer()
	})
	closeButton.Importance = widget.LowImportance

	closeBar := container.New(
		layout.NewBorderLayout(nil, nil, nil, closeButton),
		closeButton,
	)
	profileAndDevices := container.NewVBox(
		profileOptions,
		devicesLabel,
	)
	fyneUI.editProfile = container.New(
		layout.NewBorderLayout(closeBar, exportContactMenu, nil, nil),
		closeBar,
		exportContactMenu,
		profileAndDevices, // TODO: a border layout with this at the top and the exports scroll taking up the remaining space
	)
}
