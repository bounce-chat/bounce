package ui

import (
	"errors"

	"github.com/hkparker/bounce/chat"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	log "github.com/sirupsen/logrus"
)

func (fyneUI *Fyne) showEditThreadContainer(g *group) {
	fyneUI.mainWindow.SetContent(g.editContainer)
	g.editContainer.Show()
}

func (fyneUI *Fyne) buildEditThreadContainer(g *group) {
	currentThreadName, err := g.name.Get()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("data bindings are broken")
	}
	g.editThreadNameEntry.Text = currentThreadName

	g.name.AddListener(binding.NewDataListener(func() {
		newName, err := g.name.Get()
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("data bindings are broken")
		}
		g.editThreadNameEntry.Text = newName
		g.editThreadNameEntry.Refresh()
	}))

	groupIcon := canvas.NewImageFromResource(newEmbeddedResource("assets/not_found.png"))
	groupIcon.FillMode = canvas.ImageFillContain
	groupIcon.SetMinSize(fyne.NewSize(64, 64))

	g.retentionSelection = widget.NewSelect(retentionSelections, nil)
	g.retentionSelection.Selected = getRetentionName(g.retention)

	confirmClearHistory := dialog.NewConfirm(
		"Clear all message history?",
		"Are you sure you want to permanently delete all chat history on all devices?",
		func(confirmed bool) {
			if confirmed {
				err := fyneUI.callbacks.ClearGroupChatHistory(g.id)
				if err != nil {
					dialog.ShowError(errors.New("error clearing chat history: "+err.Error()), fyneUI.mainWindow)
				}
				fyneUI.showMainContainer()
				fyneUI.mainWindow.Canvas().Focus(g.getEntry())
			}
		},
		fyneUI.mainWindow,
	)
	g.clearHistoryButton = widget.NewButton("Clear History", func() {
		confirmClearHistory.Show()
	})

	confirmLeaveGroup := dialog.NewConfirm(
		"Leave Group?",
		"Are you sure you want to leave this group?",
		func(confirmed bool) {
			if confirmed {
				err := fyneUI.callbacks.RemoveUser(g.id, fyneUI.profile.id)
				if err != nil {
					dialog.ShowError(errors.New("error leaving group: "+err.Error()), fyneUI.mainWindow)
				}
				fyneUI.showMainContainer()
			}
		},
		fyneUI.mainWindow,
	)
	g.leaveGroupButton = widget.NewButton("Leave Group", func() {
		confirmLeaveGroup.Show()
	})

	confirmDeleteGroup := dialog.NewConfirm(
		"Delete this group?",
		"Are you sure you want to permanently delete this group for all members?",
		func(confirmed bool) {
			if confirmed {
				err := fyneUI.callbacks.DeleteGroup(g.id)
				if err != nil {
					dialog.ShowError(errors.New("error deleting group: "+err.Error()), fyneUI.mainWindow)
				}
				fyneUI.showMainContainer()
			}
		},
		fyneUI.mainWindow,
	)
	g.deleteGroupButton = widget.NewButton("Delete Group", func() {
		confirmDeleteGroup.Show()
	})

	saveButton := widget.NewButton("Save", func() {
		// Update the g name if it was changed
		currentThreadName, err := g.name.Get()
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("data bindings are broken")
		}
		newThreadName := g.editThreadNameEntry.Text
		if currentThreadName != newThreadName {
			err := fyneUI.callbacks.RenameGroup(g.id, newThreadName)
			if err != nil {
				dialog.ShowError(errors.New("error renaming group: "+err.Error()), fyneUI.mainWindow)
			}
		}

		// Update the notification settings if they have changes
		if g.notificationsEnabledCheck.Checked != (g.notificationsMutedUntil != chat.MutedForever) {
			mutedUntil := int64(0)
			if !g.notificationsEnabledCheck.Checked {
				mutedUntil = chat.MutedForever
			}
			err := fyneUI.callbacks.SetGroupMutedUntil(g.id, mutedUntil)
			if err != nil {
				dialog.ShowError(errors.New("error updating group mute settings: "+err.Error()), fyneUI.mainWindow)
			}
		}
		// Update the retention if it changed
		selectedRetentionString := g.retentionSelection.Selected
		selectedRetentionValue, ok := retentionValues[selectedRetentionString]
		if !ok {
			dialog.ShowError(errors.New("invalid retention selection: "+selectedRetentionString), fyneUI.mainWindow)
		} else {
			if g.retention != selectedRetentionValue {
				err = fyneUI.callbacks.SetGroupRetention(g.id, selectedRetentionValue)
				if err != nil {
					dialog.ShowError(errors.New("error setting new retention value: "+err.Error()), fyneUI.mainWindow)
				}
			}
		}

		// Update the permissions if needed
		if g.restrictUserManagementCheck.Checked && !g.restrictUserManagement {
			err := fyneUI.callbacks.RestrictUserManagement(g.id)
			if err != nil {
				dialog.ShowError(errors.New("error restricting user management: "+err.Error()), fyneUI.mainWindow)
			}
		}
		if !g.restrictUserManagementCheck.Checked && g.restrictUserManagement {
			err := fyneUI.callbacks.UnrestrictUserManagement(g.id)
			if err != nil {
				dialog.ShowError(errors.New("error unrestricting user management: "+err.Error()), fyneUI.mainWindow)
			}
		}
		if g.restrictGroupEditsCheck.Checked && !g.restrictGroupEdits {
			err := fyneUI.callbacks.RestrictGroupEdits(g.id)
			if err != nil {
				dialog.ShowError(errors.New("error restricting group edits: "+err.Error()), fyneUI.mainWindow)
			}
		}
		if !g.restrictGroupEditsCheck.Checked && g.restrictGroupEdits {
			err := fyneUI.callbacks.UnrestrictGroupEdits(g.id)
			if err != nil {
				dialog.ShowError(errors.New("error unrestricting group edits: "+err.Error()), fyneUI.mainWindow)
			}
		}
		if g.restrictPostingCheck.Checked && !g.restrictPosting {
			err := fyneUI.callbacks.RestrictPosting(g.id)
			if err != nil {
				dialog.ShowError(errors.New("error restricting posting: "+err.Error()), fyneUI.mainWindow)
			}
		}
		if !g.restrictPostingCheck.Checked && g.restrictPosting {
			err := fyneUI.callbacks.UnrestrictPosting(g.id)
			if err != nil {
				dialog.ShowError(errors.New("error unrestricting posting: "+err.Error()), fyneUI.mainWindow)
			}
		}

		fyneUI.showMainContainer()
		fyneUI.mainWindow.Canvas().Focus(g.getEntry())
	})
	saveButton.Importance = widget.HighImportance
	cancelButton := widget.NewButton("Cancel", func() {
		// Reset name
		currentThreadName, err := g.name.Get()
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("data bindings are broken")
		}
		g.editThreadNameEntry.Text = currentThreadName
		g.editThreadNameEntry.Refresh()

		// Reset retention
		g.retentionSelection.Selected = getRetentionName(g.retention)
		g.retentionSelection.Refresh()

		// Reset notification settings
		enabled := g.notificationsMutedUntil != chat.MutedForever
		g.notificationsEnabledCheck.SetChecked(enabled)

		// Reset user editing
		g.pendingUsers.empty()
		fyneUI.refreshUserSelections(g)

		// Reset permissions
		g.restrictUserManagementCheck.SetChecked(g.restrictUserManagement)
		g.restrictGroupEditsCheck.SetChecked(g.restrictGroupEdits)
		g.restrictPostingCheck.SetChecked(g.restrictPosting)

		// Show main tontainer
		fyneUI.showMainContainer()
		fyneUI.mainWindow.Canvas().Focus(g.getEntry())
	})
	actionButtons := container.New(
		layout.NewBorderLayout(nil, nil, cancelButton, saveButton),
		saveButton,
		cancelButton,
	)

	adminsLabel := widget.NewLabel("Admins:")
	currentAdminsListView := container.New(
		layout.NewBorderLayout(adminsLabel, nil, nil, nil),
		adminsLabel,
		g.currentAdminsContainer, // TODO: eventually this will get so long it breaks the UI.  Need a better looking scroll wrapper.  https://github.com/fyne-io/fyne/issues/2322
	)

	usersLabel := widget.NewLabel("Users:")
	currentUsersListView := container.New(
		layout.NewBorderLayout(usersLabel, nil, nil, nil),
		usersLabel,
		g.currentUsersContainer, // TODO: eventually this will get so long it breaks the UI.  Need a better looking scroll wrapper.  https://github.com/fyne-io/fyne/issues/2322
	)

	topOptionsVBox := container.NewVBox(
		groupIcon,
		g.editThreadNameEntry,
		g.notificationsEnabledCheck,
		widget.NewLabel("Disappearing Messages"),
		g.retentionSelection,
		container.NewHBox(g.clearHistoryButton),
		container.NewHBox(g.leaveGroupButton),
		container.NewHBox(g.deleteGroupButton),
	)
	topOptionsVBox.Add(g.restrictUserManagementCheck)
	topOptionsVBox.Add(g.restrictGroupEditsCheck)
	topOptionsVBox.Add(g.restrictPostingCheck)
	topOptionsVBox.Add(currentAdminsListView)
	topOptionsVBox.Add(currentUsersListView)

	newUserSearchEntry := widget.NewEntry()
	newUserSelector := container.New(
		layout.NewBorderLayout(newUserSearchEntry, nil, nil, nil),
		newUserSearchEntry,
		container.NewVBox(
			widget.NewLabel("Users to add:"),
			g.pendingUsersContainer,
			widget.NewLabel("All Users:"),
			g.availableNewUsersScroll,
		),
	)
	addUsersDialog := dialog.NewCustomConfirm("Add Users", "Save", "Cancel", newUserSelector, func(apply bool) {
		if apply {
			for _, thisUser := range g.pendingUsers.userList {
				err := fyneUI.callbacks.AddUser(g.id, thisUser.id)
				if err != nil {
					dialog.ShowError(errors.New("error adding user: "+err.Error()), fyneUI.mainWindow)
				}
			}
		} else {
			g.pendingUsers.empty()
			fyneUI.refreshUserSelections(g)
		}
		newUserSearchEntry.Text = ""
		newUserSearchEntry.Refresh()
	}, fyneUI.mainWindow)
	g.addUsersButton = widget.NewButton("Add Users", func() {
		addUsersDialog.Show()
	})

	// Close the window but save state.  TODO: should it clear state as well, and if so, just get rid of this?
	closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		fyneUI.showMainContainer()
		fyneUI.mainWindow.Canvas().Focus(g.getEntry())
	})
	closeButton.Importance = widget.LowImportance

	closeBar := container.New(
		layout.NewBorderLayout(nil, nil, nil, closeButton),
		closeButton,
	)

	g.editContainer = container.NewMax(
		container.New(
			layout.NewBorderLayout(closeBar, actionButtons, nil, nil),
			container.New(
				layout.NewBorderLayout(topOptionsVBox, nil, nil, nil),
				topOptionsVBox,
				container.NewVBox(container.NewHBox(g.addUsersButton)), // TODO: make it not expand all the way down
			),
			closeBar,
			actionButtons,
		),
	)
}

func (fyneUI *Fyne) refreshCurrentAndPendingUsers(g *group) {
	currentUsersList := container.NewVBox()
	pendingUsersList := container.NewVBox()
	currentAdminsList := container.NewVBox()

	for _, thisUser := range g.users.alphabetized() {
		func(u *user) {
			userDetailsButton := widget.NewButtonWithIcon(u.name, newEmbeddedResource("assets/not_found.png"), func() {
				fyneUI.getEditUserDialog(g, u.id).Show()
			})
			userDetailsButton.Alignment = widget.ButtonAlignLeading
			userDetailsButton.Importance = widget.LowImportance

			if g.isAdmin(u.id) {
				currentAdminsList.Objects = append(
					currentAdminsList.Objects,
					userDetailsButton,
				)
			} else {
				currentUsersList.Objects = append(
					currentUsersList.Objects,
					userDetailsButton,
				)
			}
		}(thisUser)
	}

	for _, thisUser := range g.pendingUsers.alphabetized() {
		func(u *user) {
			removePendingUserButton := widget.NewButtonWithIcon(u.name, newEmbeddedResource("assets/not_found.png"), func() { // TODO: use the user's icon
				g.pendingUsers.remove(u.id)
				fyneUI.refreshUserSelections(g)
			})
			removePendingUserButton.Alignment = widget.ButtonAlignLeading
			removePendingUserButton.Importance = widget.LowImportance
			pendingUsersList.Objects = append(
				pendingUsersList.Objects,
				removePendingUserButton,
			)
		}(thisUser)
	}

	g.currentAdminsContainer.Objects = []fyne.CanvasObject{currentAdminsList}
	g.currentAdminsContainer.Refresh()
	g.currentUsersContainer.Objects = []fyne.CanvasObject{currentUsersList}
	g.currentUsersContainer.Refresh()
	g.pendingUsersContainer.Objects = []fyne.CanvasObject{pendingUsersList}
	g.pendingUsersContainer.Refresh()
}

func (fyneUI *Fyne) refreshAvailableNewUsers(g *group) {
	allUsersListBox := container.NewVBox()
	for _, thisUser := range fyneUI.users.alphabetized() {
		// Exclude users already in the group
		if _, exists := g.users.get(thisUser.id); exists {
			continue
		}
		// Exclude users that are pending addition to the group
		if _, exists := g.pendingUsers.get(thisUser.id); exists {
			continue
		}

		// TODO: filter by what's in the search entry, or make searching a feature in the user store and pull from that

		// This weirdness is so that the iteration over user works with the dynamically created buttons
		// TODO: make sure I'm not making this mistake anywhere else
		func(u *user) {
			addUserButton := widget.NewButtonWithIcon(u.name, newEmbeddedResource("assets/not_found.png"), func() { // TODO: use the user's icon
				g.pendingUsers.add(u)
				fyneUI.refreshUserSelections(g)
			})
			addUserButton.Alignment = widget.ButtonAlignLeading
			addUserButton.Importance = widget.LowImportance
			allUsersListBox.Objects = append(
				allUsersListBox.Objects,
				addUserButton,
			)
		}(thisUser)
	}
	g.availableNewUsersScroll.Content = allUsersListBox
	g.availableNewUsersScroll.Refresh()
}

func (fyneUI *Fyne) refreshUserSelections(g *group) {
	fyneUI.refreshCurrentAndPendingUsers(g)
	fyneUI.refreshAvailableNewUsers(g)
}
