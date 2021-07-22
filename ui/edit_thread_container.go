package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	log "github.com/sirupsen/logrus"
)

func (fyneUI *Fyne) showEditThreadContainer(thread *thread) {
	fyneUI.mainWindow.SetContent(thread.editContainer)
	thread.editContainer.Show()
}

func (fyneUI *Fyne) buildEditThreadContainer(thread *thread) {
	threadNameEntry := widget.NewEntry()
	currentThreadName, err := thread.name.Get()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("data bindings are broken")
	}
	threadNameEntry.Text = currentThreadName

	threadIcon := canvas.NewImageFromResource(newEmbeddedResource("assets/not_found.png"))
	threadIcon.FillMode = canvas.ImageFillContain
	threadIcon.SetMinSize(fyne.NewSize(64, 64))

	notificationsCheck := widget.NewCheckWithData("Enable notifications", thread.notificationsEnabled)
	notificationsCheck.OnChanged = func(state bool) { // TODO: do we really want this to apply before save?
		fyneUI.onChangeNotificationSettings(thread.id, state)
	}

	saveButton := widget.NewButton("Save", func() {
		// Update the thread name if it was changed
		currentThreadName, err := thread.name.Get()
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("data bindings are broken")
		}
		newThreadName := threadNameEntry.Text
		if currentThreadName != newThreadName {
			err = thread.name.Set(newThreadName)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("data bindings are broken")
			}
			thread.button.Text = newThreadName
			thread.button.Refresh()
			fyneUI.onRenameGroup(thread.id, newThreadName)
		}
		// Add the selected users to the group
		for _, user := range thread.pendingUsers.alphabetized() {
			thread.users.add(user)
			thread.pendingUsers.remove(user.id)
			fyneUI.onAddUserToGroup(thread.id, user.id)
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
		threadNameEntry.Text = currentThreadName
		threadNameEntry.Refresh()
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
		//thread.currentUsersDMLinksContainer,
		thread.currentUsersContainer, // TODO: eventually this will get so long it breaks the UI.  Need a better looking scroll wrapper.
	)

	topOptionsVBox := container.NewVBox(
		threadIcon,
		threadNameEntry,
		notificationsCheck,
		currentUsersListView,
	)

	fyneUI.refreshAvailableNewUsers(thread)
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
