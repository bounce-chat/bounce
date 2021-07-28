package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

func (fyneUI *Fyne) showNetworkLoading() {
	fyneUI.mainWindow.SetMainMenu(nil)
	fyneUI.mainWindow.SetContent(fyneUI.networkLoading)
	fyneUI.networkLoading.Show()
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
