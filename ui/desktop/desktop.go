package desktop

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

type DesktopUI struct {
	app fyne.App
}

func (desktopUI *DesktopUI) Init(configDirectory string) {
	a := app.New()
	w := a.NewWindow("Hello")

	hello := widget.NewLabel("Hello Fyne!")
	w.SetContent(container.NewVBox(
		hello,
		widget.NewButton("Hi!", func() {
			hello.SetText("Welcome :)")
		}),
	))
	w.Show()

	desktopUI.app = a
}

func (desktopUI *DesktopUI) Run() {
	desktopUI.app.Run()
}

func (desktopUI *DesktopUI) Quit() {
	desktopUI.app.Quit()
}
