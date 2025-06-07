package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/hkparker/bounce/chat"
	log "github.com/sirupsen/logrus"
)

func (ui *ui) showDisplayAddUserString() {
	if fyne.CurrentDevice().IsMobile() {
		ui.viewStack = append(ui.viewStack, view{viewType: viewTypeDisplayAddUserString})
	}
	err := ui.addUserString.Set(ui.bounce.GetNewAddUserString())
	if err != nil {
		log.Fatal("data bindings are broken")
	}

	// TODO: update the QR code data

	ui.mainWindow.SetContent(ui.displayAddUserString)
	ui.displayAddUserString.Show()
}

func (ui *ui) buildDisplayAddUserString() {
	title := widget.NewLabel("Scan or paste this on your friend's device:")

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

	ui.displayAddUserString = container.New(
		layout.NewBorderLayout(closeBar, nil, nil, nil),
		closeBar,
		container.New(
			layout.NewBorderLayout(title, nil, nil, nil),
			title,
			container.NewVBox(
				widget.NewEntryWithData(ui.addUserString),
			),
		),
	)
}

func (ui *ui) UserAdded(u chat.User) {
	newUser := makeUser(u.ID, u.Name)
	ui.users.add(newUser)
	//ui.showMainContainer() // TODO: only if still showing the add user container, and actually go to the last screen
	if !ui.initialSyncIncomplete {
		fyne.DoAndWait(func() {
			ui.showDialog(dialog.NewInformation("New contact added", u.Name+" was added as a friend", ui.mainWindow), nil)
		})
	}
}
