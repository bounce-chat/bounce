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
	databaseLoading              *fyne.Container
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
	groups                       map[string]*group
	dms                          map[string]*directMessage
	activeThread                 string
	users                        *userStore
	profileSet                   bool
	initialStateSet              bool
	networkOnline                bool
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
	fyneUI.groups = make(map[string]*group)
	fyneUI.dms = make(map[string]*directMessage)
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
	fyneUI.buildDatabaseLoading()
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
	fyneUI.showMainContainer()
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
	for _, group := range fyneUI.groups {
		allThreads = append(allThreads, group)
	}
	for _, dm := range fyneUI.dms {
		allThreads = append(allThreads, dm)
	}
	sort.Sort(allThreads)

	buttons := []fyne.CanvasObject{}
	for _, thread := range allThreads {
		buttons = append(buttons, thread.getButton())
	}
	fyneUI.threadVBox.Objects = buttons
	fyneUI.threadVBox.Refresh()
}

func (fyneUI *Fyne) displaySentMessage(thread thread, message string) {
	if message == "" {
		// Shouldn't be possible, entry widget doesn't allow it.
		// Report if that is broken.
		//log.WithFields(log.Fields{
		//	"thread_id":   thread.id,
		//	"thread_name": thread.name,
		//}).Warn("UI asked to display an empty sent message")
		return
	}
	chatHistory := thread.chatHistoryScroll().Content.(*fyne.Container)

	chatHistory.Objects = append(chatHistory.Objects, newChatBubble("You", message, true, time.Now().Unix(), nil))
	chatHistory.Refresh()

	thread.chatHistoryScroll().Refresh()
	thread.chatHistoryScroll().ScrollToBottom()
	thread.chatHistoryScroll().Refresh()

	thread.setLastMessage(time.Now().Unix())
	fyneUI.refreshThreadOrder()
}

//
// Exported functions for the chat engine
//

func (fyneUI *Fyne) LoadInitialState(state chat.InitialState) {
	if fyneUI.initialStateSet {
		log.Fatal("the initial state of the UI can only be loaded once")
	}
	fyneUI.initialStateSet = true
	// Refresh the state of the main view after the state is loaded
	defer fyneUI.showMainContainer()

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
	//for _, t := range state.Threads {
	//	fyneUI.NewThread(t)
	//}
	//for _, m := range state.Messages {
	//	fyneUI.ReceivedMessage(m) // TODO: except we don't want to mark them as unread (unless they are, I guess)
	//}
}

func (fyneUI *Fyne) NetworkOnline() {
	fyneUI.networkOnline = true
	fyneUI.showMainContainer()
}

func (fyneUI *Fyne) NetworkDisconnected() {
	fyneUI.networkOnline = false
	fyneUI.showMainContainer()
}

func (fyneUI *Fyne) NewUser(id, name string) {
	fyneUI.users.add(&user{id: id, name: name})
}

func (fyneUI *Fyne) NewGroupChat(bounceThread chat.Thread) {
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
		// Create the DM, assign to dm
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

	// Create the new message box
	user, exists := group.users.get(msg.Source)
	//user, exists := group.users.get(userID)
	if !exists {
		log.WithFields(log.Fields{
			"user_id": msg.Source,
		}).Error("group received a message from user ID not in thread")
		return
	}
	//profileButton := widget.NewButtonWithIcon("", newEmbeddedResource("assets/not_found.png"), func() { // TODO: get image from user
	//	log.Info("user wants to open the profile of " + user.name)
	//	// TODO: display this user's profile
	//})
	messageBox := newChatBubble(user.name, msg.Text, false, time.Now().Unix(), nil) //profileButton)

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
