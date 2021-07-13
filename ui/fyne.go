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
	app        fyne.App
	threadVBox *fyne.Container
	entryBar   *container.Split
}

func (fyneUI *Fyne) simulate() {
	time.Sleep(3 * time.Second)
	fyneUI.AddThread("person1")
	time.Sleep(3 * time.Second)
	fyneUI.AddThread("person2")
	time.Sleep(3 * time.Second)
	fyneUI.AddThread("person3")
}

func (fyneUI *Fyne) Build(configDirectory string) {
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

	/*
		chatHistory := container.NewVBox()
		chatHistoryScroll := container.NewVScroll(chatHistory)
		go func() {
			time.Sleep(10 * time.Second)
			username := widget.NewLabel("username")
			username.TextStyle = fyne.TextStyle{Bold: true}
			oldMessage1 := container.NewVBox(
				username,
				widget.NewLabel("this is a message someone sent"),
				widget.NewSeparator(),
			)
			chatHistory.Objects = append(chatHistory.Objects, oldMessage1)
			//chatHistoryScroll.ScrollToBottom() // TODO: not working
			chatHistory.Refresh()
		}()
	*/

	threads := fyneUI.threadView()
	mainWindow.SetContent(container.NewHSplit(
		threads,
		container.NewVSplit(
			//chatHistoryScroll,
			widget.NewLabel("Select a chat thread on the left"),
			fyneUI.textEntry(),
		),
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

func (fyneUI *Fyne) Run() {
	go fyneUI.simulate()
	fyneUI.app.Run()
}

func (fyneUI *Fyne) Quit() {
	fyneUI.app.Quit()
}

func (fyneUI *Fyne) AddThread(name string) {
	newThread := widget.NewButton(name, func() {})
	newThread.Importance = widget.HighImportance
	fyneUI.threadVBox.Objects = append(
		fyneUI.threadVBox.Objects,
		newThread,
	)
	fyneUI.threadVBox.Refresh()
}

func (fyneUI *Fyne) ReceivedMessage() {
}
