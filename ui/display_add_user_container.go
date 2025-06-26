package ui

import (
	"bytes"
	"image"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/hkparker/bounce/chat"
	"github.com/rymdport/go-qrcode"
	log "github.com/sirupsen/logrus"
)

func (ui *ui) showDisplayAddUserString() {
	if fyne.CurrentDevice().IsMobile() {
		ui.viewStack = append(ui.viewStack, view{viewType: viewTypeDisplayAddUserString})
	}
	newAddUserString := ui.bounce.GetNewAddUserString()
	//err := ui.addUserString.Set(newAddUserString)
	//if err != nil {
	//	log.Fatal("data bindings are broken")
	//}
	ui.addUserStringEntry.SetText(newAddUserString)

	qrData, err := qrcode.Encode(newAddUserString, qrcode.Medium, 256)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error QR encoding add user string")
	} else {
		qrImg, _, err := image.Decode(bytes.NewReader(qrData))
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error decoding QR data as image")
		} else {
			ui.addUserQRCode.Image = qrImg
			ui.addUserQRCode.Refresh()
		}
	}

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

	ui.addUserQRCode = &canvas.Image{
		FillMode: canvas.ImageFillContain,
	}
	ui.addUserQRCode.SetMinSize(fyne.Size{Height: 256, Width: 256})

	ui.addUserStringEntry = widget.NewEntry()
	ui.addUserStringEntry.ActionItem = widget.NewButtonWithIcon("", theme.ContentCopyIcon(), func() { ui.mainWindow.Clipboard().SetContent(ui.addUserStringEntry.Text) })

	ui.displayAddUserString = container.New(
		layout.NewBorderLayout(closeBar, nil, nil, nil),
		closeBar,
		container.New(
			layout.NewBorderLayout(title, nil, nil, nil),
			title,
			container.NewVBox(
				ui.addUserQRCode,
				ui.addUserStringEntry,
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
