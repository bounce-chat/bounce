package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
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
	backButton := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() {
		if fyne.CurrentDevice().IsMobile() {
			ui.mobileBack()
		} else {
			ui.showMainContainer()
		}
	})
	backButton.Importance = widget.LowImportance
	backBar := container.New(
		layout.NewBorderLayout(nil, nil, backButton, nil),
		backButton,
	)
	header := container.NewVBox(
		backBar,
		makeLogo(247, 75),
	)

	ui.views.mobileMenu = container.New(
		layout.NewBorderLayout(header, nil, nil, nil),
		header,
		container.NewVScroll(container.NewVBox(
			newSettingsButton(canvas.NewImageFromResource(newEmbeddedResource("assets/icons/menu/my_profile.png")), "My Profile", 75, func() { ui.showEditProfile() }),
			newSettingsButton(canvas.NewImageFromResource(newEmbeddedResource("assets/icons/menu/new_group.png")), "New Group", 75, func() { ui.showNewGroup() }),
			newSettingsButton(canvas.NewImageFromResource(newEmbeddedResource("assets/icons/menu/add_contact.png")), "Add User", 75, func() { ui.showAddUser() }),
			newSettingsButton(canvas.NewImageFromResource(newEmbeddedResource("assets/icons/menu/contacts.png")), "Contacts", 75, func() { ui.showNewDM() }),
			newSettingsButton(canvas.NewImageFromResource(newEmbeddedResource("assets/icons/menu/settings.png")), "Settings", 75, func() { ui.showSettings() }),
			newSettingsButton(canvas.NewImageFromResource(newEmbeddedResource("assets/icons/menu/about.png")), "About", 75, func() { ui.showAbout() }),
		)),
	)
}
