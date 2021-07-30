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

type group struct {
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

func (group *group) chatHistoryScroll() *container.Scroll {
	return group.scroll
}

func (group *group) getButton() *widget.Button {
	return group.button
}

func (group *group) getLastMessage() int64 {
	return group.lastMessage
}

func (group *group) setLastMessage(time int64) {
	group.lastMessage = time
}

func (group *group) getID() string {
	return group.id
}

func (group *group) getNotificationsEnabled() bool {
	enabled, err := group.notificationsEnabled.Get()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("data bindings are broken")
	}
	return enabled
}

func (group *group) getNotificationsMutedUntil() int64 {
	return group.notificationsMutedUntil
}

func (group *group) getView() *fyne.Container {
	return group.view
}
func (group *group) getEntry() *threadEntry {
	return group.entry
}

//
//
//
//
//
//

func (fyneUI *Fyne) refreshCurrentAndPendingUsers(thread *group) {
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
	thread.availableNewUsersScroll.Content = allUsersListBox // TODO: this scroll behaves weird.  Why?
	thread.availableNewUsersScroll.Refresh()
}

func (fyneUI *Fyne) buildThreadEntry(thread *group) *fyne.Container {
	entry := newThreadEntry(5)
	thread.entry = entry

	entry.customOnSubmitted = func() {
		message := chat.Message{ // TODO: if dm, fyneUI.onSendDirectMessage(chat.OutgoingDirectMessage
			CreatedAt:   time.Now().Unix(),
			Destination: thread.id,
			Text:        entry.Text,
		}

		fyneUI.onSendMessage(message)
		// TODO: if dm, fyneUI.onSendDirectMessage(chat.OutgoingDirectMessage, etc?
		fyneUI.displaySentMessage(thread, message.Text) // TODO: should be a pointer receiver on thread?

		entry.Text = ""
		entry.Refresh()
	}

	return container.NewMax(entry)
}

//
// User interface manipulation
//

func (fyneUI *Fyne) displayThread(thread thread) {
	fyneUI.activeThread = thread.getID()
	fyneUI.chatContainer.Objects = []fyne.CanvasObject{thread.getView()}
	fyneUI.chatContainer.Refresh()
	thread.getButton().Importance = widget.LowImportance
	thread.getButton().Refresh()
	fyneUI.mainWindow.Canvas().Focus(thread.getEntry())
}

func (fyneUI *Fyne) isActive(thread thread) bool {
	return fyneUI.activeThread == thread.getID()
}

func (fyneUI *Fyne) refreshUserSelections(thread *group) {
	fyneUI.refreshCurrentAndPendingUsers(thread)
	fyneUI.refreshAvailableNewUsers(thread)
}
