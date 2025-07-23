package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

func (ui *ui) buildMenu() {
	ui.containers.mainMenu = fyne.NewMainMenu(
		fyne.NewMenu(
			"Bounce",
			fyne.NewMenuItem("My Profile", func() {
				ui.showEditProfile()
			}),
			fyne.NewMenuItem("Settings", func() {
				ui.showSettings()
			}),
			fyne.NewMenuItem("About", func() {
				ui.showAbout()
			}),
		),
		fyne.NewMenu(
			"Chat",
			fyne.NewMenuItem("New Group", func() {
				ui.showNewGroup()
			}),
			fyne.NewMenuItem("Contacts", func() {
				ui.showNewDM()
			}),
		),
		/*
			fyne.NewMenu(
				"Chat",
				fyne.NewMenuItem("New Group", func() {
					ui.showNewGroup()
				}),
				fyne.NewMenuItem("New DM", func() {
					ui.showNewDM()
				}),
			),
		*/
		fyne.NewMenu(
			"Contacts",
			//fyne.NewMenuItem("Manage", func() {
			//	ui.showManageUsers()
			//}),
			fyne.NewMenuItem("Add", func() {
				ui.showAddUser()
			}),
		),
	)
}

func (ui *ui) showMobileMenu() {
	ui.state.viewStack = append(ui.state.viewStack, view{viewType: viewTypeMenu})
	ui.state.currentView = viewTypeMenu
	ui.window.SetContent(ui.views.mobileMenu)
}

func (ui *ui) buildMobileMenu() {
	ui.views.mobileMenu = container.NewVBox(
		makeLogo(228, 167), // TODO: choose reasonable values here, https://github.com/fyne-io/fyne/blob/v2.0.3/cmd/fyne_demo/tutorials/welcome.go#L25
		widget.NewButton("My Profile", func() { ui.showEditProfile() }),
		widget.NewButton("Settings", func() { ui.showSettings() }),
		widget.NewButton("New Group", func() { ui.showNewGroup() }),
		widget.NewButton("New DM", func() { ui.showNewDM() }),
		widget.NewButton("Contacts", func() { ui.showManageUsers() }),
		widget.NewButton("Add Contact", func() { ui.showAddUser() }),
		widget.NewButton("About", func() { ui.showAbout() }),
		widget.NewButton("Close", func() { ui.mobileBack() }),
	)

}
