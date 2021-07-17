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
	activeThread      string
}

type thread struct {
	id                      string
	name                    binding.String
	isDM                    bool // TODO: prevents things like showing an option to set an image
	notificationsEnabled    bool
	notificationsMutedUntil int64
	editWindow              fyne.Window
	view                    *fyne.Container
	header                  *fyne.Container
	button                  *widget.Button
	scroll                  *container.Scroll
	entry                   *widget.Entry
	entryBar                *fyne.Container
	lastMessage             int64
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

func NewEmbeddedResource(path string) *embededResource {
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
	fyneUI.AddThread("1", "Group 1")
	time.Sleep(2 * time.Second)
	fyneUI.AddThread("2", "Group 2")
	time.Sleep(3 * time.Second)
	fyneUI.AddThread("3", "DM 1")
	go func() {
		for i := 0; i < 25; i++ {
			fyneUI.ReceivedMessage("1", "Person 1", "hello this is from person 1")
			time.Sleep(1 * time.Second)
		}
	}()
	go func() {
		for i := 0; i < 10; i++ {
			fyneUI.ReceivedMessage("2", "Person 2", "hello this is from person 2")
			time.Sleep(5 * time.Second)
		}
	}()

	fyneUI.ReceivedMessage("3", "Person 3", "hello this is from person 3")

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

	fyneUI.app = app.New()
	fyneUI.app.SetIcon(NewEmbeddedResource("assets/icon.png"))
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

	logo := canvas.NewImageFromResource(NewEmbeddedResource("assets/logo.png"))
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
	logo := canvas.NewImageFromResource(NewEmbeddedResource("assets/logo_with_network.png"))
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

func (fyneUI *Fyne) buildEditThreadWindow(thread *thread) {
	thread.editWindow = fyneUI.app.NewWindow("Edit Thread")

	threadNameEntry := widget.NewEntry()
	currentThreadName, err := thread.name.Get()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("data bindings are broken")
	}
	threadNameEntry.Text = currentThreadName

	threadIcon := canvas.NewImageFromResource(NewEmbeddedResource("assets/not_found.png"))
	threadIcon.FillMode = canvas.ImageFillContain
	threadIcon.SetMinSize(fyne.NewSize(64, 64))

	notificationsCheck := widget.NewCheck("Enable notifications", func(bool) {})
	notificationsCheck.Checked = thread.notificationsEnabled

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

		thread.notificationsEnabled = notificationsCheck.Checked
		thread.editWindow.Hide()
	})
	saveButton.Importance = widget.HighImportance
	cancelButton := widget.NewButton("Cancel", func() {
		// TODO: reset everything to original values
		thread.editWindow.Hide()
	})
	actionButtons := container.New(
		layout.NewBorderLayout(nil, nil, cancelButton, saveButton),
		saveButton,
		cancelButton,
	)

	thread.editWindow.SetContent(
		container.NewPadded(
			container.New(
				layout.NewBorderLayout(nil, actionButtons, nil, nil),
				container.New(
					layout.NewCenterLayout(),
					container.NewVBox(
						threadIcon,
						threadNameEntry,
						notificationsCheck,
						widget.NewButton("Add Users", func() {}),
					),
				),
				actionButtons,
			),
		),
	)
	thread.editWindow.SetCloseIntercept(func() {
		thread.editWindow.Hide()
	})
}

func (fyneUI *Fyne) buildThreadEntry(thread *thread) *fyne.Container {
	entry := widget.NewMultiLineEntry()
	entry.Wrapping = fyne.TextWrapWord
	entry.OnSubmitted = func(message string) {
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

func (fyneUI *Fyne) NetworkLoaded() {
	fyneUI.mainWindow.SetContent(fyneUI.mainContainer)
}

// TODO: figure out why SetContent doesn't work again here
//func (fyneUI *Fyne) NetworkDisconnected() {
//	fyneUI.mainWindow.SetContent(fyneUI.networkLoading)
//}

func (fyneUI *Fyne) AddThread(id, name string) {
	if _, exists := fyneUI.threads[id]; exists {
		log.WithFields(log.Fields{
			"id":   id,
			"name": name,
		}).Warn("attempt to create a thread that already exists, ignored")
		return
	}

	thread := &thread{
		id:                   id,
		name:                 binding.NewString(),
		scroll:               container.NewVScroll(container.NewVBox()),
		notificationsEnabled: false,
		lastMessage:          time.Now().Unix(),
	}
	err := thread.name.Set(name)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("data bindings are broken")
	}

	fyneUI.buildEditThreadWindow(thread)
	editButton := widget.NewButton("Edit", func() {
		thread.editWindow.Show()
	})
	threadLabel := widget.NewLabel(name)
	threadLabel.Bind(thread.name)
	threadLabel.TextStyle = fyne.TextStyle{Bold: true}
	threadButtons := container.NewMax(editButton)
	thread.header = container.New(
		layout.NewBorderLayout(nil, nil, threadLabel, threadButtons),
		threadLabel,
		threadButtons,
	)

	thread.entryBar = fyneUI.buildThreadEntry(thread)
	thread.button = widget.NewButton(name, func() {
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

func (fyneUI *Fyne) ReceivedMessage(threadID, username, message string) { // TODO: this should probably take the protobuf definition
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
	usernameText := widget.NewLabel(username)
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

	if thread.notificationsEnabled && time.Now().Unix() > thread.notificationsMutedUntil && !autoscroll {
		threadName, err := thread.name.Get()
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("data bindings are broken")
		}
		fyneUI.app.SendNotification(fyne.NewNotification(threadName, "New message from "+username))
	}
}

// TODO: figure out callbacks for things like
//fyneUI.SetOnThreadRename(func(string, string))
// The UI interface must let the chat app pass a function to handle database updates and stuff
// when things stored in the DB are changed
// However, what is a chat engine setting and what is a UI setting?  Notifications UI, thread name chat?
