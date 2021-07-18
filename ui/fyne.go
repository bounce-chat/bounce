package ui

import (
	"embed"
	"image/color"
	"sort"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	log "github.com/sirupsen/logrus"
)

//go:embed assets
var assets embed.FS

type Fyne struct {
	app               fyne.App
	mainWindow        fyne.Window
	profileWindow     fyne.Window
	addContactWindow  fyne.Window
	createGroupWindow fyne.Window
	mainContainer     *fyne.Container
	networkLoading    *fyne.Container
	threadVBox        *fyne.Container
	chatContainer     *fyne.Container
	threads           map[string]*thread
	users             map[string]*user
	activeThread      string
}

type thread struct {
	id                      string
	name                    binding.String
	users                   map[string]*user
	pendingUsers            map[string]*user
	isDM                    bool         // TODO: prevents things like showing an option to set an image
	notificationsEnabled    binding.Bool // TODO: this makes it take effect before save is hit, do I want that?
	notificationsMutedUntil int64        // TODO: fyne feature request/PR: support binding int64 for time.Time
	editWindow              fyne.Window
	addUsersWindow          fyne.Window
	view                    *fyne.Container
	header                  *fyne.Container
	button                  *widget.Button
	scroll                  *container.Scroll
	availableNewUsersScroll *container.Scroll
	currentUsersContainer   *fyne.Container
	entry                   *widget.Entry
	entryBar                *fyne.Container
	lastMessage             int64
}

type user struct {
	id   string
	name string
}

// For sorting by last message received
type sortableThreads []*thread

func (threads sortableThreads) Len() int {
	return len(threads)
}
func (threads sortableThreads) Swap(i, j int) {
	threads[i], threads[j] = threads[j], threads[i]
}
func (threads sortableThreads) Less(i, j int) bool {
	// Reverse order, highest timestamp on top
	return threads[i].lastMessage > threads[j].lastMessage
}

// Implement Fyne's fyne.Resource, loading from embedded files
type embededResource struct {
	path  string
	bytes []byte
}

func newEmbeddedResource(path string) *embededResource {
	bytes, err := assets.ReadFile(path)
	if err != nil {
		log.WithFields(log.Fields{
			"path": path,
		}).Fatal("unable to locate embedded resource")
	}
	return &embededResource{
		path:  path,
		bytes: bytes,
	}
}

func (resource *embededResource) Name() string {
	return resource.path
}

func (resource *embededResource) Content() []byte {
	return resource.bytes
}

func (fyneUI *Fyne) simulate() {
	//time.Sleep(3 * time.Second)
	fyneUI.NetworkLoaded()
	//time.Sleep(1 * time.Second)
	user1 := &user{
		id:   "1",
		name: "User 1",
	}
	user2 := &user{
		id:   "2",
		name: "User 2",
	}
	user3 := &user{
		id:   "3",
		name: "User 3",
	}
	user4 := &user{
		id:   "4",
		name: "User 4",
	}
	fyneUI.LoadUsers([]*user{user1, user2, user3, user4})
	fyneUI.AddThread("1", "Group with 1 and 2", []string{"1", "2"})
	//time.Sleep(2 * time.Second)
	fyneUI.AddThread("2", "Group with 2 and 3", []string{"2", "3"})
	//time.Sleep(3 * time.Second)
	fyneUI.AddThread("3", "DM and 4", []string{"4"})
	go func() {
		for i := 0; i < 25; i++ {
			fyneUI.ReceivedMessage("1", "1", "hello this is from user 1")
			time.Sleep(1 * time.Second)
		}
	}()
	go func() {
		for i := 0; i < 10; i++ {
			fyneUI.ReceivedMessage("2", "2", "hello this is from user 2")
			time.Sleep(5 * time.Second)
		}
	}()

	fyneUI.ReceivedMessage("3", "4", "hello this is from user 4")

	/*
		time.Sleep(5 * time.Second)
		fyneUI.NetworkDisconnected()
		time.Sleep(5 * time.Second)
		fyneUI.NetworkLoaded()
	*/
}

//
// Lifecycle
//

func (fyneUI *Fyne) Build(configDirectory string) {
	fyneUI.threads = make(map[string]*thread)
	fyneUI.users = make(map[string]*user)

	fyneUI.app = app.New()
	fyneUI.app.SetIcon(newEmbeddedResource("assets/icon.png"))
	fyneUI.buildNetworkLoading()
	fyneUI.buildProfileWindow()
	fyneUI.buildAddContactWindow()
	fyneUI.buildCreateGroupWindow()
	fyneUI.buildMainWindow()

}

func (fyneUI *Fyne) Run() {
	go fyneUI.simulate()
	fyneUI.app.Run()
}

func (fyneUI *Fyne) Quit() {
	fyneUI.app.Quit()
}

//
// Build user interface objects
//

func (fyneUI *Fyne) buildMainWindow() {
	mainWindow := fyneUI.app.NewWindow("Bounce")
	mainWindow.SetMaster()
	mainWindow.SetMainMenu(fyneUI.buildMainMenu())

	mainWindow.SetCloseIntercept(func() {
		log.WithFields(log.Fields{
			"at": "desktop.DesktopUI.Build",
		}).Info("window close button hit, shutting down")
		// There's some bug in Fyne where the app will hang when the close button
		// is hit unless this is explicitly set
		fyneUI.Quit()
	})

	//
	// Logo and welcome message / instructions to be shown before a thread is selected
	//

	logo := canvas.NewImageFromResource(newEmbeddedResource("assets/logo.png"))
	logo.FillMode = canvas.ImageFillContain
	// TODO: choose reasonable values here
	// https://github.com/fyne-io/fyne/blob/v2.0.3/cmd/fyne_demo/tutorials/welcome.go#L25
	logo.SetMinSize(fyne.NewSize(228, 167))

	fyneUI.chatContainer = container.NewMax(
		container.New(
			layout.NewCenterLayout(),
			container.NewVBox(
				logo,
				widget.NewLabel("Select a thread on the left to get started"),
			),
		),
	)

	fyneUI.threadVBox = container.NewVBox()
	threads := container.NewHBox(container.NewVScroll(fyneUI.threadVBox), widget.NewSeparator())

	fyneUI.mainContainer = container.New(
		layout.NewBorderLayout(nil, nil, threads, nil),
		threads,
		fyneUI.chatContainer,
	)

	mainWindow.SetContent(fyneUI.networkLoading)
	mainWindow.Show()

	fyneUI.mainWindow = mainWindow
}

func (fyneUI *Fyne) buildNetworkLoading() {
	logo := canvas.NewImageFromResource(newEmbeddedResource("assets/logo_with_network.png"))
	logo.FillMode = canvas.ImageFillContain
	// TODO: choose reasonable values here
	// https://github.com/fyne-io/fyne/blob/v2.0.3/cmd/fyne_demo/tutorials/welcome.go#L25
	logo.SetMinSize(fyne.NewSize(228, 167))

	fyneUI.networkLoading = container.NewMax(
		container.New(
			layout.NewCenterLayout(),
			container.NewVBox(
				logo,
				widget.NewLabel("Connecting to the Tor network..."),
				widget.NewProgressBarInfinite(),
			),
		),
	)
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
			fyne.NewMenuItem("Add Contact", func() {
				fyneUI.addContactWindow.Show()
			}),
			fyne.NewMenuItem("Create Group", func() {
				fyneUI.createGroupWindow.Show()
			}),
		),
	)
}

func (fyneUI *Fyne) refreshUserSelections(thread *thread) {
	fyneUI.refreshCurrentAndPendingUsers(thread)
	fyneUI.refreshAvailableNewUsers(thread)

}

func (fyneUI *Fyne) refreshCurrentAndPendingUsers(thread *thread) {
	currentUsersList := container.NewVBox()

	for _, thisUser := range thread.users {
		userIconCanvas := canvas.NewImageFromResource(newEmbeddedResource("assets/not_found.png"))
		userIconCanvas.FillMode = canvas.ImageFillContain
		userIconCanvas.SetMinSize(fyne.NewSize(24, 24)) // TODO: figure out how to get the ideal consistent size

		currentUsersList.Objects = append(
			currentUsersList.Objects,
			container.NewHBox(
				userIconCanvas,
				widget.NewLabel(thisUser.name),
			),
		)
	}

	for _, thisUser := range thread.pendingUsers {
		func(u *user) {
			removePendingUserButton := widget.NewButtonWithIcon(u.name, newEmbeddedResource("assets/not_found.png"), func() {
				delete(thread.pendingUsers, u.id)
				fyneUI.refreshUserSelections(thread)
			})
			removePendingUserButton.Alignment = widget.ButtonAlignLeading
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
	for _, thisUser := range fyneUI.users {
		// Exclude users already in the thread
		if _, exists := thread.users[thisUser.id]; exists {
			continue
		}
		// Exclude users that are pending addition to the group
		if _, exists := thread.pendingUsers[thisUser.id]; exists {
			continue
		}
		// This weirdness is so that the iteration over user works with the dynamically created buttons
		// TODO: make sure I'm not making this mistake anywhere else
		func(u *user) {
			addUserButton := widget.NewButtonWithIcon(u.name, newEmbeddedResource("assets/not_found.png"), func() { // TODO: use the user's icon
				thread.pendingUsers[u.id] = u
				fyneUI.refreshUserSelections(thread)
			})
			addUserButton.Alignment = widget.ButtonAlignLeading
			allUsersListBox.Objects = append(
				allUsersListBox.Objects,
				addUserButton,
			)
		}(thisUser)
	}
	thread.availableNewUsersScroll.Content = allUsersListBox
	thread.availableNewUsersScroll.Refresh()
}

func (fyneUI *Fyne) buildAddUsersThreadWindow(thread *thread) {
	thread.addUsersWindow = fyneUI.app.NewWindow("Add Users")
	thread.addUsersWindow.SetCloseIntercept(func() {
		thread.addUsersWindow.Hide()
	})

	//
	// List all users who are already in this thread
	//
	currentUsersLabel := widget.NewLabel("Current Users:")
	currentUsersListView := container.New(
		layout.NewBorderLayout(currentUsersLabel, nil, nil, nil),
		currentUsersLabel,
		thread.currentUsersContainer, // TODO: eventually this will get so long it breaks the UI.  Need a better looking scroll wrapper.
	)

	//
	// List all users who are not in this thread
	//
	fyneUI.refreshAvailableNewUsers(thread)
	addUsersLabel := widget.NewLabel("Add Users:")
	allUsersList := container.New(
		layout.NewBorderLayout(addUsersLabel, nil, nil, nil),
		addUsersLabel,
		thread.availableNewUsersScroll,
	)

	//
	// Buttons to confirm or cancel the new user additions
	//
	saveAddUsersButton := widget.NewButton("Save", func() {
		for userID, user := range thread.pendingUsers {
			thread.users[userID] = user
		}
		thread.pendingUsers = make(map[string]*user)
		// TODO: tell the chat engine to add them
		fyneUI.refreshUserSelections(thread)
		thread.addUsersWindow.Hide()
	})
	saveAddUsersButton.Importance = widget.HighImportance
	cancelAddUsersButton := widget.NewButton("Cancel", func() {
		// Reset everything
		thread.pendingUsers = make(map[string]*user)
		fyneUI.refreshUserSelections(thread)
		thread.addUsersWindow.Hide()
	})
	addUsersActionButtons := container.New(
		layout.NewBorderLayout(nil, nil, cancelAddUsersButton, saveAddUsersButton),
		saveAddUsersButton,
		cancelAddUsersButton,
	)

	addUsersContent := container.New(
		layout.NewBorderLayout(currentUsersListView, addUsersActionButtons, nil, nil),
		currentUsersListView,
		allUsersList,
		addUsersActionButtons,
	)
	thread.addUsersWindow.SetContent(addUsersContent)

}

func (fyneUI *Fyne) buildEditThreadWindow(thread *thread) {
	thread.editWindow = fyneUI.app.NewWindow("Edit Thread")
	thread.editWindow.SetCloseIntercept(func() {
		thread.editWindow.Hide()
	})

	threadNameEntry := widget.NewEntry()
	currentThreadName, err := thread.name.Get()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("data bindings are broken")
	}
	threadNameEntry.Text = currentThreadName

	threadIcon := canvas.NewImageFromResource(newEmbeddedResource("assets/not_found.png"))
	threadIcon.FillMode = canvas.ImageFillContain
	threadIcon.SetMinSize(fyne.NewSize(64, 64))

	notificationsCheck := widget.NewCheckWithData("Enable notifications", thread.notificationsEnabled)

	saveButton := widget.NewButton("Save", func() {
		newThreadName := threadNameEntry.Text
		err := thread.name.Set(newThreadName)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("data bindings are broken")
		}
		thread.button.Text = newThreadName
		thread.button.Refresh()

		// TODO: persist settings in Fyne
		thread.editWindow.Hide()
	})
	saveButton.Importance = widget.HighImportance
	cancelButton := widget.NewButton("Cancel", func() {
		threadNameEntry.Text = currentThreadName
		threadNameEntry.Refresh()
		thread.editWindow.Hide()
	})
	actionButtons := container.New(
		layout.NewBorderLayout(nil, nil, cancelButton, saveButton),
		saveButton,
		cancelButton,
	)

	usersLabel := widget.NewLabel("Users:")
	addUserButton := widget.NewButton("  +  ", func() {
		fyneUI.refreshUserSelections(thread)
		thread.addUsersWindow.Show()
	})
	addUserButton.Importance = widget.LowImportance
	usersTitle := container.New(
		layout.NewBorderLayout(nil, nil, usersLabel, addUserButton),
		usersLabel,
		addUserButton,
	)

	userListBox := container.NewVBox()
	for _, user := range thread.users {
		userListBox.Objects = append(
			userListBox.Objects,
			widget.NewLabel(user.name), // TODO when this is a link the iteration thing will break it
		)
	}

	userList := container.NewPadded(
		container.NewVScroll(userListBox),
	)

	topOptionsVBox := container.NewVBox(
		threadIcon,
		threadNameEntry,
		notificationsCheck,
		usersTitle,
	)

	thread.editWindow.SetContent(
		container.NewPadded(
			container.New(
				layout.NewBorderLayout(nil, actionButtons, nil, nil),
				container.New(
					layout.NewPaddedLayout(),
					container.New(
						layout.NewBorderLayout(topOptionsVBox, nil, nil, nil),
						topOptionsVBox,
						userList,
						//container.NewVScroll(thread.currentUsersContainer), // TODO: no, use a new view just for this
					),
				),
				actionButtons,
			),
		),
	)
}

func (fyneUI *Fyne) buildThreadEntry(thread *thread) *fyne.Container {
	entry := widget.NewMultiLineEntry()
	// TODO: custom submit functionality here
	entry.Wrapping = fyne.TextWrapWord
	entry.OnSubmitted = func(message string) {
		// TODO: hook into the chat engine
		// immediately delete, put back if it fails to send?
		//fyneUI.onMessageSent(thread, message)
		fyneUI.displaySentMessage(thread, message)
		entry.Text = ""
		entry.Refresh()
	}

	button := widget.NewButton("Send", func() {
		fyneUI.displaySentMessage(thread, entry.Text)
		entry.Text = ""
		entry.Refresh()
	})

	thread.entry = entry

	return container.New(
		layout.NewBorderLayout(nil, nil, nil, button),
		entry,
		button,
	)
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

func (fyneUI *Fyne) LoadUsers(users []*user) { // TODO: this needs to take a type defined in the chat engine
	for _, user := range users {
		fyneUI.users[user.id] = user
	}
}

func (fyneUI *Fyne) NetworkLoaded() {
	fyneUI.mainWindow.SetContent(fyneUI.mainContainer)
}

// TODO: figure out why SetContent doesn't work again here
//func (fyneUI *Fyne) NetworkDisconnected() {
//	fyneUI.mainWindow.SetContent(fyneUI.networkLoading)
//}

func (fyneUI *Fyne) AddThread(id, name string, userIDs []string) {
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
		users:                   make(map[string]*user),
		pendingUsers:            make(map[string]*user),
		scroll:                  container.NewVScroll(container.NewVBox()),
		availableNewUsersScroll: container.NewVScroll(container.NewVBox()),
		currentUsersContainer:   container.NewMax(),
		notificationsEnabled:    binding.NewBool(),
		lastMessage:             time.Now().Unix(),
	}
	for _, userID := range userIDs {
		user, exists := fyneUI.users[userID]
		if !exists {
			log.WithFields(log.Fields{
				"user_id": userID,
			}).Error("attempted to create thread with user unknown to UI")
			return
		} else {
			thread.users[userID] = user
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

	fyneUI.buildEditThreadWindow(thread)
	fyneUI.buildAddUsersThreadWindow(thread)
	editButton := widget.NewButton("Edit", func() {
		thread.editWindow.Show()
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

func (fyneUI *Fyne) ReceivedMessage(threadID, userID, message string) { // TODO: this should probably take the protobuf definition
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
	user, exists := thread.users[userID]
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

// TODO: figure out callbacks for things like
//fyneUI.SetOnThreadRename(func(string, string))
// The UI interface must let the chat app pass a function to handle database updates and stuff
// when things stored in the DB are changed
// However, what is a chat engine setting and what is a UI setting?  Notifications UI, thread name chat?
