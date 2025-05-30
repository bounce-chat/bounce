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

func (fyneUI *Fyne) showNewDM() {
	if fyne.CurrentDevice().IsMobile() {
		fyneUI.viewStack = append(fyneUI.viewStack, view{viewType: viewTypeNewDM})
	}
	fyneUI.newDMUserSearchEntry.Text = ""
	fyneUI.newDMUserSearchEntry.Refresh()
	fyneUI.refreshAllUsersDMLinks()
	fyneUI.mainWindow.SetContent(fyneUI.newDM)
	fyneUI.newDM.Show()
	fyneUI.mainWindow.Canvas().Focus(fyneUI.newDMUserSearchEntry)
}

func (fyneUI *Fyne) buildNewDM() {
	fyneUI.allUsersDMLinksScroll = container.NewVScroll(container.NewVBox())

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

	fyneUI.newDMUserSearchEntry = widget.NewEntry()
	fyneUI.newDMUserSearchEntry.OnChanged = func(str string) {
		fyneUI.refreshAllUsersDMLinks()
	}
	fyneUI.newDM = container.New(
		layout.NewBorderLayout(closeBar, nil, nil, nil),
		closeBar,
		container.New(
			layout.NewBorderLayout(fyneUI.newDMUserSearchEntry, nil, nil, nil),
			fyneUI.newDMUserSearchEntry,
			fyneUI.allUsersDMLinksScroll,
		),
	)
}

func (fyneUI *Fyne) refreshAllUsersDMLinks() {
	usersBox := container.NewVBox()
	for _, thisUser := range fyneUI.users.search(fyneUI.newDMUserSearchEntry.Text) {
		func(u *user) {
			openDMButton := newUserButton(
				newDefaultImage(u.id, u.images, u.initials, theme.IconInlineSize()*2, fyneUI.callbacks.GetFileData, nil),
				u.getName(),
				false,
				func() {
					dm, dmExists := fyneUI.dms[u.id]
					if !dmExists {
						fyneUI.NewDirectMessage(chat.User{
							ID:   u.id,
							Name: u.getName(),
						})
						dm, dmExists = fyneUI.dms[u.id]
						if !dmExists {
							log.Fatal("DM doesn't exist immediately after creation")
						}
					}
					if fyne.CurrentDevice().IsMobile() {
						fyneUI.mobileBack() // Close new DM view
						fyneUI.mobileBack() // Close menu
						fyneUI.viewStack = append(fyneUI.viewStack, view{viewType: viewTypeThread, context: dm.user.id})
					} else {
						fyneUI.showMainContainer()
					}
					fyneUI.callbacks.UserConnectionDesired(dm.user.id)
					fyneUI.displayThread(dm)
				},
			)
			usersBox.Objects = append(
				usersBox.Objects,
				openDMButton,
			)
		}(thisUser)
	}
	fyneUI.allUsersDMLinksScroll.Content = usersBox
	fyneUI.allUsersDMLinksScroll.Refresh()
}
