package ui

import (
	"fyne.io/fyne/v2"
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
			//fyne.NewMenuItem("Manage Workspaces", func() {
			//	fyneUI.showNewWorkspace()
			//}),
		),
		fyne.NewMenu(
			"Contacts",
			fyne.NewMenuItem("Share", func() {
				fyneUI.showDisplayAddUserString()
			}),
			fyne.NewMenuItem("Add", func() {
				fyneUI.showAddUser()
			}),
			//fyne.NewMenuItem("Introduce", func() {
			//	fyneUI.showIntroduceContacts()
			//}),
			fyne.NewMenuItem("Import", func() {
				fyneUI.showImportContact()
			}),
		),
	)
}

func (fyneUI *Fyne) showMobileMenu() {
	fyneUI.viewStack = append(fyneUI.viewStack, view{viewType: viewTypeMenu})
	fyneUI.mainWindow.SetContent(fyneUI.mobileMenu)
	fyneUI.mobileMenu.Show()
}

func (fyneUI *Fyne) buildMobileMenu() {
	fyneUI.mobileMenu = container.NewVBox(
		makeLogo(228, 167), // TODO: choose reasonable values here, https://github.com/fyne-io/fyne/blob/v2.0.3/cmd/fyne_demo/tutorials/welcome.go#L25
		widget.NewButton("My Profile", func() { fyneUI.showEditProfile() }),
		widget.NewButton("Settings", func() { fyneUI.showSettings() }),
		widget.NewButton("New Group", func() { fyneUI.showNewGroup() }),
		widget.NewButton("New DM", func() { fyneUI.showNewDM() }),
		widget.NewButton("Share Contact", func() { fyneUI.showDisplayAddUserString() }),
		widget.NewButton("Add Contact", func() { fyneUI.showAddUser() }),
		widget.NewButton("Import Contact", func() { fyneUI.showImportContact() }),
		widget.NewButton("About", func() { fyneUI.showAbout() }),
		widget.NewButton("Close", func() { fyneUI.mobileBack() }),
	)

}
