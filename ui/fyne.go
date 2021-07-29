package ui

import (
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

//
// An implementation of the Bounce chat.UI interface using Fyne
//
type Fyne struct {
	app                          fyne.App
	mainWindow                   fyne.Window
	mainContainer                *fyne.Container
	newInstall                   *fyne.Container
	newProfileCreator            *fyne.Container
	networkLoading               *fyne.Container
	editProfile                  *fyne.Container
	settings                     *fyne.Container
	about                        *fyne.Container
	newGroup                     *fyne.Container
	newDM                        *fyne.Container
	introduceContacts            *fyne.Container
	importContact                *fyne.Container
	threadVBox                   *fyne.Container
	chatContainer                *fyne.Container
	mainMenu                     *fyne.MainMenu
	threads                      map[string]*thread
	activeThread                 string
	users                        *userStore
	profileSet                   bool
	onSendMessage                chat.SendMessageCallback
	onAddUserToGroup             chat.AddUserToGroupCallback
	onRenameGroup                chat.RenameGroupCallback
	onChangeNotificationSettings chat.ChangeNotificationSettingsCallback
	onSetProfile                 chat.SetProfileCallback
}

func (fyneUI *Fyne) Build(configDirectory string) {
	//
	// Initialize types that require it
	//
	fyneUI.threads = make(map[string]*thread)
	fyneUI.users = newUserStore()

	//
	// Define the app
	//
	fyneUI.app = app.NewWithID("chat.bounce")
	fyneUI.app.SetIcon(newEmbeddedResource("assets/icon.png"))

	//
	// Define the main window
	//
	fyneUI.mainWindow = fyneUI.app.NewWindow("Bounce")
	fyneUI.mainWindow.SetMaster()
	fyneUI.mainWindow.SetCloseIntercept(func() {
		// There's some bug in Fyne where the app will hang when the close button
		// is hit unless this is explicitly set https://github.com/fyne-io/fyne/issues/2314
		fyneUI.Quit()
	})
	fyneUI.mainWindow.Resize(fyne.Size{Height: 600, Width: 800})
	fyneUI.mainWindow.Show()

	//
	// Build all the containers
	//
	fyneUI.buildMenu()
	fyneUI.buildNewInstall()
	fyneUI.buildNewProfileCreator()
	fyneUI.buildNetworkLoading()
	fyneUI.buildEditProfile()
	fyneUI.buildSettings()
	fyneUI.buildAbout()
	fyneUI.buildNewGroup()
	fyneUI.buildNewDM()
	fyneUI.buildIntroduceContacts()
	fyneUI.buildImportContact()
	fyneUI.buildMainContainer()

	//
	// Default to displaying the "network loading" container
	//
	fyneUI.showNetworkLoading()
}

func (fyneUI *Fyne) RegisterCallbacks(callbacks chat.UICallbacks) {
	fyneUI.onSendMessage = callbacks.SendMessage
	fyneUI.onAddUserToGroup = callbacks.AddUserToGroup
	fyneUI.onRenameGroup = callbacks.RenameGroup
	fyneUI.onChangeNotificationSettings = callbacks.ChangeNotificationSettings
	fyneUI.onSetProfile = callbacks.SetProfile
}

func (fyneUI *Fyne) Run() {
	fyneUI.app.Run()
}

func (fyneUI *Fyne) Quit() {
	fyneUI.app.Quit()
}

//
// TODO: move these:
//

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
		log.WithFields(log.Fields{
			"thread_id":   thread.id,
			"thread_name": thread.name,
		}).Warn("UI asked to display an empty sent message")
		return
	}
	chatHistory := thread.scroll.Content.(*fyne.Container)

	chatHistory.Objects = append(chatHistory.Objects, newChatBubble("You", message, true, time.Now().Unix(), nil))
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

func (fyneUI *Fyne) LoadInitialState(state chat.InitialState) {
	// TODO: if the profile is nil, then the "main view" is a profile
	// builder / device sync UI, until a profile is set by the user or
	// over the wire
	if !state.ProfileSet {
		// This is a brand new installation.  Tell the user interface it must have the
		// user create a profile or sync this device to an existing device group before
		// the app can be used.
		fyneUI.profileSet = false
		// TODO: log fatal if anything else is set
		return
	} else {
		fyneUI.profileSet = true
	}

	for _, u := range state.Users {
		fyneUI.users.add(&user{id: u.ID, name: u.Name})
	}
	for _, t := range state.Threads {
		fyneUI.LoadThread(t)
	}
	for _, m := range state.Messages {
		fyneUI.ReceivedMessage(m) // TODO: except we don't want to mark them as unread (unless they are, I guess)
	}
}

func (fyneUI *Fyne) NetworkOnline() {
	fyneUI.showMainContainer()
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
	fyneUI.threads[id] = thread
	fyneUI.refreshThreadOrder()
}

func (fyneUI *Fyne) ReceivedMessage(bounceMessage chat.Message) {
	threadID := bounceMessage.Destination
	userID := bounceMessage.Source
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
	//profileButton := widget.NewButtonWithIcon("", newEmbeddedResource("assets/not_found.png"), func() { // TODO: get image from user
	//	log.Info("user wants to open the profile of " + user.name)
	//	// TODO: display this user's profile
	//})
	messageBox := newChatBubble(user.name, message, false, time.Now().Unix(), nil) //profileButton)

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
