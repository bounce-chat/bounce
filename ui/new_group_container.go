package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
)

func (fyneUI *Fyne) showNewGroup() {
	fyneUI.clearNewGroupSelectors()
	fyneUI.mainWindow.SetContent(fyneUI.newGroup)
	fyneUI.newGroup.Show()
}

func (fyneUI *Fyne) clearNewGroupSelectors() {
	fyneUI.newGroupNameEntry.Text = ""
	fyneUI.newGroupNameEntry.Refresh()

	// Empty the currently selected users
	fyneUI.newGroupSelectedUsersContainer = container.NewVBox()

	// Refresh the list of available users to all users that aren't us
	allUsersListBox := container.NewVBox()
	for _, thisUser := range fyneUI.users.alphabetized() {
		// Don't include our user in the list of users for a chat
		if thisUser.id == fyneUI.profile.id {
			continue
		}

		// Anonymous function to cope the looped user
		func(u *user) {
			addUserButton := widget.NewButtonWithIcon(u.name, newEmbeddedResource("assets/not_found.png"), func() { // TODO: use the user's icon
				fyneUI.newGroupSelectedUsers.add(u)
				fyneUI.refreshNewGroupUserSelections()
			})
			addUserButton.Alignment = widget.ButtonAlignLeading
			addUserButton.Importance = widget.LowImportance
			allUsersListBox.Objects = append(
				allUsersListBox.Objects,
				addUserButton,
			)
		}(thisUser)
	}
	fyneUI.newGroupAllAvailableUsers.Content = allUsersListBox
	fyneUI.newGroupAllAvailableUsers.Refresh()
}

func (fyneUI *Fyne) refreshNewGroupUserSelections() {
	// Add the currently selected users to the display
	currentUsersList := container.NewVBox()
	for _, thisUser := range fyneUI.newGroupSelectedUsers.alphabetized() {
		func(u *user) {
			removePendingUserButton := widget.NewButtonWithIcon(u.name, newEmbeddedResource("assets/not_found.png"), func() { // TODO: use the user's icon
				fyneUI.newGroupSelectedUsers.remove(u.id)
				fyneUI.refreshNewGroupUserSelections()
			})
			removePendingUserButton.Alignment = widget.ButtonAlignLeading
			removePendingUserButton.Importance = widget.LowImportance
			currentUsersList.Objects = append(
				currentUsersList.Objects,
				container.New(
					layout.NewMaxLayout(),
					&canvas.Rectangle{FillColor: color.NRGBA{0, 0, 0x40, 0x40}},
					removePendingUserButton,
				),
			)
		}(thisUser)
	}
	fyneUI.newGroupSelectedUsersContainer.Objects = []fyne.CanvasObject{currentUsersList}
	fyneUI.newGroupSelectedUsersContainer.Refresh()

	// Update the available user to exclude these pending users
	allUsersListBox := container.NewVBox()
	for _, thisUser := range fyneUI.users.alphabetized() {
		// Exclude users that are pending addition to the group
		if _, exists := fyneUI.newGroupSelectedUsers.get(thisUser.id); exists {
			continue
		}
		// Exclude our users
		if thisUser.id == fyneUI.profile.id {
			continue
		}

		func(u *user) {
			addUserButton := widget.NewButtonWithIcon(u.name, newEmbeddedResource("assets/not_found.png"), func() { // TODO: use the user's icon
				fyneUI.newGroupSelectedUsers.add(u)
				fyneUI.refreshNewGroupUserSelections()
			})
			addUserButton.Alignment = widget.ButtonAlignLeading
			addUserButton.Importance = widget.LowImportance
			allUsersListBox.Objects = append(
				allUsersListBox.Objects,
				addUserButton,
			)
		}(thisUser)
	}
	fyneUI.newGroupAllAvailableUsers.Content = allUsersListBox
	fyneUI.newGroupAllAvailableUsers.Refresh()
}

func (fyneUI *Fyne) buildNewGroup() {
	closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		fyneUI.showMainContainer()
	})
	closeButton.Importance = widget.LowImportance

	closeBar := container.New(
		layout.NewBorderLayout(nil, nil, nil, closeButton),
		closeButton,
	)

	saveButton := widget.NewButton("Save", func() {
		// TODO: dialog an error if there's no group name, or no users selected

		users := []uuid.UUID{}
		for _, user := range fyneUI.newGroupSelectedUsers.alphabetized() {
			users = append(users, user.id)
		}
		fyneUI.callbacks.CreateGroup(fyneUI.newGroupNameEntry.Text, users)
	})
	saveButton.Importance = widget.HighImportance
	cancelButton := widget.NewButton("Cancel", func() {
		fyneUI.showMainContainer()
	})
	actionButtons := container.New(
		layout.NewBorderLayout(nil, nil, cancelButton, saveButton),
		saveButton,
		cancelButton,
	)

	currentUsers := container.NewVBox(
		widget.NewLabel("current users show here:"),
		fyneUI.newGroupSelectedUsersContainer,
		widget.NewLabel("end current users"),
	)
	fyneUI.newGroup = container.New(
		layout.NewBorderLayout(closeBar, actionButtons, nil, nil),
		closeBar,
		actionButtons,
		container.New(
			layout.NewBorderLayout(fyneUI.newGroupNameEntry, nil, nil, nil),
			fyneUI.newGroupNameEntry,
			container.New(
				layout.NewBorderLayout(currentUsers, nil, nil, nil),
				currentUsers,
				fyneUI.newGroupAllAvailableUsers,
			),
		),
	)
}
