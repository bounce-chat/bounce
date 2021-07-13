package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	log "github.com/sirupsen/logrus"
)

type Fyne struct {
	app fyne.App
}

func (desktopUI *Fyne) Build(configDirectory string) {
	a := app.New()
	desktopUI.app = a
	w := a.NewWindow("Bounce")
	w.SetMaster()
	w.SetCloseIntercept(func() {
		log.WithFields(log.Fields{
			"at": "desktop.DesktopUI.Build",
		}).Info("window close button hit, shutting down")
		//w.Close()
		//a.Quit()
		desktopUI.Quit()
	})
	threads := container.NewVBox(
		widget.NewButton("Chat 1", func() {}),
		widget.NewButton("Chat 2", func() {}),
	)
	w.SetContent(container.NewHSplit(
		threads,
		container.NewVSplit(
			widget.NewMultiLineEntry(),
			widget.NewMultiLineEntry(),
		),
	))
	w.Show()

}

func (desktopUI *Fyne) Run() {
	desktopUI.app.Run()
}

func (desktopUI *Fyne) Quit() {
	desktopUI.app.Quit()
}
