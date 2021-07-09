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

func (desktopUI *DesktopUI) Build(configDirectory string) {
	a := app.New()
	w := a.NewWindow("Bounce")
	w.SetMaster()

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

	desktopUI.app = a
}

func (desktopUI *DesktopUI) Run() {
	desktopUI.app.Run()
}

func (desktopUI *DesktopUI) Quit() {
	desktopUI.app.Quit()
}
