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
	log "github.com/sirupsen/logrus"
)

type thread struct {
	id                      string
	name                    binding.String
	users                   *userStore
	pendingUsers            *userStore
	isDM                    bool         // TODO: prevents things like showing an option to set an image or editing the name
	notificationsEnabled    binding.Bool // TODO: this makes it take effect before save is hit, do I want that?
	notificationsMutedUntil int64        // TODO: fyne feature request/PR: support binding int64 for time.Time
	editContainer           *fyne.Container
	addUsersWindow          fyne.Window
	view                    *fyne.Container
	header                  *fyne.Container
	button                  *widget.Button
	scroll                  *container.Scroll
	availableNewUsersScroll *container.Scroll
	currentUsersContainer   *fyne.Container
	entry                   *threadEntry
	entryBar                *fyne.Container
	lastMessage             int64
}

func (fyneUI *Fyne) refreshCurrentAndPendingUsers(thread *thread) {
	currentUsersList := container.NewVBox()

	for _, thisUser := range thread.users.alphabetized() {
		func(u *user) {
			dmButton := widget.NewButtonWithIcon(u.name, newEmbeddedResource("assets/not_found.png"), func() {
				log.Info("user wants to open a DM with " + u.name)
				// TODO: hide this window and open a DM
				// check fyneUI for existing DMs, create thread if needed and focus it
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
	thread.availableNewUsersScroll.Content = allUsersListBox // TODO: this scroll behaves weird.  Why?
	thread.availableNewUsersScroll.Refresh()
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
	fyneUI.refreshCurrentAndPendingUsers(thread)
	fyneUI.refreshAvailableNewUsers(thread)
}
