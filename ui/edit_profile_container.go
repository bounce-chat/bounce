package ui

import (
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

	devicesLabel := widget.NewLabel("Devices")
	profileAndDevices := container.NewVBox(
		profileOptions,
		devicesLabel,
	)

	closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		fyneUI.showMainContainer()
	})
	closeButton.Importance = widget.LowImportance

	closeBar := container.New(
		layout.NewBorderLayout(nil, nil, nil, closeButton),
		closeButton,
	)

	fileSelector := dialog.NewFileSave(func(handler fyne.URIWriteCloser, err error) {
		if handler == nil {
			// Selection canceled
			return
		}
		profileData := fyneUI.onExportContact()
		// TODO: err check these two
		handler.Write(profileData)
		handler.Close()

		//if err != nil {
		//	dialog.ShowError(errors.New("error importing contact: "+err.Error()), fyneUI.mainWindow)
		//} else {
		dialog.ShowInformation("Success", "Contact export written to "+handler.URI().Name(), fyneUI.mainWindow)
		//}
	}, fyneUI.mainWindow)

	exportContactMenu := container.NewCenter(
		widget.NewButton("Click here to export your contact", func() {
			fileSelector.SetFileName("profile.bounce") // TODO: set the user's current profile name
			fileSelector.Show()
		}),
	)

	// TODO: other things here: change profile name, manage sync devices
	fyneUI.editProfile = container.New(
		layout.NewBorderLayout(closeBar, exportContactMenu, nil, nil),
		closeBar,
		exportContactMenu,
		profileAndDevices, // TODO: a border layout with this at the top and the exports scroll taking up the remaining space
	)
}
