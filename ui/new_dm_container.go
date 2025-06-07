package ui

import (
	"github.com/hkparker/bounce/chat"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	log "github.com/sirupsen/logrus"
)

func (ui *ui) showNewDM() {
	if fyne.CurrentDevice().IsMobile() {
		ui.viewStack = append(ui.viewStack, view{viewType: viewTypeNewDM})
	}
	ui.newDMUserSearchEntry.Text = ""
	ui.newDMUserSearchEntry.Refresh()
	ui.refreshAllUsersDMLinks()
	ui.mainWindow.SetContent(ui.newDM)
	ui.newDM.Show()
	ui.mainWindow.Canvas().Focus(ui.newDMUserSearchEntry)
}

func (ui *ui) buildNewDM() {
	ui.allUsersDMLinksScroll = container.NewVScroll(container.NewVBox())

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

	ui.newDMUserSearchEntry = widget.NewEntry()
	ui.newDMUserSearchEntry.OnChanged = func(str string) {
		ui.refreshAllUsersDMLinks()
	}
	ui.newDM = container.New(
		layout.NewBorderLayout(closeBar, nil, nil, nil),
		closeBar,
		container.New(
			layout.NewBorderLayout(ui.newDMUserSearchEntry, nil, nil, nil),
			ui.newDMUserSearchEntry,
			ui.allUsersDMLinksScroll,
		),
	)
}

func (ui *ui) refreshAllUsersDMLinks() {
	usersBox := container.NewVBox()
	for _, thisUser := range ui.users.search(ui.newDMUserSearchEntry.Text) {
		func(u *user) {
			openDMButton := newUserButton(
				newDefaultImage(u.id, u.images, u.initials, theme.IconInlineSize()*2, ui.bounce.GetFileData, nil),
				u.getName(),
				false,
				func() {
					dm, dmExists := ui.dms[u.id]
					if !dmExists {
						ui.NewDirectMessage(chat.User{
							ID:   u.id,
							Name: u.getName(),
						})
						dm, dmExists = ui.dms[u.id]
						if !dmExists {
							log.Fatal("DM doesn't exist immediately after creation")
						}
					}
					if fyne.CurrentDevice().IsMobile() {
						ui.mobileBack() // Close new DM view
						ui.mobileBack() // Close menu
						ui.viewStack = append(ui.viewStack, view{viewType: viewTypeThread, context: dm.user.id})
					} else {
						ui.showMainContainer()
					}
					ui.bounce.UserConnectionDesired(dm.user.id)
					ui.displayThread(dm)
				},
			)
			usersBox.Objects = append(
				usersBox.Objects,
				openDMButton,
			)
		}(thisUser)
	}
	ui.allUsersDMLinksScroll.Content = usersBox
	ui.allUsersDMLinksScroll.Refresh()
}
