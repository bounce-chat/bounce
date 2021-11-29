package ui

import (
	"time"

	"github.com/google/uuid"
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

type directMessage struct {
	user                    *user
	notificationsEnabled    binding.Bool // TODO: this makes it take effect before save is hit, do I want that?
	notificationsMutedUntil int64        // TODO: fyne feature request/PR: support binding int64 for time.Time
	editContainer           *fyne.Container
	view                    *fyne.Container
	header                  *fyne.Container
	button                  *widget.Button
	scroll                  *container.Scroll
	entry                   *threadEntry
	entryBar                *fyne.Container
	lastMessage             int64
}

func (dm *directMessage) getID() uuid.UUID {
	return dm.user.id
}

func (dm *directMessage) getView() *fyne.Container {
	return dm.view
}
func (dm *directMessage) getEntry() *threadEntry {
	return dm.entry
}

func (dm *directMessage) chatHistoryScroll() *container.Scroll {
	return dm.scroll
}

func (dm *directMessage) getButton() *widget.Button {
	return dm.button
}

func (dm *directMessage) getLastMessageTime() int64 {
	return dm.lastMessage
}

func (dm *directMessage) setLastMessageTime(time int64) {
	dm.lastMessage = time
}

func (fyneUI *Fyne) NewDirectMessage(bounceUser chat.User) { // TODO: Should wrap around something that takes the internal user object
	user, exists := fyneUI.users.get(bounceUser.ID)
	if !exists {
		log.WithFields(log.Fields{
			"user_id":   bounceUser.ID,
			"user_name": bounceUser.Name,
		}).Error("cannot create DM with user unknown to the UI")
		return
	}
	if _, exists := fyneUI.dms[bounceUser.ID]; exists {
		log.WithFields(log.Fields{
			"user_id":   bounceUser.ID,
			"user_name": bounceUser.Name,
		}).Error("attempt to create a DM that already exists, ignoring")
		return
	}

	dm := &directMessage{
		user:                 user,
		scroll:               container.NewVScroll(container.NewVBox()),
		notificationsEnabled: binding.NewBool(),
		lastMessage:          time.Now().Unix(),
	}

	err := dm.notificationsEnabled.Set(false) // TODO: this should default to true when ready
	if err != nil {
		log.WithFields(log.Fields{
			"at":    "fyneUI.NewDirectMessage",
			"error": err.Error(),
		}).Fatal("data bindings are broken")
	}

	fyneUI.buildEditDMContainer(dm)
	editButton := widget.NewButton("Edit", func() {
		fyneUI.showEditDMContainer(dm)
	})
	userIcon := newEmbeddedResource("assets/not_found.png")
	userIconCanvas := canvas.NewImageFromResource(newEmbeddedResource("assets/not_found.png"))
	userIconCanvas.FillMode = canvas.ImageFillContain
	userIconCanvas.SetMinSize(fyne.NewSize(32, 32))

	userLabelText := widget.NewLabel(user.name)
	userLabel := container.NewHBox(
		userIconCanvas,
		userLabelText,
	)

	//userLabelText.Bind(user.name) // TODO: user objects should have bindings as names and take updates from the chat engine
	userLabelText.TextStyle = fyne.TextStyle{Bold: true}
	dmButtons := container.NewMax(editButton)
	dm.header = container.New(
		layout.NewBorderLayout(nil, nil, userLabel, dmButtons),
		userLabel,
		dmButtons,
	)

	//
	// BUild the entry bar
	//
	entry := newThreadEntry(5)
	dm.entry = entry
	entry.customOnSubmitted = func() {
		message := &chat.DirectMessage{
			Destination: dm.user.id,
			Text:        entry.Text,
		}

		id := fyneUI.callbacks.SendDirectMessage(message) // TODO: store reference to ID?
		fyneUI.displaySentMessage(dm, id, message.Text)   // TODO: just build the bubble around the actual object?

		entry.Text = ""
		entry.Refresh()
	}
	dm.entryBar = container.NewMax(entry)

	dm.button = widget.NewButtonWithIcon(user.name, userIcon, func() {
		fyneUI.displayThread(dm)
		fyneUI.callbacks.UserConnectionDesired(user.id)
	})
	dm.button.Importance = widget.LowImportance
	dm.button.Alignment = widget.ButtonAlignLeading

	dm.view = container.New(
		layout.NewBorderLayout(dm.header, dm.entryBar, nil, nil),
		dm.header,
		dm.entryBar,
		dm.scroll,
	)
	fyneUI.dms[bounceUser.ID] = dm
	fyneUI.refreshThreadOrder()
}

func (fyneUI *Fyne) ReceivedDirectMessage(msg chat.DirectMessage) {
	fyneUI.loadDirectMessage(msg, false, false)
}

func (fyneUI *Fyne) loadDirectMessage(msg chat.DirectMessage, overrideScroll, hideNotification bool) {
	user, userExists := fyneUI.users.get(msg.Source)
	if !userExists {
		log.WithFields(log.Fields{
			"user_id": msg.Source,
		}).Error("chat engine sent DM from user unknown to UI")
		return
	}

	if fyneUI.profile == nil {
		log.Fatal("cannot load direct message if no profile exists")
	}

	displayName := user.name
	isOutgoing := false
	targetDM := msg.Source
	// TODO: this really needs to be cleaned up
	if msg.Source == fyneUI.profile.id {
		// We're learning about a DM we sent from another device
		displayName = "You"
		isOutgoing = true
		targetDM = msg.Destination
		hideNotification = true
	}

	dm, dmExists := fyneUI.dms[targetDM]
	if !dmExists {
		fyneUI.NewDirectMessage(chat.User{
			ID:   targetDM,
			Name: user.name,
		})
		dm, dmExists = fyneUI.dms[targetDM]
		if !dmExists {
			log.Fatal("DM doesn't exist immediately after creation")
		}
	}
	fyneUI.threadWithMessageMutex.Lock()
	fyneUI.threadWithMessage[msg.ID] = dm
	fyneUI.threadWithMessageMutex.Unlock()

	chatHistory := dm.scroll.Content.(*fyne.Container)

	autoscroll := false
	location := dm.scroll.Offset.Y
	height := dm.scroll.Content.Size().Height - dm.scroll.Size().Height
	if height == location {
		autoscroll = true
	}

	chatHistory.Objects = append(
		chatHistory.Objects,
		newChatBubble(displayName, msg.ID, msg.Text, isOutgoing, time.Now().Unix(), nil), // TODO: user's name should be a binding.  Display the creation time too, if needed
	)
	chatHistory.Refresh()
	dm.scroll.Refresh()

	if fyneUI.isActive(dm) {
		if autoscroll {
			dm.scroll.ScrollToBottom()
			dm.scroll.Refresh()
		}
	} else {
		// This thread isn't active, mark the button as unread
		if !hideNotification {
			dm.button.Importance = widget.HighImportance
			dm.button.Refresh()
		}
	}

	if overrideScroll {
		dm.scroll.ScrollToBottom()
		dm.scroll.Refresh()
	}

	dm.lastMessage = time.Now().Unix()
	fyneUI.refreshThreadOrder()

	notificationsMuted := time.Now().Unix() < dm.notificationsMutedUntil
	notificationsEnabled, err := dm.notificationsEnabled.Get()
	if err != nil {
		log.Fatal("data bindings are broken")
	}

	// TODO: different criteria on mobile probably.  need to clean this up and add context around fyneUI.focused
	if notificationsEnabled && !notificationsMuted && !autoscroll && !hideNotification {
		fyneUI.app.SendNotification(fyne.NewNotification(user.name, msg.Text))
	}
}

func (fyneUI *Fyne) showEditDMContainer(dm *directMessage) {
	fyneUI.mainWindow.SetContent(dm.editContainer)
	dm.editContainer.Show()
}

func (fyneUI *Fyne) buildEditDMContainer(dm *directMessage) {
	threadIcon := canvas.NewImageFromResource(newEmbeddedResource("assets/not_found.png"))
	threadIcon.FillMode = canvas.ImageFillContain
	threadIcon.SetMinSize(fyne.NewSize(64, 64))

	username := widget.NewLabel(dm.user.name)

	notificationsCheck := widget.NewCheckWithData("Enable notifications", dm.notificationsEnabled)
	notificationsCheck.OnChanged = func(state bool) { // TODO: do we really want this to apply before save?
		dm.notificationsEnabled.Set(state) // TODO: error check
		fyneUI.callbacks.SetDMNotificationSettings(dm.user.id, state)
	}

	saveButton := widget.NewButton("Save", func() {
		fyneUI.showMainContainer()
	})
	saveButton.Importance = widget.HighImportance
	cancelButton := widget.NewButton("Cancel", func() {
		fyneUI.showMainContainer()
	})
	actionButtons := container.New(
		layout.NewBorderLayout(nil, nil, cancelButton, saveButton),
		saveButton,
		cancelButton,
	)
	topOptionsVBox := container.NewVBox(
		threadIcon,
		username,
		notificationsCheck,
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

	// TODO: add option to introduce this contact to someone
	dm.editContainer = container.NewMax(
		container.New(
			layout.NewBorderLayout(closeBar, actionButtons, nil, nil),
			container.New(
				layout.NewBorderLayout(topOptionsVBox, nil, nil, nil),
				topOptionsVBox,
			),
			closeBar,
			actionButtons,
		),
	)

}
