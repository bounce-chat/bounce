package ui

import (
	"fyne.io/fyne/v2"
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

	// TODO: other things here: change profile name, manage sync devices
	fyneUI.editProfile = container.New(
		layout.NewBorderLayout(closeBar, nil, nil, nil),
		closeBar,
		container.NewCenter(
			widget.NewButton("Click here to export your contact", func() {
				fileSelector.SetFileName("profile.bounce") // TODO: set the user's current profile name
				fileSelector.Show()
			}),
		),
	)
}
