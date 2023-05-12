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
	id                        uuid.UUID
	name                      binding.String
	users                     *userStore
	pendingUsers              *userStore
	notificationsMutedUntil   int64
	editContainer             *fyne.Container
	retentionSelection        *widget.Select
	view                      *fyne.Container
	header                    *fyne.Container
	button                    *threadButton
	notificationsEnabledCheck *widget.Check
	scroll                    *container.Scroll
	availableNewUsersScroll   *container.Scroll
	currentUsersContainer     *fyne.Container
	entry                     *threadEntry
	entryBar                  *fyne.Container
	lastMessage               int64
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

func (group *group) getButton() *threadButton {
	return group.button
}

func (group *group) getLastMessageTime() int64 {
	return group.lastMessage
}

func (group *group) setLastMessageTime(time int64) {
	group.lastMessage = time
}

func (fyneUI *Fyne) OpenNewGroupChat(bounceGroup chat.Group) { // TODO: rename "create and open"?
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

	group.notificationsEnabledCheck = widget.NewCheck("Enable notifications", func(_ bool) {})
	group.notificationsMutedUntil, err = fyneUI.callbacks.GetGroupMutedUntil(group.id)
	if err != nil {
		log.WithFields(log.Fields{
			"group_id": group.id,
		}).Error("cannot find muted until for group")
		return
		// TODO: fatal?
	}
	enabled := group.notificationsMutedUntil != chat.MutedForever
	group.notificationsEnabledCheck.SetChecked(enabled)

	fyneUI.buildEditThreadContainer(group)
	editButton := widget.NewButton("Edit", func() {
		fyneUI.refreshUserSelections(group)
		fyneUI.showEditThreadContainer(group)
	})
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
	entry.OnChanged = func(_ string) {
		fyneUI.callbacks.TypingInGroup(group.id)
	}
	entry.customOnSubmitted = func() {
		message := &chat.GroupMessage{
			Thread: group.id,
			Text:   entry.Text,
		}
		fyneUI.callbacks.SendGroupMessage(message)
		fyneUI.displaySentMessage(group, message.ID, message.Text)

		entry.Text = ""
		entry.Refresh()
	}

	group.entryBar = container.NewMax(entry)

	group.button = newThreadButton(todoImage(), bounceGroup.Name, func() {
		fyneUI.displayThread(group)
		fyneUI.callbacks.GroupConnectionDesired(group.id)
	})
	// Keep the last message time counter up to date
	go func() {
		for {
			time.Sleep(1 * time.Minute)
			group.button.updateLastMessageTimeText()
		}
	}()

	// Make sure chaning the name of the group also updates the thread button
	group.name.AddListener(binding.NewDataListener(func() {
		newName, err := group.name.Get()
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("data bindings are broken")
		}
		button := group.getButton()
		button.threadName.Segments[0].(*widget.TextSegment).Text = newName
		button.threadName.Refresh()
	}))

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
	fyneUI.loadGroupMessage(msg, false, false)
}

func (fyneUI *Fyne) loadGroupMessage(msg chat.GroupMessage, overrideScroll, hideNotification bool) {
	// Log an error and early return if the group doesn't exist
	group, exists := fyneUI.groups[msg.Thread]
	if !exists {
		log.WithFields(log.Fields{
			"group_id": msg.Thread,
		}).Error("group does not exist on message receive, ignoring the message")
		return
	}

	chatHistory := group.scroll.Content.(*fyne.Container)

	user, exists := group.users.get(msg.Author)
	if !exists {
		log.WithFields(log.Fields{
			"user_id": msg.Author,
		}).Error("group received a message from user ID not in thread")
		return
	}
	profileButton := widget.NewButtonWithIcon("", newEmbeddedResource("assets/not_found.png"), func() { // TODO: get image from user
		log.Info("user wants to open the profile of " + user.name)
		// TODO: display this user's profile
	})

	displayName := user.name
	isOutgoing := false
	// TODO: this really needs to be cleaned up
	if msg.Author == fyneUI.profile.id {
		// We're learning about a message we sent from another device
		displayName = "You"
		isOutgoing = true
		hideNotification = true
		profileButton = nil
	}

	messageBox := newChatBubble(displayName, msg.ID, msg.Text, isOutgoing, msg.WrittenAt, profileButton)

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

	group.button.setLastMessage(displayName, msg.Text)
	group.button.setLastMessageTime(time.Unix(msg.WrittenAt, 0))

	if fyneUI.isActive(group) {
		if autoscroll {
			group.scroll.ScrollToBottom()
			group.scroll.Refresh()
		}
	} else {
		if !hideNotification {
			group.button.addUnread()
		}
	}

	if overrideScroll {
		group.scroll.ScrollToBottom()
		group.scroll.Refresh()
	}

	group.lastMessage = msg.WrittenAt
	fyneUI.refreshThreadOrder()

	notificationsEnabled := group.notificationsMutedUntil != chat.MutedForever
	notificationsMuted := time.Now().Unix() < group.notificationsMutedUntil

	groupName, err := group.name.Get()
	if err != nil {
		log.Fatal("data bindings are broken")
	}
	if notificationsEnabled && !notificationsMuted && !autoscroll && !hideNotification { //TODO: also notify if not focused?
		fyneUI.app.SendNotification(fyne.NewNotification(groupName, "New message from "+user.name))
	}
}

func (fyneUI *Fyne) RenameGroup(groupID, actorID uuid.UUID, newName string) {
	// Find the actor
	actor, ok := fyneUI.users.get(actorID)
	actorName := ""
	if !ok {
		actorName = "unknown"
		log.WithFields(log.Fields{
			"actor_id": actorID,
		}).Warn("unknown user just updated group name")
		// TODO: error and return here?
	} else {
		actorName = actor.name
	}

	// Find the group
	group, exists := fyneUI.groups[groupID]
	if !exists {
		log.WithFields(log.Fields{
			"group_id": groupID,
		}).Error("cannot update name of unknown group")
		return
	}

	// Change the group name
	err := group.name.Set(newName)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("data bindings are broken")
	}

	// Calculate if we should autoscroll the new message
	autoscroll := false
	location := group.scroll.Offset.Y
	height := group.scroll.Content.Size().Height - group.scroll.Size().Height
	if height == location {
		autoscroll = true
	}

	// Add a message to the thread indicating the change
	changeString := actorName + " changed the group name to " + newName
	changeLabel := widget.NewLabel(changeString)
	changeLabel.Alignment = fyne.TextAlignCenter
	chatHistory := group.chatHistoryScroll().Content.(*fyne.Container)
	chatHistory.Objects = append(chatHistory.Objects, changeLabel)
	group.chatHistoryScroll().Refresh()

	group.button.setLastAction(changeString)

	if autoscroll {
		if fyneUI.isActive(group) {
			group.scroll.ScrollToBottom()
			group.scroll.Refresh()
		}
	}

	group.setLastMessageTime(time.Now().Unix())
	fyneUI.refreshThreadOrder()
}

func (fyneUI *Fyne) GroupRetentionChanged(groupID, actorID uuid.UUID, retention int64, timestamp int64) {
	// Find the actor
	actor, ok := fyneUI.users.get(actorID)
	actorName := ""
	if !ok {
		actorName = "unknown"
		log.WithFields(log.Fields{
			"actor_id": actorID,
		}).Warn("unknown user just updated group retention")
		// TODO: error and return here?
	} else {
		actorName = actor.name
	}

	// Find the group
	group, exists := fyneUI.groups[groupID]
	if !exists {
		log.WithFields(log.Fields{
			"group_id": groupID,
		}).Error("cannot update retention of unknown group")
		return
	}

	// Update the selection
	newRetentionName := getRetentionName(retention)
	group.retentionSelection.Selected = newRetentionName
	group.retentionSelection.Refresh()

	// Calculate if we should autoscroll the new message
	autoscroll := false
	location := group.scroll.Offset.Y
	height := group.scroll.Content.Size().Height - group.scroll.Size().Height
	if height == location {
		autoscroll = true
	}

	// Add a message to the thread indicating the change
	changeString := actorName + " changed the group retention to " + newRetentionName
	changeLabel := widget.NewLabel(changeString)
	changeLabel.Alignment = fyne.TextAlignCenter
	chatHistory := group.chatHistoryScroll().Content.(*fyne.Container)
	chatHistory.Objects = append(chatHistory.Objects, changeLabel)
	group.chatHistoryScroll().Refresh()
	// TODO: thread this in the correct spot in the thread based on timestamp

	group.button.setLastAction(changeString)

	if autoscroll {
		if fyneUI.isActive(group) {
			group.scroll.ScrollToBottom()
			group.scroll.Refresh()
		}
	}

	group.setLastMessageTime(time.Now().Unix())
	fyneUI.refreshThreadOrder()

}

func (fyneUI *Fyne) GroupChatHistoryCleared(groupID, actorID uuid.UUID) { // TODO: DRY with the DM version of this?  it can all be applied to threads in general
	actor, ok := fyneUI.users.get(actorID)
	actorName := ""
	if !ok {
		actorName = "unknown"
		log.WithFields(log.Fields{
			"actor_id": actorID,
		}).Warn("unknown user just updated DM retetion settings")
	} else {
		actorName = actor.name
	}

	if gm, exists := fyneUI.groups[groupID]; exists {
		autoscroll := false
		location := gm.scroll.Offset.Y
		height := gm.scroll.Content.Size().Height - gm.scroll.Size().Height
		if height == location {
			autoscroll = true
		}

		changeString := actorName + " cleared the chat history"
		changeLabel := widget.NewLabel(changeString)
		changeLabel.Alignment = fyne.TextAlignCenter
		chatHistory := gm.chatHistoryScroll().Content.(*fyne.Container)
		chatHistory.Objects = append(chatHistory.Objects, changeLabel)
		gm.chatHistoryScroll().Refresh()

		gm.button.setLastAction(changeString)

		if autoscroll {
			if fyneUI.isActive(gm) {
				gm.scroll.ScrollToBottom()
				gm.scroll.Refresh()
			}
		}

		gm.setLastMessageTime(time.Now().Unix())
		fyneUI.refreshThreadOrder()
	} else {
		log.WithFields(log.Fields{
			"group_id": groupID,
		}).Warn("cannot notify messages cleared for group that doesn't exist")
	}

	// TODO: it's possible we cleared messages and there are messages newer than the clear time
	// that we want to preserve.  In that case, these messages will not have been removed from the
	// history scroll, and we should insert the message about the clearing above them, so it makes
	// sense.  This will require we know the timestamp of the clearing update in the frontend,
	// in fact everything that goes into a thread should follow an interface that has timestamps.
	// once that's done we can insertion sort these in.
}

func (fyneUI *Fyne) GroupMutedUntilChanged(groupID uuid.UUID, mutedUntil int64) {
	if group, exists := fyneUI.groups[groupID]; exists {
		group.notificationsMutedUntil = mutedUntil
		enabled := mutedUntil != chat.MutedForever
		group.notificationsEnabledCheck.SetChecked(enabled)
	} else {
		log.WithFields(log.Fields{
			"group_id": groupID,
		}).Warn("cannot notify messages cleared for group that doesn't exist")
	}
}
