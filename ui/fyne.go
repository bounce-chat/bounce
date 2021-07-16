package ui

import (
	"sort"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	log "github.com/sirupsen/logrus"
)

type Fyne struct {
	app               fyne.App
	mainWindow        fyne.Window
	profileWindow     fyne.Window
	addContactWindow  fyne.Window
	createGroupWindow fyne.Window
	threadVBox        *fyne.Container
	chatContainer     *fyne.Container
	threads           map[string]*thread
	activeThread      string
}

type thread struct {
	id          string
	name        string
	header      *fyne.Container
	button      *widget.Button
	scroll      *container.Scroll
	entry       *widget.Entry
	entryBar    *fyne.Container
	lastMessage int64
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

func (fyneUI *Fyne) simulate() {
	time.Sleep(1 * time.Second)
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
}

//
// Lifecycle
//

func (fyneUI *Fyne) Build(configDirectory string) {
	fyneUI.threads = make(map[string]*thread)

	fyneUI.app = app.New()
	fyneUI.buildMainWindow()
	fyneUI.buildProfileWindow()
	fyneUI.buildAddContactWindow()
	fyneUI.buildCreateGroupWindow()

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
	logoResource, _ := fyne.LoadResourceFromPath("ui/assets/logo.png") // TODO: embed
	logo := canvas.NewImageFromResource(logoResource)
	logo.FillMode = canvas.ImageFillContain
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

	mainWindow.SetContent(container.New(
		layout.NewBorderLayout(nil, nil, threads, nil),
		threads,
		fyneUI.chatContainer,
	))
	mainWindow.Show()

	fyneUI.mainWindow = mainWindow
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

func (fyneUI *Fyne) textEntry(thread *thread) *fyne.Container {
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
	fyneUI.chatContainer.Objects = []fyne.CanvasObject{
		container.New(
			layout.NewBorderLayout(thread.header, thread.entryBar, nil, nil),
			thread.header,
			thread.entryBar,
			thread.scroll,
		),
	}
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
	usernameText.Alignment = fyne.TextAlignTrailing
	usernameText.Wrapping = fyne.TextWrapWord
	messageText := widget.NewLabel(message)
	//messageText.Alignment = fyne.TextAlignTrailing
	messageText.Wrapping = fyne.TextWrapWord

	// TODO: left aligning a Label with a boarder layout inside of a VBox doesn't work when
	// text alignment is set to trailing.  Open a ticket about this.
	//alignedMessageText := container.New(
	//	layout.NewBorderLayout(nil, nil, nil, messageText),
	//	messageText,
	//)
	messageBox := container.NewVBox(
		usernameText,
		messageText, //alignedMessageText,
		widget.NewSeparator(),
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

func (fyneUI *Fyne) AddThread(id, name string) {
	if _, exists := fyneUI.threads[id]; exists {
		log.WithFields(log.Fields{
			"id":   id,
			"name": name,
		}).Warn("attempt to create a thread that already exists, ignored")
		return
	}

	addUser := widget.NewButton("Edit", func() {})

	threadLabel := widget.NewLabel(name)
	threadLabel.TextStyle = fyne.TextStyle{Bold: true}
	threadButtons := container.NewHBox(addUser)
	header := container.New(
		layout.NewBorderLayout(nil, nil, threadLabel, threadButtons),
		threadLabel,
		threadButtons,
	)
	thread := &thread{
		id:          id,
		name:        name,
		header:      header,
		scroll:      container.NewVScroll(container.NewVBox()),
		lastMessage: time.Now().Unix(),
	}

	thread.entryBar = fyneUI.textEntry(thread)
	thread.button = widget.NewButton(name, func() {
		fyneUI.displayThread(thread)
	})
	thread.button.Importance = widget.LowImportance
	thread.button.Alignment = widget.ButtonAlignLeading

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

	// TODO: OS notifications
}
