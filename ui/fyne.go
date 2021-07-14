package ui

import (
	"sort"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	log "github.com/sirupsen/logrus"
)

type Fyne struct {
	app           fyne.App
	threadVBox    *fyne.Container
	entryBar      *container.Split
	chatContainer *fyne.Container
	chatHistory   *container.Scroll
	threads       map[string]*thread
	activeThread  string
}

type thread struct {
	id          string
	button      *widget.Button
	scroll      *container.Scroll
	lastMessage int64
}

// For sorting by last message received
type threads []*thread

func (t threads) Len() int {
	return len(t)
}
func (t threads) Swap(i, j int) {
	t[i], t[j] = t[j], t[i]
}
func (t threads) Less(i, j int) bool {
	// Reverse order, highest timestamp on top
	return t[i].lastMessage > t[j].lastMessage
}

func (fyneUI *Fyne) simulate() {
	time.Sleep(1 * time.Second)
	fyneUI.AddThread("person1")
	time.Sleep(2 * time.Second)
	fyneUI.AddThread("person2")
	time.Sleep(3 * time.Second)
	fyneUI.AddThread("person3")
	go func() {
		for i := 0; i < 25; i++ {
			fyneUI.ReceivedMessage("person1", "Person 1", "hello this is from person 1")
			time.Sleep(1 * time.Second)
		}
	}()
	go func() {
		for i := 0; i < 10; i++ {
			fyneUI.ReceivedMessage("person2", "Person 2", "hello this is from person 2")
			time.Sleep(5 * time.Second)
		}
	}()

	fyneUI.ReceivedMessage("person3", "Person 3", "hello this is from person 3")
}

//
// Lifecycle
//

func (fyneUI *Fyne) Build(configDirectory string) {
	fyneUI.threads = make(map[string]*thread)
	fyneApp := app.New()
	fyneUI.app = fyneApp
	mainWindow := fyneUI.app.NewWindow("Bounce")
	mainWindow.SetMaster()
	mainWindow.SetMainMenu(fyneUI.mainMenu())
	mainWindow.SetCloseIntercept(func() {
		log.WithFields(log.Fields{
			"at": "desktop.DesktopUI.Build",
		}).Info("window close button hit, shutting down")
		// There's some bug in Fyne where the app will hang when the close button
		// is hit unless this is explicitly set
		fyneUI.Quit()
	})

	fyneUI.threadVBox = container.NewVBox()
	threads := container.NewVScroll(fyneUI.threadVBox)
	fyneUI.chatContainer = container.NewMax(widget.NewLabel("Select a chat thread on the left"))
	chatBox := container.NewVSplit(
		fyneUI.chatContainer,
		fyneUI.textEntry(),
	)
	mainWindow.SetContent(container.NewHSplit(
		threads,
		chatBox,
	))
	mainWindow.Show()

}

func (fyneUI *Fyne) Run() {
	go fyneUI.simulate()
	fyneUI.app.Run()
}

func (fyneUI *Fyne) Quit() {
	fyneUI.app.Quit()
}

//
// User interface objects
//

func (fyneUI *Fyne) mainMenu() *fyne.MainMenu {
	return fyne.NewMainMenu(
		fyne.NewMenu(
			"Bounce",
			fyne.NewMenuItem("My Profile", func() {}),
			fyne.NewMenuItem("Add Contact", func() {}),
			fyne.NewMenuItem("Create Group", func() {}),
		),
	)
}

func (fyneUI *Fyne) textEntry() *container.Split {
	fyneUI.entryBar = container.NewHSplit( // TODO: something better for always smallest button
		widget.NewMultiLineEntry(),
		widget.NewButton("Send", func() {}),
	)
	return fyneUI.entryBar
}

//
// User interface manipulation
//

func (fyneUI *Fyne) displayHistory(thread *thread) {
	fyneUI.activeThread = thread.id
	fyneUI.chatContainer.Objects = []fyne.CanvasObject{thread.scroll}
	fyneUI.chatContainer.Refresh()
	thread.button.Importance = widget.LowImportance
	thread.button.Refresh()
}

func (fyneUI *Fyne) isActive(thread *thread) bool {
	return fyneUI.activeThread == thread.id
}

func (fyneUI *Fyne) refreshThreadOrder() {
	allThreads := threads{}
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

//
// Exported functions for the chat engine
//

func (fyneUI *Fyne) AddThread(id string) { // TODO: add group/user name add that as button
	if _, exists := fyneUI.threads[id]; exists {
		log.WithFields(log.Fields{
			"id": id,
		}).Warn("attempt to create a thread that already exists, ignored")
		return
	}

	thread := &thread{
		id:          id,
		scroll:      container.NewVScroll(container.NewVBox()),
		lastMessage: time.Now().Unix(),
	}

	thread.button = widget.NewButton(id, func() {
		fyneUI.displayHistory(thread)
	})
	thread.button.Importance = widget.LowImportance
	thread.button.Alignment = widget.ButtonAlignLeading

	fyneUI.threads[id] = thread
	fyneUI.refreshThreadOrder()
}

func (fyneUI *Fyne) ReceivedMessage(threadID, username, message string) { // TODO: this should probably take the protobuf definition
	if thread, exists := fyneUI.threads[threadID]; exists {
		chatHistory := thread.scroll.Content.(*fyne.Container)
		usernameText := widget.NewLabel(username)
		usernameText.TextStyle = fyne.TextStyle{Bold: true}
		messageBox := container.NewVBox(
			usernameText,
			widget.NewLabel(message),
			widget.NewSeparator(),
		)
		chatHistory.Objects = append(chatHistory.Objects, messageBox)
		chatHistory.Refresh()

		if fyneUI.isActive(thread) {
			// TODO: not working, always off by one.  probably need something manual based on this
			// https://github.com/fyne-io/fyne/blob/a25b08480d742f016ee24c1b37aa02c3bb433bd5/internal/widget/scroller.go#L473
			// also: this should only happen when the user was already scrolled all the way down,
			thread.scroll.ScrollToBottom()
		} else {
			// This thread isn't active, mark the button as unread
			thread.button.Importance = widget.HighImportance
			thread.button.Refresh()
		}

		thread.lastMessage = time.Now().Unix()
		fyneUI.refreshThreadOrder()
	} else {
		log.WithFields(log.Fields{
			"id": threadID,
		}).Warn("thread does not exist on message receive")
	}
}
