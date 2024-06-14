package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

func (fyneUI *Fyne) buildMenu() {
	fyneUI.mainMenu = fyne.NewMainMenu(
		fyne.NewMenu(
			"Bounce",
			fyne.NewMenuItem("My Profile", func() {
				fyneUI.showEditProfile()
			}),
			//fyne.NewMenuItem("My Devices", func() {
			//fyneUI.showEditDevices()
			//}),
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
			fyne.NewMenuItem("New Workspace", func() {
				//fyneUI.showNewWorkspace()
			}),
		),
		fyne.NewMenu(
			"Contacts",
			fyne.NewMenuItem("Share", func() {
				fyneUI.showDisplayAddUserString()
			}),
			fyne.NewMenuItem("Add", func() {
				fyneUI.showAddUser()
			}),
			fyne.NewMenuItem("Introduce", func() {
				fyneUI.showIntroduceContacts()
			}),
			fyne.NewMenuItem("Import", func() {
				fyneUI.showImportContact()
			}),
		),
	)
}

func (fyneUI *Fyne) showMobileMenu() {
	fyneUI.mainWindow.SetContent(fyneUI.mobileMenu)
	fyneUI.mobileMenu.Show()
}

func (fyneUI *Fyne) buildMobileMenu() {
	logo := canvas.NewImageFromResource(newEmbeddedResource("assets/logo.png"))
	logo.FillMode = canvas.ImageFillContain
	logo.SetMinSize(fyne.NewSize(228, 167))

	fyneUI.mobileMenu = container.NewVBox(
		logo,
		widget.NewButton("My Profile", func() { fyneUI.showEditProfile() }),
		widget.NewButton("Settings", func() { fyneUI.showSettings() }),
		widget.NewButton("New Group", func() { fyneUI.showNewGroup() }),
		widget.NewButton("New DM", func() { fyneUI.showNewDM() }),
		widget.NewButton("Share Contact", func() { fyneUI.showDisplayAddUserString() }),
		widget.NewButton("Add Contact", func() { fyneUI.showAddUser() }),
		widget.NewButton("Import Contact", func() { fyneUI.showImportContact() }),
		widget.NewButton("About", func() { fyneUI.showAbout() }),
		widget.NewButton("Close", func() { fyneUI.showMainContainer() }),
	)

}
