package ui

import (
	"fyne.io/fyne/v2"
)

func (fyneUI *Fyne) buildMenu() *fyne.MainMenu {
	return fyne.NewMainMenu(
		fyne.NewMenu(
			"Bounce",
			fyne.NewMenuItem("My Profile", func() {
				fyneUI.showEditProfile()
			}),
			fyne.NewMenuItem("My Devices", func() {
				//fyneUI.showEditDevices()
			}),
			fyne.NewMenuItem("Settings", func() {
				fyneUI.showSettings()
			}),
			fyne.NewMenuItem("About", func() {
				fyneUI.showAbout()
			}),
		),
		fyne.NewMenu(
			"Chat",
			fyne.NewMenuItem("New Group", func() {
				fyneUI.showNewGroup()
			}),
			fyne.NewMenuItem("New DM", func() {
				fyneUI.showNewDM()
			}),
		),
		fyne.NewMenu(
			"Contacts",
			fyne.NewMenuItem("Introduce", func() {
				fyneUI.showIntroduceContacts()
			}),
			fyne.NewMenuItem("Import", func() {
				fyneUI.showImportContact()
			}),
		),
	)
}
