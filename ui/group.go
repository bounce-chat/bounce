package ui

import (
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

func (fyneUI *Fyne) NewGroupChat(bounceThread chat.Group) {
	id := bounceThread.ID
	name := bounceThread.Name
	userIDs := bounceThread.UserIDs

	if _, exists := fyneUI.groups[id]; exists {
		log.WithFields(log.Fields{
			"id":   id,
			"name": name,
		}).Warn("attempt to create a thread that already exists, ignored")
		return
	}

	thread := &group{
		id:                      id,
		name:                    binding.NewString(),
		users:                   newUserStore(),
		pendingUsers:            newUserStore(),
		scroll:                  container.NewVScroll(container.NewVBox()),
		availableNewUsersScroll: container.NewVScroll(container.NewVBox()),
		currentUsersContainer:   container.NewMax(),
		notificationsEnabled:    binding.NewBool(),
		lastMessage:             time.Now().Unix(),
	}
	for _, userID := range userIDs {
		user, exists := fyneUI.users.get(userID)
		if !exists {
			log.WithFields(log.Fields{
				"user_id": userID,
			}).Error("attempted to create thread with user unknown to UI")
			return
		} else {
			thread.users.add(user)
		}
	}

	err := thread.name.Set(name)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("data bindings are broken")
	}
	err = thread.notificationsEnabled.Set(false) // TODO: this should default to true when ready
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("data bindings are broken")
	}

	fyneUI.buildEditThreadContainer(thread)
	editButton := widget.NewButton("Edit", func() {
		fyneUI.refreshUserSelections(thread)
		fyneUI.showEditThreadContainer(thread)
	})
	threadIcon := newEmbeddedResource("assets/not_found.png")
	threadIconCanvas := canvas.NewImageFromResource(newEmbeddedResource("assets/not_found.png"))
	threadIconCanvas.FillMode = canvas.ImageFillContain
	threadIconCanvas.SetMinSize(fyne.NewSize(32, 32))

	threadLabelText := widget.NewLabel(name)
	threadLabel := container.NewHBox(
		threadIconCanvas,
		threadLabelText,
	)

	threadLabelText.Bind(thread.name)
	threadLabelText.TextStyle = fyne.TextStyle{Bold: true}
	threadButtons := container.NewMax(editButton)
	thread.header = container.New(
		layout.NewBorderLayout(nil, nil, threadLabel, threadButtons),
		threadLabel,
		threadButtons,
	)

	thread.entryBar = fyneUI.buildThreadEntry(thread)

	thread.button = widget.NewButtonWithIcon(name, threadIcon, func() {
		// TODO: tell the entine this is happening so that it can make sure
		// there's a connection open
		fyneUI.displayThread(thread)
	})
	thread.button.Importance = widget.LowImportance
	thread.button.Alignment = widget.ButtonAlignLeading

	thread.view = container.New(
		layout.NewBorderLayout(thread.header, thread.entryBar, nil, nil),
		thread.header,
		thread.entryBar,
		thread.scroll,
	)
	fyneUI.groups[id] = thread
	fyneUI.refreshThreadOrder()
}

func (fyneUI *Fyne) ReceivedGroupMessage(msg chat.Message) {
	// Log an error and early return if the group doesn't exist
	group, exists := fyneUI.groups[msg.Destination]
	if !exists {
		log.WithFields(log.Fields{
			"group_id": msg.Destination,
		}).Error("group does not exist on message receive, ignoring the message")
		return
	}

	chatHistory := group.scroll.Content.(*fyne.Container)

	user, exists := group.users.get(msg.Source)
	if !exists {
		log.WithFields(log.Fields{
			"user_id": msg.Source,
		}).Error("group received a message from user ID not in thread")
		return
	}
	profileButton := widget.NewButtonWithIcon("", newEmbeddedResource("assets/not_found.png"), func() { // TODO: get image from user
		log.Info("user wants to open the profile of " + user.name)
		// TODO: display this user's profile
	})
	messageBox := newChatBubble(user.name, msg.Text, false, time.Now().Unix(), profileButton)

	// Check if we're already scrolled to the bottom, for auto-scroll reasons
	autoscroll := false
	location := group.scroll.Offset.Y
	height := group.scroll.Content.Size().Height - group.scroll.Size().Height
	if height == location {
		autoscroll = true
	}

	// Add the message to the group
	chatHistory.Objects = append(chatHistory.Objects, messageBox)
	chatHistory.Refresh()
	group.scroll.Refresh()

	if fyneUI.isActive(group) {
		if autoscroll {
			group.scroll.ScrollToBottom()
			group.scroll.Refresh()
		}
	} else {
		// This group isn't active, mark the button as unread
		group.button.Importance = widget.HighImportance
		group.button.Refresh()
	}

	group.lastMessage = time.Now().Unix()
	fyneUI.refreshThreadOrder()

	notificationsMuted := time.Now().Unix() < group.notificationsMutedUntil
	notificationsEnabled, err := group.notificationsEnabled.Get()
	if err != nil {
		log.Fatal("data bindings are broken")
	}

	groupName, err := group.name.Get()
	if err != nil {
		log.Fatal("data bindings are broken")
	}
	if notificationsEnabled && !notificationsMuted && !autoscroll { //TODO: also notify if not focused?
		fyneUI.app.SendNotification(fyne.NewNotification(groupName, "New message from "+user.name))
	}
}
