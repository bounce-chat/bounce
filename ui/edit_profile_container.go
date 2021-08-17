package ui

import (
	"errors"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

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
	oneTimeUse := widget.NewCheck("One-Time Use", nil)
	exportContactForm := widget.NewForm(
		&widget.FormItem{
			Text:   "Name:",
			Widget: exportNameEntry,
		},
		&widget.FormItem{
			Text:   "Expires:",
			Widget: widget.NewSelect([]string{"1 Hour", "1 Day", "1 Week", "Never"}, nil),
		},
		&widget.FormItem{
			Text:   "",
			Widget: oneTimeUse,
		},
	)
	exportContactForm.SubmitText = "Export"

	fileSelector := dialog.NewFileSave(func(handler fyne.URIWriteCloser, err error) {
		// TODO: err check the error passed in
		if handler == nil {
			// Selection canceled
			return
		}
		expiration := int64(0) // TODO: translate from selection
		profileData := fyneUI.onExportContact(exportNameEntry.Text, expiration, oneTimeUse.Checked)
		// TODO: err check these two
		handler.Write(profileData)
		handler.Close()

		//if err != nil {
		//	dialog.ShowError(errors.New("error importing contact: "+err.Error()), fyneUI.mainWindow)
		//} else {
		dialog.ShowInformation("Success", "Contact export written to "+handler.URI().Name(), fyneUI.mainWindow)
		//}
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
