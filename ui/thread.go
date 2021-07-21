package ui

import (
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/hkparker/bounce/chat"
)

type thread struct {
	id                           string
	name                         binding.String
	users                        *userStore
	pendingUsers                 *userStore
	isDM                         bool         // TODO: prevents things like showing an option to set an image
	notificationsEnabled         binding.Bool // TODO: this makes it take effect before save is hit, do I want that?
	notificationsMutedUntil      int64        // TODO: fyne feature request/PR: support binding int64 for time.Time
	editContainer                *fyne.Container
	addUsersWindow               fyne.Window
	view                         *fyne.Container
	header                       *fyne.Container
	button                       *widget.Button
	scroll                       *container.Scroll
	availableNewUsersScroll      *container.Scroll
	currentUsersContainer        *fyne.Container
	currentUsersDMLinksContainer *fyne.Container
	entry                        *threadEntry
	entryBar                     *fyne.Container
	lastMessage                  int64
}

func (fyneUI *Fyne) refreshCurrentUsersWithDMLinks(thread *thread) {
	currentUsersList := container.NewVBox()

	for _, thisUser := range thread.users.alphabetized() {
		func(u *user) {
			userIconCanvas := canvas.NewImageFromResource(newEmbeddedResource("assets/not_found.png"))
			userIconCanvas.FillMode = canvas.ImageFillContain
			userIconCanvas.SetMinSize(fyne.NewSize(24, 24)) // TODO: figure out how to get the ideal consistent size

			dmButton := widget.NewButton(u.name, func() {
				// TODO: hide this window and open a DM
				// check fyneUI for existing DMs, create thread if needed and focus it
			}) // TODO: when arbitrary canvas objects can be clicked, we want the icon to be clickable too
			dmButton.Alignment = widget.ButtonAlignLeading
			dmButton.Importance = widget.LowImportance

			currentUsersList.Objects = append(
				currentUsersList.Objects,
				container.NewHBox(
					userIconCanvas,
					dmButton,
				),
			)
		}(thisUser)
	}

	thread.currentUsersDMLinksContainer.Objects = []fyne.CanvasObject{currentUsersList}
	thread.currentUsersDMLinksContainer.Refresh()
}

func (fyneUI *Fyne) refreshCurrentAndPendingUsers(thread *thread) {
	currentUsersList := container.NewVBox()

	for _, thisUser := range thread.users.alphabetized() {
		func(u *user) {
			userIconCanvas := canvas.NewImageFromResource(newEmbeddedResource("assets/not_found.png"))
			userIconCanvas.FillMode = canvas.ImageFillContain
			userIconCanvas.SetMinSize(fyne.NewSize(24, 24)) // TODO: figure out how to get the ideal consistent size

			currentUsersList.Objects = append(
				currentUsersList.Objects,
				container.NewHBox(
					userIconCanvas,
					widget.NewLabel(u.name),
				),
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

func (fyneUI *Fyne) refreshAvailableNewUsers(thread *thread) {
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

func (fyneUI *Fyne) buildAddUsersThreadWindow(thread *thread) {
	thread.addUsersWindow = fyneUI.app.NewWindow("Add Users")
	thread.addUsersWindow.SetCloseIntercept(func() {
		thread.addUsersWindow.Hide()
	})

	//
	// List all users who are already in this thread
	//
	currentUsersLabel := widget.NewLabel("Current Users:")
	currentUsersListView := container.New(
		layout.NewBorderLayout(currentUsersLabel, nil, nil, nil),
		currentUsersLabel,
		thread.currentUsersContainer, // TODO: eventually this will get so long it breaks the UI.  Need a better looking scroll wrapper.
	)

	//
	// List all users who are not in this thread
	//
	fyneUI.refreshAvailableNewUsers(thread)
	addUsersLabel := widget.NewLabel("Add Users:")
	allUsersList := container.New(
		layout.NewBorderLayout(addUsersLabel, nil, nil, nil),
		addUsersLabel,
		thread.availableNewUsersScroll,
	)

	//
	// Buttons to confirm or cancel the new user additions
	//
	saveAddUsersButton := widget.NewButton("Save", func() {
		for _, user := range thread.pendingUsers.alphabetized() {
			thread.users.add(user)
			thread.pendingUsers.remove(user.id)
			fyneUI.onAddUserToGroup(thread.id, user.id)
		}
		fyneUI.refreshUserSelections(thread)
		thread.addUsersWindow.Hide()
	})
	saveAddUsersButton.Importance = widget.HighImportance
	cancelAddUsersButton := widget.NewButton("Cancel", func() {
		// Reset everything
		thread.pendingUsers.empty()
		fyneUI.refreshUserSelections(thread)
		thread.addUsersWindow.Hide()
	})
	addUsersActionButtons := container.New(
		layout.NewBorderLayout(nil, nil, cancelAddUsersButton, saveAddUsersButton),
		saveAddUsersButton,
		cancelAddUsersButton,
	)

	addUsersContent := container.New(
		layout.NewBorderLayout(currentUsersListView, addUsersActionButtons, nil, nil),
		currentUsersListView,
		allUsersList,
		addUsersActionButtons,
	)
	thread.addUsersWindow.SetContent(addUsersContent)

}

func (fyneUI *Fyne) buildThreadEntry(thread *thread) *fyne.Container {
	entry := newThreadEntry(5)
	thread.entry = entry

	entry.customOnSubmitted = func() {
		message := entry.Text
		fyneUI.onSendMessage(chat.OutgoingMessage{
			CreatedAt:   time.Now().Unix(),
			Destination: thread.id,
			Text:        message,
		})
		fyneUI.displaySentMessage(thread, message)
		entry.Text = ""
		entry.Refresh()
	}

	return container.NewMax(entry)
}

//
// User interface manipulation
//

func (fyneUI *Fyne) displayThread(thread *thread) {
	fyneUI.activeThread = thread.id
	fyneUI.chatContainer.Objects = []fyne.CanvasObject{thread.view}
	fyneUI.chatContainer.Refresh()
	thread.button.Importance = widget.LowImportance
	thread.button.Refresh()
	fyneUI.mainWindow.Canvas().Focus(thread.entry)
}

func (fyneUI *Fyne) isActive(thread *thread) bool {
	return fyneUI.activeThread == thread.id
}

func (fyneUI *Fyne) refreshUserSelections(thread *thread) {
	fyneUI.refreshCurrentUsersWithDMLinks(thread)
	fyneUI.refreshCurrentAndPendingUsers(thread)
	fyneUI.refreshAvailableNewUsers(thread)
}
