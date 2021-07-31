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

func (dm *directMessage) chatHistoryScroll() *container.Scroll {
	return dm.scroll
}

func (dm *directMessage) getButton() *widget.Button {
	return dm.button
}

func (dm *directMessage) getLastMessage() int64 {
	return dm.lastMessage
}

func (dm *directMessage) setLastMessage(time int64) {
	dm.lastMessage = time
}

func (dm *directMessage) getID() string {
	return dm.user.id
}

func (dm *directMessage) getView() *fyne.Container {
	return dm.view
}
func (dm *directMessage) getEntry() *threadEntry {
	return dm.entry
}

func (fyneUI *Fyne) ReceivedDirectMessage(msg chat.Message) {
	user, userExists := fyneUI.users.get(msg.Source)
	if !userExists {
		log.WithFields(log.Fields{
			"user_id": msg.Source,
		}).Error("chat engine sent DM from user unknown to UI")
		return
	}

	dm, dmExists := fyneUI.dms[msg.Source]
	if !dmExists {
		fyneUI.NewDirectMessage(chat.User{
			ID:   user.id,
			Name: user.name,
		})
		dm, dmExists = fyneUI.dms[msg.Source]
		if !dmExists {
			log.Fatal("DM doesn't exist immediately after creation")
		}
	}

	chatHistory := dm.scroll.Content.(*fyne.Container)

	autoscroll := false
	location := dm.scroll.Offset.Y
	height := dm.scroll.Content.Size().Height - dm.scroll.Size().Height
	if height == location {
		autoscroll = true
	}

	chatHistory.Objects = append(
		chatHistory.Objects,
		newChatBubble(user.name, msg.Text, false, time.Now().Unix(), nil), // TODO: user's name should be a binding.  Display the creation time too, if needed
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
		dm.button.Importance = widget.HighImportance
		dm.button.Refresh()
	}

	dm.lastMessage = time.Now().Unix()
	fyneUI.refreshThreadOrder()

	notificationsMuted := time.Now().Unix() < dm.notificationsMutedUntil
	notificationsEnabled, err := dm.notificationsEnabled.Get()
	if err != nil {
		log.Fatal("data bindings are broken")
	}

	if notificationsEnabled && !notificationsMuted && !autoscroll { //TODO: also notify if not focused?
		fyneUI.app.SendNotification(fyne.NewNotification(user.name, msg.Text))
	}
}

func (fyneUI *Fyne) NewDirectMessage(bounceUser chat.User) {
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

	fyneUI.buildEditDMContainer(dm) // TODO: dm.buildEditContainer
	editButton := widget.NewButton("Edit", func() {
		fyneUI.showEditDMContainer(dm) // TODO: dm.showEditContainer()
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
		message := chat.Message{ // TODO: if dm, fyneUI.onSendDirectMessage(chat.OutgoingDirectMessage
			CreatedAt:   time.Now().Unix(),
			Destination: dm.user.id,
			Text:        entry.Text,
		}

		fyneUI.onSendMessage(message)
		// TODO: if dm, fyneUI.onSendDirectMessage(chat.OutgoingDirectMessage, etc?
		fyneUI.displaySentMessage(dm, message.Text) // TODO: should be a pointer receiver on dm?

		entry.Text = ""
		entry.Refresh()
	}
	dm.entryBar = container.NewMax(entry)

	dm.button = widget.NewButtonWithIcon(user.name, userIcon, func() {
		// TODO: tell the entine this is happening so that it can make sure
		// there's a connection open
		fyneUI.displayThread(dm)
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

func (fyneUI *Fyne) buildEditDMContainer(dm *directMessage) {}
func (fyneUI *Fyne) showEditDMContainer(dm *directMessage)  {}
