package ui

import (
	"time"

	"github.com/hkparker/bounce/chat"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

type group struct {
	id                      uuid.UUID
	name                    binding.String
	users                   *userStore
	pendingUsers            *userStore
	notificationsEnabled    binding.Bool // TODO: this makes it take effect before save is hit, do I want that?
	notificationsMutedUntil int64        // TODO: fyne feature request/PR: support binding int64 for time.Time
	editContainer           *fyne.Container
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

func (group *group) getID() uuid.UUID {
	return group.id
}

func (group *group) getView() *fyne.Container {
	return group.view
}
func (group *group) getEntry() *threadEntry {
	return group.entry
}

func (group *group) chatHistoryScroll() *container.Scroll {
	return group.scroll
}

func (group *group) getButton() *widget.Button {
	return group.button
}

func (group *group) getLastMessageTime() int64 {
	return group.lastMessage
}

func (group *group) setLastMessageTime(time int64) {
	group.lastMessage = time
}

func (fyneUI *Fyne) OpenNewGroupChat(bounceGroup chat.Group) {
	fyneUI.NewGroupChat(bounceGroup)

	group, exists := fyneUI.groups[bounceGroup.ID]
	if exists {
		fyneUI.showMainContainer()
		fyneUI.displayThread(group)
	} else {
		log.Error("cannot open newly created group because the UI isn't aware of it")
	}
}

func (fyneUI *Fyne) NewGroupChat(bounceGroup chat.Group) {
	if _, exists := fyneUI.groups[bounceGroup.ID]; exists {
		log.WithFields(log.Fields{
			"id":   bounceGroup.ID,
			"name": bounceGroup.Name,
		}).Warn("requested to create a group that already exists, ignored")
		return
	}

	group := &group{
		id:                      bounceGroup.ID,
		name:                    binding.NewString(),
		users:                   newUserStore(),
		pendingUsers:            newUserStore(),
		scroll:                  container.NewVScroll(container.NewVBox()),
		availableNewUsersScroll: container.NewVScroll(container.NewVBox()),
		currentUsersContainer:   container.NewMax(),
		notificationsEnabled:    binding.NewBool(),
		lastMessage:             time.Now().Unix(),
	}
	for _, userID := range bounceGroup.UserIDs {
		user, exists := fyneUI.users.get(userID)
		if !exists {
			log.WithFields(log.Fields{
				"user_id": userID,
			}).Error("attempted to create group with user unknown to UI")
			return
		} else {
			group.users.add(user)
		}
	}

	err := group.name.Set(bounceGroup.Name)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("data bindings are broken")
	}
	err = group.notificationsEnabled.Set(false) // TODO: this should default to true when ready
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("data bindings are broken")
	}

	fyneUI.buildEditThreadContainer(group)
	editButton := widget.NewButton("Edit", func() {
		fyneUI.refreshUserSelections(group)
		fyneUI.showEditThreadContainer(group)
	})
	groupIcon := newEmbeddedResource("assets/not_found.png")
	groupIconCanvas := canvas.NewImageFromResource(newEmbeddedResource("assets/not_found.png"))
	groupIconCanvas.FillMode = canvas.ImageFillContain
	groupIconCanvas.SetMinSize(fyne.NewSize(32, 32))

	groupLabelText := widget.NewLabel(bounceGroup.Name)
	groupLabel := container.NewHBox(
		groupIconCanvas,
		groupLabelText,
	)

	groupLabelText.Bind(group.name)
	groupLabelText.TextStyle = fyne.TextStyle{Bold: true}
	groupButtons := container.NewMax(editButton)
	group.header = container.New(
		layout.NewBorderLayout(nil, nil, groupLabel, groupButtons),
		groupLabel,
		groupButtons,
	)

	entry := newThreadEntry(5)
	group.entry = entry

	entry.customOnSubmitted = func() {
		message := &chat.GroupMessage{
			Destination: group.id,
			Text:        entry.Text,
		}

		id := fyneUI.callbacks.SendGroupMessage(message)
		fyneUI.displaySentMessage(group, id, message.Text)

		entry.Text = ""
		entry.Refresh()
	}

	group.entryBar = container.NewMax(entry)

	group.button = widget.NewButtonWithIcon(bounceGroup.Name, groupIcon, func() {
		// TODO: tell the entine this is happening so that it can make sure
		// there's a connection open
		fyneUI.displayThread(group)
	})
	group.button.Importance = widget.LowImportance
	group.button.Alignment = widget.ButtonAlignLeading

	group.view = container.New(
		layout.NewBorderLayout(group.header, group.entryBar, nil, nil),
		group.header,
		group.entryBar,
		group.scroll,
	)
	fyneUI.groups[group.id] = group
	fyneUI.refreshThreadOrder()
}

func (fyneUI *Fyne) ReceivedGroupMessage(msg chat.GroupMessage) {
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
	messageBox := newChatBubble(user.name, msg.ID, msg.Text, false, time.Now().Unix(), profileButton)

	fyneUI.threadWithMessageMutex.Lock()
	fyneUI.threadWithMessage[msg.ID] = group
	fyneUI.threadWithMessageMutex.Unlock()

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
