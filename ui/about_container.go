package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

func (fyneUI *Fyne) showAbout() {
	if fyne.CurrentDevice().IsMobile() {
		fyneUI.viewStack = append(fyneUI.viewStack, view{viewType: viewTypeAbout})
	}
	fyneUI.mainWindow.SetContent(fyneUI.about)
	fyneUI.about.Show()
}

func (fyneUI *Fyne) buildAbout() {
	closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		if fyne.CurrentDevice().IsMobile() {
			fyneUI.mobileBack()
		} else {
			fyneUI.showMainContainer()
		}
	})
	closeButton.Importance = widget.LowImportance

	closeBar := container.New(
		layout.NewBorderLayout(nil, nil, nil, closeButton),
		closeButton,
	)

	fyneUI.about = container.New(
		layout.NewBorderLayout(closeBar, nil, nil, nil),
		closeBar,
		container.NewCenter(
			container.NewVBox(
				makeLogo(228, 167), // TODO: choose reasonable values here, https://github.com/fyne-io/fyne/blob/v2.0.3/cmd/fyne_demo/tutorials/welcome.go#L25
				widget.NewLabel("version 0.0.0 pre-release evaluation demo"),
				widget.NewLabel("for your consideration"),
			),
		),
	)
}
