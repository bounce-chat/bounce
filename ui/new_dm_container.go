package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type newDM struct {
	searchEntry *widget.Entry
	scroll      *container.Scroll
}

func (ui *ui) showNewDM() {
	if fyne.CurrentDevice().IsMobile() {
		ui.state.viewStack = append(ui.state.viewStack, view{viewType: viewTypeNewDM})
	}
	ui.state.currentView = viewTypeNewDM
	ui.widgets.newDM.searchEntry.Text = ""
	ui.widgets.newDM.searchEntry.Refresh()
	ui.refreshAllUsersDMLinks()
	ui.window.SetContent(ui.views.newDM)
	ui.window.Canvas().Focus(ui.widgets.newDM.searchEntry)
}

func (ui *ui) buildNewDM() {
	ui.widgets.newDM = &newDM{
		scroll: container.NewVScroll(container.NewVBox()),
	}

	closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		if fyne.CurrentDevice().IsMobile() {
			ui.mobileBack()
		} else {
			ui.showMainContainer()
		}
	})
	closeButton.Importance = widget.LowImportance

	closeBar := container.New(
		layout.NewBorderLayout(nil, nil, nil, closeButton),
		closeButton,
	)

	ui.widgets.newDM.searchEntry = widget.NewEntry()
	ui.widgets.newDM.searchEntry.OnChanged = func(str string) {
		ui.refreshAllUsersDMLinks()
	}
	ui.views.newDM = container.New(
		layout.NewBorderLayout(closeBar, nil, nil, nil),
		closeBar,
		container.New(
			layout.NewBorderLayout(ui.widgets.newDM.searchEntry, nil, nil, nil),
			ui.widgets.newDM.searchEntry,
			ui.widgets.newDM.scroll,
		),
	)
}

func (ui *ui) refreshAllUsersDMLinks() {
	usersBox := container.NewVBox()
	for _, thisUser := range ui.users.search(ui.widgets.newDM.searchEntry.Text) {
		func(u *user) {
			openDMButton := newUserButton(
				newDefaultImage(u.id, u.images, u.initials, theme.IconInlineSize()*2, ui.bounce.GetFileData, nil),
				u.getDisplayName(),
				false,
				func() {
					dm, ok := ui.threads.getDM(u.id)
					if !ok {
						defer ui.bounce.SetOpenDM(u.id, true)
						dm = ui.openAndPopulateDM(u)
						if dm == nil {
							return
						}
					}

					if fyne.CurrentDevice().IsMobile() {
						ui.mobileBack() // Close new DM view
						ui.mobileBack() // Close menu
						ui.state.viewStack = append(ui.state.viewStack, view{viewType: viewTypeThread, context: dm.user.id})
						ui.state.currentView = viewTypeThread
					} else {
						ui.showMainContainer()
					}
					ui.bounce.UserConnectionDesired(u.id)
					ui.displayThread(dm)
				},
			)
			usersBox.Objects = append(
				usersBox.Objects,
				openDMButton,
			)
		}(thisUser)
	}
	ui.widgets.newDM.scroll.Content = usersBox
	ui.widgets.newDM.scroll.Refresh()
}
