package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

func (fyneUI *Fyne) showMainContainer() {
	fyneUI.mainWindow.SetContent(fyneUI.mainContainer)
	fyneUI.mainContainer.Show()

}

func (fyneUI *Fyne) buildMainWindow() {
	mainWindow := fyneUI.app.NewWindow("Bounce")
	mainWindow.SetMaster()
	mainWindow.SetMainMenu(fyneUI.buildMainMenu())

	mainWindow.SetCloseIntercept(func() {
		// There's some bug in Fyne where the app will hang when the close button
		// is hit unless this is explicitly set https://github.com/fyne-io/fyne/issues/2314
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

	mainWindow.SetContent(fyneUI.networkLoading) // TODO: move to main builder function
	mainWindow.Show()

	fyneUI.mainWindow = mainWindow
}
