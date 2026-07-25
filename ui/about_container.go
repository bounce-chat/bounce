package ui

import (
	"fmt"
	"runtime/debug"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

func (ui *ui) showAbout() {
	if fyne.CurrentDevice().IsMobile() {
		ui.state.viewStack = append(ui.state.viewStack, view{viewType: viewTypeAbout})
	}
	ui.setContent(viewTypeAbout, ui.views.about)
}

func (ui *ui) buildAbout() {
	var closeBar fyne.CanvasObject
	if fyne.CurrentDevice().IsMobile() {
		closeButton := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() {
			if fyne.CurrentDevice().IsMobile() {
				ui.mobileBack()
			} else {
				ui.showMainContainer()
			}
		})
		closeButton.Importance = widget.LowImportance

		closeBar = container.New(
			layout.NewBorderLayout(nil, nil, closeButton, nil),
			closeButton,
		)
	} else {
		closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
			if fyne.CurrentDevice().IsMobile() {
				ui.mobileBack()
			} else {
				ui.showMainContainer()
			}
		})
		closeButton.Importance = widget.LowImportance

		closeBar = container.New(
			layout.NewBorderLayout(nil, nil, nil, closeButton),
			closeButton,
		)
	}

	revision := "000000"
	buildInfo, ok := debug.ReadBuildInfo()
	if ok {
		for _, bs := range buildInfo.Settings {
			if bs.Key == "vcs.revision" {
				revision = bs.Value
				if len(revision) > 6 {
					revision = revision[:6]
				}
				break
			}
		}

		for _, bs := range buildInfo.Settings {
			if bs.Key == "vcs.modified" {
				if bs.Value == "true" {
					revision = revision + "-modified"
				}
				break
			}
		}
	}

	ui.views.about = container.New(
		layout.NewBorderLayout(closeBar, nil, nil, nil),
		closeBar,
		container.NewCenter(
			container.NewVBox(
				makeLogo(228, 167), // TODO: choose reasonable values here, https://github.com/fyne-io/fyne/blob/v2.0.3/cmd/fyne_demo/tutorials/welcome.go#L25
				widget.NewLabel("version 0.1.0-develop"),
				widget.NewLabel(fmt.Sprintf("build %s", revision)),
			),
		),
	)
}
