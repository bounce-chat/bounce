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
	notificationsCheck.OnChanged = func(state bool) {
		fyneUI.onChangeNotificationSettings(thread.id, state)
	}

	saveButton := widget.NewButton("Save", func() {
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
		fyneUI.showMainContainer()
	})
	saveButton.Importance = widget.HighImportance
	cancelButton := widget.NewButton("Cancel", func() {
		threadNameEntry.Text = currentThreadName
		threadNameEntry.Refresh()
		fyneUI.showMainContainer()
	})
	actionButtons := container.New(
		layout.NewBorderLayout(nil, nil, cancelButton, saveButton),
		saveButton,
		cancelButton,
	)

	usersLabel := widget.NewLabel("Users:")
	addUserButton := widget.NewButton("  +  ", func() {
		fyneUI.refreshUserSelections(thread)
		thread.addUsersWindow.Show()
	})
	addUserButton.Importance = widget.LowImportance
	usersTitle := container.New(
		layout.NewBorderLayout(nil, nil, usersLabel, addUserButton),
		usersLabel,
		addUserButton,
	)

	topOptionsVBox := container.NewVBox(
		threadIcon,
		threadNameEntry,
		notificationsCheck,
		usersTitle,
	)

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
				layout.NewPaddedLayout(),
				container.New(
					layout.NewBorderLayout(topOptionsVBox, nil, nil, nil),
					topOptionsVBox,
					container.NewVScroll(thread.currentUsersDMLinksContainer),
				),
			),
			closeBar,
			actionButtons,
		),
	)
}
