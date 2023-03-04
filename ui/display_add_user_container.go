package ui

import (
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/hkparker/bounce/chat"
	log "github.com/sirupsen/logrus"
)

func (fyneUI *Fyne) showDisplayAddUserString() {
	err := fyneUI.addUserString.Set(fyneUI.callbacks.GetNewAddUserString())
	if err != nil {
		log.Fatal("data bindings are broken")
	}

	// TODO: update the QR code data

	fyneUI.mainWindow.SetContent(fyneUI.displayAddUserString)
	fyneUI.displayAddUserString.Show()

}

func (fyneUI *Fyne) buildDisplayAddUserString() {
	title := widget.NewLabel("Scan or paste this on your friend's device:")

	closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		fyneUI.showMainContainer()
	})
	closeButton.Importance = widget.LowImportance

	closeBar := container.New(
		layout.NewBorderLayout(nil, nil, nil, closeButton),
		closeButton,
	)

	fyneUI.displayAddUserString = container.New(
		layout.NewBorderLayout(closeBar, nil, nil, nil),
		closeBar,
		container.New(
			layout.NewBorderLayout(title, nil, nil, nil),
			title,
			container.NewVBox(
				widget.NewEntryWithData(fyneUI.addUserString),
			),
		),
	)
}

func (fyneUI *Fyne) FriendAdded(u chat.User) {
	fyneUI.users.add(&user{id: u.ID, name: u.Name})
	fyneUI.showMainContainer() // TODO: only if still shoing the add user container, and actually go to the last screen
	dialog.ShowInformation("New contact added", u.Name+" was added as a friend", fyneUI.mainWindow)
}
