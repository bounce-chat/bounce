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

func (fyneUI *Fyne) showEditThreadContainer(thread *group) {
	fyneUI.mainWindow.SetContent(thread.editContainer)
	thread.editContainer.Show()
}

func (fyneUI *Fyne) buildEditThreadContainer(thread *group) { // TODO: rename these to group
	currentThreadName, err := thread.name.Get()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("data bindings are broken")
	}
	thread.editThreadNameEntry.Text = currentThreadName

	thread.name.AddListener(binding.NewDataListener(func() {
		newName, err := thread.name.Get()
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("data bindings are broken")
		}
		thread.editThreadNameEntry.Text = newName
		thread.editThreadNameEntry.Refresh()
	}))

	threadIcon := canvas.NewImageFromResource(newEmbeddedResource("assets/not_found.png"))
	threadIcon.FillMode = canvas.ImageFillContain
	threadIcon.SetMinSize(fyne.NewSize(64, 64))

	thread.retentionSelection = widget.NewSelect(retentionSelections, nil)
	retention := fyneUI.callbacks.GetGroupRetention(thread.id)
	thread.retentionSelection.Selected = getRetentionName(retention)

	confirmClearHistory := dialog.NewConfirm(
		"Clear all message history?",
		"Are you sure you want to permanently delete all chat history on all devices?",
		func(confirmed bool) {
			if confirmed {
				fyneUI.callbacks.ClearGroupChatHistory(thread.id)
				fyneUI.showMainContainer()
				fyneUI.mainWindow.Canvas().Focus(thread.getEntry())
			}
		},
		fyneUI.mainWindow,
	)
	thread.clearHistoryButton = widget.NewButton("Clear history", func() {
		confirmClearHistory.Show()
	})

	saveButton := widget.NewButton("Save", func() {
		// Update the thread name if it was changed
		currentThreadName, err := thread.name.Get()
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("data bindings are broken")
		}
		newThreadName := thread.editThreadNameEntry.Text
		if currentThreadName != newThreadName {
			fyneUI.callbacks.RenameGroup(thread.id, newThreadName) // TODO: error check and display error in dialog, internationalize off exported error types
		}

		// Update the notification settings if they have changes
		if thread.notificationsEnabledCheck.Checked != (thread.notificationsMutedUntil != chat.MutedForever) {
			mutedUntil := int64(0)
			if !thread.notificationsEnabledCheck.Checked {
				mutedUntil = chat.MutedForever
			}
			err := fyneUI.callbacks.SetGroupMutedUntil(thread.id, mutedUntil)
			if err != nil {
				dialog.ShowError(errors.New("error updating group mute settings: "+err.Error()), fyneUI.mainWindow)
			}
		}
		// Update the retention if it changed
		currentRetention := fyneUI.callbacks.GetGroupRetention(thread.id)
		selectedRetentionString := thread.retentionSelection.Selected
		selectedRetentionValue, ok := retentionValues[selectedRetentionString]
		if !ok {
			dialog.ShowError(errors.New("invalid retention selection: "+selectedRetentionString), fyneUI.mainWindow)
		} else {
			if currentRetention != selectedRetentionValue {
				err = fyneUI.callbacks.SetGroupRetention(thread.id, selectedRetentionValue)
				if err != nil {
					dialog.ShowError(errors.New("error setting new retention value: "+err.Error()), fyneUI.mainWindow)
				}
			}
		}

		// Update the permissions if needed
		if thread.restrictUserManagementCheck.Checked && !thread.restrictUserManagement {
			fyneUI.callbacks.RestrictUserManagement(thread.id)
		}
		if !thread.restrictUserManagementCheck.Checked && thread.restrictUserManagement {
			fyneUI.callbacks.UnrestrictUserManagement(thread.id)
		}
		if thread.restrictGroupEditsCheck.Checked && !thread.restrictGroupEdits {
			fyneUI.callbacks.RestrictGroupEdits(thread.id)
		}
		if !thread.restrictGroupEditsCheck.Checked && thread.restrictGroupEdits {
			fyneUI.callbacks.UnrestrictGroupEdits(thread.id)
		}
		if thread.restrictPostingCheck.Checked && !thread.restrictPosting {
			fyneUI.callbacks.RestrictPosting(thread.id)
		}
		if !thread.restrictPostingCheck.Checked && thread.restrictPosting {
			fyneUI.callbacks.UnrestrictPosting(thread.id)
		}

		fyneUI.showMainContainer()
		fyneUI.mainWindow.Canvas().Focus(thread.getEntry())
	})
	saveButton.Importance = widget.HighImportance
	cancelButton := widget.NewButton("Cancel", func() {
		// Reset name
		currentThreadName, err := thread.name.Get()
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("data bindings are broken")
		}
		thread.editThreadNameEntry.Text = currentThreadName
		thread.editThreadNameEntry.Refresh()

		// Reset retention
		thread.retentionSelection.Selected = getRetentionName(fyneUI.callbacks.GetGroupRetention(thread.id))
		thread.retentionSelection.Refresh()

		// Reset notification settings
		thread.notificationsMutedUntil, err = fyneUI.callbacks.GetGroupMutedUntil(thread.id)
		if err != nil {
			log.WithFields(log.Fields{
				"group_id": thread.id,
			}).Fatal("cannot find muted until for group")
		}
		enabled := thread.notificationsMutedUntil != chat.MutedForever
		thread.notificationsEnabledCheck.SetChecked(enabled)

		// Reset user editing
		thread.pendingUsers.empty()
		fyneUI.refreshUserSelections(thread)

		// Reset permissions
		thread.restrictUserManagementCheck.SetChecked(thread.restrictUserManagement)
		thread.restrictGroupEditsCheck.SetChecked(thread.restrictGroupEdits)
		thread.restrictPostingCheck.SetChecked(thread.restrictPosting)

		// Show main tontainer
		fyneUI.showMainContainer()
		fyneUI.mainWindow.Canvas().Focus(thread.getEntry())
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
		thread.currentAdminsContainer, // TODO: eventually this will get so long it breaks the UI.  Need a better looking scroll wrapper.  https://github.com/fyne-io/fyne/issues/2322
	)

	usersLabel := widget.NewLabel("Users:")
	currentUsersListView := container.New(
		layout.NewBorderLayout(usersLabel, nil, nil, nil),
		usersLabel,
		thread.currentUsersContainer, // TODO: eventually this will get so long it breaks the UI.  Need a better looking scroll wrapper.  https://github.com/fyne-io/fyne/issues/2322
	)

	topOptionsVBox := container.NewVBox(
		threadIcon,
		thread.editThreadNameEntry,
		thread.notificationsEnabledCheck,
		widget.NewLabel("Disappearing Messages"),
		thread.retentionSelection,
		container.NewHBox(thread.clearHistoryButton),
	)
	topOptionsVBox.Add(thread.restrictUserManagementCheck)
	topOptionsVBox.Add(thread.restrictGroupEditsCheck)
	topOptionsVBox.Add(thread.restrictPostingCheck)
	topOptionsVBox.Add(currentAdminsListView)
	topOptionsVBox.Add(currentUsersListView)

	newUserSearchEntry := widget.NewEntry()
	newUserSelector := container.New(
		layout.NewBorderLayout(newUserSearchEntry, nil, nil, nil),
		newUserSearchEntry,
		container.NewVBox(
			widget.NewLabel("Users to add:"),
			thread.pendingUsersContainer,
			widget.NewLabel("All Users:"),
			thread.availableNewUsersScroll,
		),
	)
	addUsersDialog := dialog.NewCustomConfirm("Add Users", "Save", "Cancel", newUserSelector, func(apply bool) {
		if apply {
			for _, thisUser := range thread.pendingUsers.userList {
				go fyneUI.callbacks.AddUser(thread.id, thisUser.id) // TODO: dialog error if there is one
			}
		} else {
			thread.pendingUsers.empty()
			fyneUI.refreshUserSelections(thread)
		}
		newUserSearchEntry.Text = ""
		newUserSearchEntry.Refresh()
	}, fyneUI.mainWindow)
	thread.addUsersButton = widget.NewButton("Add Users", func() {
		addUsersDialog.Show()
	})

	// Close the window but save state.  TODO: should it clear state as well, and if so, just get rid of this?
	closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		fyneUI.showMainContainer()
		fyneUI.mainWindow.Canvas().Focus(thread.getEntry())
	})
	closeButton.Importance = widget.LowImportance

	closeBar := container.New(
		layout.NewBorderLayout(nil, nil, nil, closeButton),
		closeButton,
	)

	thread.editContainer = container.NewMax(
		container.New(
			layout.NewBorderLayout(closeBar, actionButtons, nil, nil),
			container.New(
				layout.NewBorderLayout(topOptionsVBox, nil, nil, nil),
				topOptionsVBox,
				container.NewVBox(container.NewHBox(thread.addUsersButton)), // TODO: make it not expand all the way down
			),
			closeBar,
			actionButtons,
		),
	)
}

func (fyneUI *Fyne) refreshCurrentAndPendingUsers(thread *group) {
	currentUsersList := container.NewVBox()
	pendingUsersList := container.NewVBox()
	currentAdminsList := container.NewVBox()

	for _, thisUser := range thread.users.alphabetized() {
		func(u *user) {
			userDetailsButton := widget.NewButtonWithIcon(u.name, newEmbeddedResource("assets/not_found.png"), func() {
				fyneUI.getEditUserDialog(thread, u.id).Show()
			})
			userDetailsButton.Alignment = widget.ButtonAlignLeading
			userDetailsButton.Importance = widget.LowImportance

			if thread.isAdmin(u.id) {
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

	for _, thisUser := range thread.pendingUsers.alphabetized() {
		func(u *user) {
			removePendingUserButton := widget.NewButtonWithIcon(u.name, newEmbeddedResource("assets/not_found.png"), func() { // TODO: use the user's icon
				thread.pendingUsers.remove(u.id)
				fyneUI.refreshUserSelections(thread)
			})
			removePendingUserButton.Alignment = widget.ButtonAlignLeading
			removePendingUserButton.Importance = widget.LowImportance
			pendingUsersList.Objects = append(
				pendingUsersList.Objects,
				removePendingUserButton,
			)
		}(thisUser)
	}

	thread.currentAdminsContainer.Objects = []fyne.CanvasObject{currentAdminsList}
	thread.currentAdminsContainer.Refresh()
	thread.currentUsersContainer.Objects = []fyne.CanvasObject{currentUsersList}
	thread.currentUsersContainer.Refresh()
	thread.pendingUsersContainer.Objects = []fyne.CanvasObject{pendingUsersList}
	thread.pendingUsersContainer.Refresh()
}

func (fyneUI *Fyne) refreshAvailableNewUsers(thread *group) {
	allUsersListBox := container.NewVBox()
	for _, thisUser := range fyneUI.users.alphabetized() {
		// Exclude users already in the thread
		if _, exists := thread.users.get(thisUser.id); exists {
			continue
		}
		// Exclude users that are pending addition to the group
		if _, exists := thread.pendingUsers.get(thisUser.id); exists {
			continue
		}

		// TODO: filter by what's in the search entry, or make searching a feature in the user store and pull from that

		// This weirdness is so that the iteration over user works with the dynamically created buttons
		// TODO: make sure I'm not making this mistake anywhere else
		func(u *user) {
			addUserButton := widget.NewButtonWithIcon(u.name, newEmbeddedResource("assets/not_found.png"), func() { // TODO: use the user's icon
				thread.pendingUsers.add(u)
				fyneUI.refreshUserSelections(thread)
			})
			addUserButton.Alignment = widget.ButtonAlignLeading
			addUserButton.Importance = widget.LowImportance
			allUsersListBox.Objects = append(
				allUsersListBox.Objects,
				addUserButton,
			)
		}(thisUser)
	}
	thread.availableNewUsersScroll.Content = allUsersListBox
	thread.availableNewUsersScroll.Refresh()
}

func (fyneUI *Fyne) refreshUserSelections(group *group) {
	fyneUI.refreshCurrentAndPendingUsers(group)
	fyneUI.refreshAvailableNewUsers(group)
}
