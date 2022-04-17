package ui

import (
	"github.com/hkparker/bounce/chat"

	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	log "github.com/sirupsen/logrus"
)

func (fyneUI *Fyne) showNewDM() {
	fyneUI.refreshAllUsersDMLinks()
	fyneUI.mainWindow.SetContent(fyneUI.newDM)
	fyneUI.newDM.Show()
}

func (fyneUI *Fyne) buildNewDM() {
	closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		fyneUI.showMainContainer()
	})
	closeButton.Importance = widget.LowImportance

	closeBar := container.New(
		layout.NewBorderLayout(nil, nil, nil, closeButton),
		closeButton,
	)

	label := widget.NewLabel("Select a user to message:")
	fyneUI.newDM = container.New(
		layout.NewBorderLayout(closeBar, nil, nil, nil),
		closeBar,
		container.New(
			layout.NewBorderLayout(label, nil, nil, nil),
			label,
			fyneUI.allUsersDMLinksScroll,
		),
	)
}

func (fyneUI *Fyne) refreshAllUsersDMLinks() {
	usersBox := container.NewVBox()
	for _, thisUser := range fyneUI.users.alphabetized() {
		func(u *user) {
			addUserButton := widget.NewButtonWithIcon(u.name, newEmbeddedResource("assets/not_found.png"), func() { // TODO: use the user's icon
				dm, dmExists := fyneUI.dms[u.id]
				if !dmExists {
					fyneUI.NewDirectMessage(chat.User{
						ID:   u.id,
						Name: u.name,
					})
					dm, dmExists = fyneUI.dms[u.id]
					if !dmExists {
						log.Fatal("DM doesn't exist immediately after creation")
					}
				}
				fyneUI.showMainContainer()
				fyneUI.displayThread(dm)
			})
			addUserButton.Alignment = widget.ButtonAlignLeading
			addUserButton.Importance = widget.LowImportance
			usersBox.Objects = append(
				usersBox.Objects,
				addUserButton,
			)
		}(thisUser)
	}
	fyneUI.allUsersDMLinksScroll.Content = usersBox
	fyneUI.allUsersDMLinksScroll.Refresh()
}
