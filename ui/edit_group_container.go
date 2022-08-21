package ui

import (
	"image/color"

	"github.com/hkparker/bounce/chat"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
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
	editThreadNameEntry := widget.NewEntry()
	currentThreadName, err := thread.name.Get()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("data bindings are broken")
	}
	editThreadNameEntry.Text = currentThreadName
	thread.name.AddListener(binding.NewDataListener(func() {
		newName, err := thread.name.Get()
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("data bindings are broken")
		}
		editThreadNameEntry.Text = newName
		editThreadNameEntry.Refresh()
	}))

	threadIcon := canvas.NewImageFromResource(newEmbeddedResource("assets/not_found.png"))
	threadIcon.FillMode = canvas.ImageFillContain
	threadIcon.SetMinSize(fyne.NewSize(64, 64))

	notificationsCheck := widget.NewCheckWithData("Enable notifications", thread.notificationsEnabled)
	notificationsCheck.OnChanged = func(state bool) { // TODO: do we really want this to apply before save?
		thread.notificationsEnabled.Set(state) // TODO: error check
		fyneUI.callbacks.ChangeGroupNotificationSettings(thread.id, state)
	}

	saveButton := widget.NewButton("Save", func() {
		// Update the thread name if it was changed
		currentThreadName, err := thread.name.Get()
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("data bindings are broken")
		}
		newThreadName := editThreadNameEntry.Text
		if currentThreadName != newThreadName {
			fyneUI.callbacks.RenameGroup(thread.id, newThreadName) // TODO: error check and display error in dialog, internationalize off exported error types
		}
		// Add the selected users to the group
		for _, user := range thread.pendingUsers.alphabetized() {
			thread.users.add(user)
			thread.pendingUsers.remove(user.id)
			fyneUI.callbacks.AddUserToGroup(thread.id, user.id)
		}
		fyneUI.refreshUserSelections(thread)
		fyneUI.showMainContainer()
	})
	saveButton.Importance = widget.HighImportance
	cancelButton := widget.NewButton("Cancel", func() {
		// Reset everything
		currentThreadName, err := thread.name.Get()
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("data bindings are broken")
		}
		editThreadNameEntry.Text = currentThreadName
		editThreadNameEntry.Refresh()
		thread.pendingUsers.empty()
		fyneUI.refreshUserSelections(thread)
		fyneUI.showMainContainer()
	})
	actionButtons := container.New(
		layout.NewBorderLayout(nil, nil, cancelButton, saveButton),
		saveButton,
		cancelButton,
	)

	usersLabel := widget.NewLabel("Current Users:")
	currentUsersListView := container.New(
		layout.NewBorderLayout(usersLabel, nil, nil, nil),
		usersLabel,
		thread.currentUsersContainer, // TODO: eventually this will get so long it breaks the UI.  Need a better looking scroll wrapper.  https://github.com/fyne-io/fyne/issues/2322
	)

	topOptionsVBox := container.NewVBox(
		threadIcon,
		editThreadNameEntry,
		notificationsCheck,
		currentUsersListView,
	)

	addUsersLabel := widget.NewLabel("Add Users:")
	allUsersList := container.New(
		layout.NewBorderLayout(addUsersLabel, nil, nil, nil),
		addUsersLabel,
		thread.availableNewUsersScroll,
	)

	// Close the window but save state.  TODO: should it clear state as well?
	closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		fyneUI.showMainContainer()
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
				allUsersList,
			),
			closeBar,
			actionButtons,
		),
	)
}

func (fyneUI *Fyne) refreshCurrentAndPendingUsers(thread *group) {
	currentUsersList := container.NewVBox()

	for _, thisUser := range thread.users.alphabetized() {
		func(u *user) {
			dmButton := widget.NewButtonWithIcon(u.name, newEmbeddedResource("assets/not_found.png"), func() {
				log.Info("user wants to open a DM with " + u.name)
				// TODO: hide this window and open a DM
				// check fyneUI for existing DMs, create thread if needed and focus it
				dm, dmExists := fyneUI.dms[u.id]
				if !dmExists {
					fyneUI.NewDirectMessage(chat.User{
						ID:   u.id,
						Name: u.name,
					})
					dm, dmExists = fyneUI.dms[u.id]
					if !dmExists {
						log.Fatal("DM doesn't exist immediately after creation")
					}
				}
				fyneUI.showMainContainer()
				fyneUI.displayThread(dm)
			})
			dmButton.Alignment = widget.ButtonAlignLeading
			dmButton.Importance = widget.LowImportance

			currentUsersList.Objects = append(
				currentUsersList.Objects,
				dmButton,
			)
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

	thread.currentUsersContainer.Objects = []fyne.CanvasObject{currentUsersList}
	thread.currentUsersContainer.Refresh()
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
