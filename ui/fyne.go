package ui

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	log "github.com/sirupsen/logrus"
)

type Fyne struct {
	app                fyne.App
	threadVBox         *fyne.Container
	entryBar           *container.Split
	chatContainer      *fyne.Container
	chatHistory        *container.Scroll
	chatHistoriesViews map[string]*container.Scroll
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
			time.Sleep(500 * time.Millisecond)
		}
	}()

	fyneUI.ReceivedMessage("person2", "Person 2", "hello this is from person 2")
	fyneUI.ReceivedMessage("person1", "Person 1", "hello this is from person 1")
	fyneUI.ReceivedMessage("person2", "Person 2", "hello this is from person 2")
	fyneUI.ReceivedMessage("person3", "Person 3", "hello this is from person 3")
	fyneUI.ReceivedMessage("person1", "Person 1", "hello this is from person 1")
	/*
		chat1 := widget.NewButton("Chat 1", func() {})
		chat1.Importance = widget.LowImportance
		chat2 := widget.NewButton("Chat 2", func() {})
		chat2.Importance = widget.LowImportance
		go func() {
			time.Sleep(3 * time.Second)
			chat2.Importance = widget.HighImportance
			threads.Objects = []fyne.CanvasObject{threads.Objects[1], threads.Objects[0]}
			threads.Refresh()
		}()
	*/
}

func (fyneUI *Fyne) Build(configDirectory string) {
	fyneUI.chatHistoriesViews = make(map[string]*container.Scroll)
	fyneApp := app.New()
	fyneUI.app = fyneApp
	mainWindow := fyneUI.app.NewWindow("Bounce")
	mainWindow.SetMaster()
	mainWindow.SetMainMenu(fyneUI.mainMenu())
	mainWindow.SetCloseIntercept(func() {
		log.WithFields(log.Fields{
			"at": "desktop.DesktopUI.Build",
		}).Info("window close button hit, shutting down")
		//w.Close()
		//a.Quit()
		fyneUI.Quit()
	})

	threads := fyneUI.threadView()
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

func (fyneUI *Fyne) threadView() *container.Scroll {
	fyneUI.threadVBox = container.NewVBox()
	return container.NewVScroll(fyneUI.threadVBox)
}

func (fyneUI *Fyne) textEntry() *container.Split {
	fyneUI.entryBar = container.NewHSplit(
		widget.NewMultiLineEntry(),
		widget.NewButton("Send", func() {}),
	)
	return fyneUI.entryBar
}

func (fyneUI *Fyne) findOrCreateChatHistoryScroll(id string) *container.Scroll {
	// if it exists in the UI's map, return it
	if history, exists := fyneUI.chatHistoriesViews[id]; exists {
		return history
	}
	chatHistory := container.NewVBox()
	chatHistoryScroll := container.NewVScroll(chatHistory)
	fyneUI.chatHistoriesViews[id] = chatHistoryScroll
	return chatHistoryScroll
}

func (fyneUI *Fyne) Run() {
	go fyneUI.simulate()
	fyneUI.app.Run()
}

func (fyneUI *Fyne) Quit() {
	fyneUI.app.Quit()
}

func (fyneUI *Fyne) AddThread(name string) { // TODO: add UUID
	fyneUI.findOrCreateChatHistoryScroll(name)
	newThread := widget.NewButton(name, func() {
		chatHistory := fyneUI.findOrCreateChatHistoryScroll(name) // actually ID
		fyneUI.chatContainer.Objects = []fyne.CanvasObject{chatHistory}
		fyneUI.chatContainer.Refresh()
		// TODO: how do I reference self in this function?
		//newThread.Importance = widget.LowImportance
		//newThread.Refresh()
	})
	newThread.Importance = widget.LowImportance
	newThread.Alignment = widget.ButtonAlignLeading
	fyneUI.threadVBox.Objects = append(
		fyneUI.threadVBox.Objects,
		newThread,
	)
	fyneUI.threadVBox.Refresh()
}

func (fyneUI *Fyne) ReceivedMessage(threadID, username, message string) {
	if thread, exists := fyneUI.chatHistoriesViews[threadID]; exists {
		chatHistory := thread.Content.(*fyne.Container)
		usernameText := widget.NewLabel(username)
		usernameText.TextStyle = fyne.TextStyle{Bold: true}
		messageBox := container.NewVBox(
			usernameText,
			widget.NewLabel(message),
			widget.NewSeparator(),
		)
		chatHistory.Objects = append(chatHistory.Objects, messageBox)
		chatHistory.Refresh()
		// TODO: not working, always off by one.  Also only want this when open
		// probably best to make a custom "thread" object using fyne objects internally
		// to exist concepts like "IsOpen()" and "IsUnread()"
		thread.ScrollToBottom()
		// TODO: mark as important and sort to the top.  insertion sort by most
		// recent unread
		// TODO: need a refernce to the thread's button
		//thread.Importance = widget.HighImportance
		//thread.Refresh()
	} else {
		log.Warn("thread does not exist: " + threadID)
	}
}
