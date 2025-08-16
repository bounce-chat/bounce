package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
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
			fyne.NewMenuItem("Add Contact", func() {
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
	logo := makeLogo(228, 167) // TODO: choose reasonable values here, https://github.com/fyne-io/fyne/blob/v2.0.3/cmd/fyne_demo/tutorials/welcome.go#L25

	ui.views.mobileMenu = container.New(
		layout.NewBorderLayout(logo, nil, nil, nil),
		logo,
		container.NewVBox(container.New(
			NewRowWrapLayout(),
			newClickableImage("My Profile", canvas.NewImageFromResource(newEmbeddedResource("assets/icons/menu/my_profile.png")), 150, 150, true, func() { ui.showEditProfile() }),
			newClickableImage("New Group", canvas.NewImageFromResource(newEmbeddedResource("assets/icons/menu/new_group.png")), 150, 150, true, func() { ui.showNewGroup() }),
			newClickableImage("Add Contact", canvas.NewImageFromResource(newEmbeddedResource("assets/icons/menu/add_contact.png")), 150, 150, true, func() { ui.showAddUser() }),
			newClickableImage("Contacts", canvas.NewImageFromResource(newEmbeddedResource("assets/icons/menu/contacts.png")), 150, 150, true, func() { ui.showNewDM() }),
			newClickableImage("Settings", canvas.NewImageFromResource(newEmbeddedResource("assets/icons/menu/settings.png")), 150, 150, true, func() { ui.showSettings() }),
			newClickableImage("About", canvas.NewImageFromResource(newEmbeddedResource("assets/icons/menu/about.png")), 150, 150, true, func() { ui.showAbout() }),
		)),
	)
}
