package ui

import (
	"image/color"
	"sort"
	"time"

	"github.com/hkparker/bounce/chat"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	log "github.com/sirupsen/logrus"
)

type Fyne struct {
	app                          fyne.App
	mainWindow                   fyne.Window
	profileWindow                fyne.Window
	addContactWindow             fyne.Window
	createGroupWindow            fyne.Window
	mainContainer                *fyne.Container
	networkLoading               *fyne.Container
	threadVBox                   *fyne.Container
	chatContainer                *fyne.Container
	threads                      map[string]*thread
	users                        *userStore
	activeThread                 string
	onSendMessage                chat.SendMessageCallback
	onAddUserToGroup             chat.AddUserToGroupCallback
	onRenameGroup                chat.RenameGroupCallback
	onChangeNotificationSettings chat.ChangeNotificationSettingsCallback
}

type user struct {
	id   string
	name string
}

func (fyneUI *Fyne) Build(configDirectory string) {
	fyneUI.threads = make(map[string]*thread)
	fyneUI.users = newUserStore()

	fyneUI.app = app.New()
	fyneUI.app.SetIcon(newEmbeddedResource("assets/icon.png"))
	fyneUI.buildNetworkLoading()
	fyneUI.buildProfileWindow()
	fyneUI.buildAddContactWindow()
	fyneUI.buildCreateGroupWindow()
	fyneUI.buildMainWindow()

}

func (fyneUI *Fyne) RegisterCallbacks(callbacks chat.Callbacks) {
	fyneUI.onSendMessage = callbacks.SendMessage
	fyneUI.onAddUserToGroup = callbacks.AddUserToGroup
	fyneUI.onRenameGroup = callbacks.RenameGroup
	fyneUI.onChangeNotificationSettings = callbacks.ChangeNotificationSettings
}

func (fyneUI *Fyne) Run() {
	fyneUI.app.Run()
}

func (fyneUI *Fyne) Quit() {
	fyneUI.app.Quit()
}

func (fyneUI *Fyne) buildProfileWindow() {
	fyneUI.profileWindow = fyneUI.app.NewWindow("Profile")
	fyneUI.profileWindow.SetContent(container.NewMax(widget.NewLabel("Edit your profile")))
	fyneUI.profileWindow.SetCloseIntercept(func() {
		fyneUI.profileWindow.Hide()
	})

}

func (fyneUI *Fyne) buildAddContactWindow() {
	fyneUI.addContactWindow = fyneUI.app.NewWindow("Add Contact")
	fyneUI.addContactWindow.SetContent(container.NewMax(widget.NewLabel("Add a contact")))
	fyneUI.addContactWindow.SetCloseIntercept(func() {
		fyneUI.addContactWindow.Hide()
	})
}

func (fyneUI *Fyne) buildCreateGroupWindow() {
	fyneUI.createGroupWindow = fyneUI.app.NewWindow("Create Group")
	fyneUI.createGroupWindow.SetContent(container.NewMax(widget.NewLabel("Create a group here")))
	fyneUI.createGroupWindow.SetCloseIntercept(func() {
		fyneUI.createGroupWindow.Hide()
	})
}

func (fyneUI *Fyne) buildMainMenu() *fyne.MainMenu {
	return fyne.NewMainMenu(
		fyne.NewMenu(
			"Bounce",
			fyne.NewMenuItem("My Profile", func() {
				fyneUI.profileWindow.Show()
			}),
			fyne.NewMenuItem("Settings", func() {
				//fyneUI.addContactWindow.Show()
			}),
			fyne.NewMenuItem("About", func() {
				//fyneUI.createGroupWindow.Show()
			}),
		),
		fyne.NewMenu(
			"Chat",
			fyne.NewMenuItem("New Group", func() {
				fyneUI.createGroupWindow.Show()
			}),
			fyne.NewMenuItem("New DM", func() {
				//fyneUI.createGroupWindow.Show()
			}),
		),
		fyne.NewMenu(
			"Contacts",
			fyne.NewMenuItem("Introduce", func() {
				//fyneUI.addContactWindow.Show()
			}),
			fyne.NewMenuItem("Import", func() {
				fyneUI.addContactWindow.Show()
			}),
		),
	)
}

func (fyneUI *Fyne) refreshThreadOrder() {
	allThreads := sortableThreads{}
	for _, thread := range fyneUI.threads {
		allThreads = append(allThreads, thread)
	}
	sort.Sort(allThreads)

	buttons := []fyne.CanvasObject{}
	for _, thread := range allThreads {
		buttons = append(buttons, thread.button)
	}
	fyneUI.threadVBox.Objects = buttons
	fyneUI.threadVBox.Refresh()
}

func (fyneUI *Fyne) displaySentMessage(thread *thread, message string) {
	if message == "" {
		return
	}
	chatHistory := thread.scroll.Content.(*fyne.Container)

	// Create the new message box
	usernameText := widget.NewLabel("You")
	usernameText.TextStyle = fyne.TextStyle{Bold: true}
	usernameText.Wrapping = fyne.TextWrapWord
	messageText := widget.NewLabel(message)
	messageText.Wrapping = fyne.TextWrapWord

	messageBox := container.New(
		layout.NewMaxLayout(),
		&canvas.Rectangle{FillColor: color.NRGBA{0, 0, 0x40, 0x40}},
		container.NewVBox(
			usernameText,
			messageText,
			widget.NewSeparator(),
		),
	)

	// Add the message to the thread
	chatHistory.Objects = append(chatHistory.Objects, messageBox)
	chatHistory.Refresh()

	thread.scroll.Refresh()
	thread.scroll.ScrollToBottom()
	thread.scroll.Refresh()

	thread.lastMessage = time.Now().Unix()
	fyneUI.refreshThreadOrder()
}

//
// Exported functions for the chat engine
//

func (fyneUI *Fyne) LoadUsers(users []chat.User) {
	for _, u := range users {
		fyneUI.users.add(&user{id: u.ID, name: u.Name})
	}
}

func (fyneUI *Fyne) NetworkOnline() {
	fyneUI.mainWindow.SetContent(fyneUI.mainContainer)
	fyneUI.mainContainer.Show()
}

func (fyneUI *Fyne) NetworkDisconnected() {
	fyneUI.showNetworkLoading()
}

func (fyneUI *Fyne) LoadThread(bounceThread chat.Thread) {
	id := bounceThread.ID
	name := bounceThread.Name
	userIDs := bounceThread.UserIDs

	if _, exists := fyneUI.threads[id]; exists {
		log.WithFields(log.Fields{
			"id":   id,
			"name": name,
		}).Warn("attempt to create a thread that already exists, ignored")
		return
	}

	thread := &thread{
		id:                           id,
		name:                         binding.NewString(),
		users:                        newUserStore(),
		pendingUsers:                 newUserStore(),
		scroll:                       container.NewVScroll(container.NewVBox()),
		availableNewUsersScroll:      container.NewVScroll(container.NewVBox()),
		currentUsersContainer:        container.NewMax(),
		currentUsersDMLinksContainer: container.NewMax(),
		notificationsEnabled:         binding.NewBool(),
		lastMessage:                  time.Now().Unix(),
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

	//fyneUI.buildEditThreadWindow(thread)
	fyneUI.buildEditThreadContainer(thread)
	fyneUI.buildAddUsersThreadWindow(thread)
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
	fyneUI.threads[id] = thread
	fyneUI.refreshThreadOrder()
}

func (fyneUI *Fyne) ReceivedMessage(bounceMessage chat.IncomingMessage) {
	threadID := bounceMessage.ThreadID
	userID := bounceMessage.UserID
	message := bounceMessage.Text

	// Log an error and early return if the thread doesn't exist
	thread, exists := fyneUI.threads[threadID]
	if !exists {
		log.WithFields(log.Fields{
			"thread_id": threadID,
		}).Error("thread does not exist on message receive, ignoring the message")
		return
	}

	chatHistory := thread.scroll.Content.(*fyne.Container)

	// Create the new message box
	user, exists := thread.users.get(userID)
	if !exists {
		log.WithFields(log.Fields{
			"user_id": userID,
		}).Error("thread received a message from user ID not in thread")
		return
	}
	usernameText := widget.NewLabel(user.name)
	usernameText.TextStyle = fyne.TextStyle{Bold: true}
	usernameText.Wrapping = fyne.TextWrapWord
	messageText := widget.NewLabel(message)
	messageText.Wrapping = fyne.TextWrapWord
	messageBox := container.NewVBox(
		usernameText,
		messageText,
		widget.NewSeparator(),
	)

	// Check if we're already scrolled to the bottom, for auto-scroll reasons
	autoscroll := false
	location := thread.scroll.Offset.Y
	height := thread.scroll.Content.Size().Height - thread.scroll.Size().Height
	if height == location {
		autoscroll = true
	}

	// Add the message to the thread
	chatHistory.Objects = append(chatHistory.Objects, messageBox)
	chatHistory.Refresh()
	thread.scroll.Refresh()

	if fyneUI.isActive(thread) {
		if autoscroll {
			thread.scroll.ScrollToBottom()
			thread.scroll.Refresh()
		}
	} else {
		// This thread isn't active, mark the button as unread
		thread.button.Importance = widget.HighImportance
		thread.button.Refresh()
	}

	thread.lastMessage = time.Now().Unix()
	fyneUI.refreshThreadOrder()

	notificationsEnabled, err := thread.notificationsEnabled.Get()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("data bindings are broken")
	}

	if notificationsEnabled && time.Now().Unix() > thread.notificationsMutedUntil && !autoscroll { //TODO: also notify if not focused?
		threadName, err := thread.name.Get()
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("data bindings are broken")
		}
		fyneUI.app.SendNotification(fyne.NewNotification(threadName, "New message from "+user.name))
	}
}
