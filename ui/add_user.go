package ui

import (
	"bytes"
	"errors"
	"image"
	"strings"
	"time"

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

type addUser struct {
	currentStep  *widget.Label
	progressBar  *widget.ProgressBarInfinite
	scanEntry    *widget.Entry
	displayEntry *widget.Entry
	qrCode       *canvas.Image
	scanButton   *widget.Button
	shareButton  *widget.Button
}

func (ui *ui) showAddUser() {
	if fyne.CurrentDevice().IsMobile() {
		ui.state.viewStack = append(ui.state.viewStack, view{viewType: viewTypeAddUser})
	}
	ui.state.currentView = viewTypeAddUser

	ui.widgets.addUser.scanEntry.Text = ""
	ui.widgets.addUser.scanEntry.Refresh()
	ui.widgets.addUser.progressBar.Hide()
	ui.widgets.addUser.currentStep.Text = ""
	ui.widgets.addUser.currentStep.Refresh()
	ui.widgets.addUser.currentStep.Hide()

	newAddUserString := ui.bounce.GetNewAddUserString()
	ui.widgets.addUser.displayEntry.SetText(newAddUserString)
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
			ui.widgets.addUser.qrCode.Image = qrImg
			ui.widgets.addUser.qrCode.Refresh()
		}
	}

	ui.widgets.addUser.shareButton.Importance = widget.HighImportance
	ui.widgets.addUser.shareButton.Refresh()
	ui.widgets.addUser.scanButton.Importance = widget.MediumImportance
	ui.widgets.addUser.scanButton.Refresh()

	ui.containers.addUserContent.Objects = []fyne.CanvasObject{ui.containers.displayAddUserString}
	ui.containers.addUserContent.Refresh()
	ui.window.SetContent(ui.views.addUser)
}

func (ui *ui) buildAddUser() {
	ui.buildScanUser()
	ui.buildDisplayAddUserString()

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

	ui.widgets.addUser.shareButton = widget.NewButton("Share", func() {
		ui.widgets.addUser.shareButton.Importance = widget.HighImportance
		ui.widgets.addUser.shareButton.Refresh()
		ui.widgets.addUser.scanButton.Importance = widget.MediumImportance
		ui.widgets.addUser.scanButton.Refresh()

		ui.containers.addUserContent.Objects = []fyne.CanvasObject{ui.containers.displayAddUserString}
		ui.containers.addUserContent.Refresh()
	})
	ui.widgets.addUser.shareButton.Importance = widget.HighImportance
	ui.widgets.addUser.scanButton = widget.NewButton("Scan", func() {
		ui.widgets.addUser.scanButton.Importance = widget.HighImportance
		ui.widgets.addUser.scanButton.Refresh()
		ui.widgets.addUser.shareButton.Importance = widget.MediumImportance
		ui.widgets.addUser.shareButton.Refresh()

		ui.containers.addUserContent.Objects = []fyne.CanvasObject{ui.containers.scanUser}
		ui.containers.addUserContent.Refresh()
		if !fyne.CurrentDevice().IsMobile() {
			ui.window.Canvas().Focus(ui.widgets.addUser.scanEntry)
		}
	})
	ui.widgets.addUser.scanButton.Importance = widget.MediumImportance
	header := container.NewVBox(
		closeBar,
		container.NewCenter(container.NewHBox(
			ui.widgets.addUser.shareButton,
			ui.widgets.addUser.scanButton,
		)),
	)
	ui.containers.addUserContent = container.NewStack()
	ui.views.addUser = container.New(
		layout.NewBorderLayout(header, nil, nil, nil),
		header,
		ui.containers.addUserContent,
	)
}

func (ui *ui) buildScanUser() {
	ui.widgets.addUser = &addUser{
		currentStep: widget.NewLabel(""),
		progressBar: widget.NewProgressBarInfinite(),
		scanEntry:   widget.NewEntry(),
	}

	ui.widgets.addUser.scanEntry.OnSubmitted = func(str string) {
		ui.widgets.addUser.progressBar.Start()
		ui.widgets.addUser.progressBar.Show()
		go func() {
			if ui.state.networkState == networkStateStarting || ui.state.networkState == networkStateOffline {
				fyne.Do(func() {
					ui.widgets.addUser.currentStep.Text = "Waiting for network..."
					ui.widgets.addUser.currentStep.Refresh()
					ui.widgets.addUser.currentStep.Show()
				})
				for {
					time.Sleep(500 * time.Millisecond) // TODO: use a callback for this?
					if ui.state.networkState == networkStateOnline {
						break
					}
				}
			}

			ui.widgets.addUser.currentStep.Text = "Sending friend request..."
			ui.widgets.addUser.currentStep.Refresh()
			ui.widgets.addUser.currentStep.Show()
			err := ui.bounce.RequestToAddUser(strings.TrimSpace(str))
			fyne.Do(func() {
				if err != nil {
					ui.widgets.addUser.scanEntry.Text = ""
					ui.containers.scanUser.Refresh()
					ui.showDialog(dialog.NewError(errors.New("Error sending friend request: "+err.Error()), ui.window), nil)
				} else {
					ui.widgets.addUser.currentStep.Text = "Waiting for response..."
					ui.widgets.addUser.currentStep.Refresh()
					ui.views.addUser.Refresh()
				}
			})
		}()
	}
	ui.widgets.addUser.scanEntry.ActionItem = widget.NewButtonWithIcon("", theme.MediaPhotoIcon(), func() {
		// TODO: open the camera
		//_, err := camera.Open()
		//if err != nil {
		//	log.Error(err.Error())
		//}
	})
	ui.widgets.addUser.scanEntry.ActionItem.(*widget.Button).Disable()

	ui.containers.scanUser = container.NewVBox(
		widget.NewLabel("Paste in the string or scan the QR code to add friend"),
		ui.widgets.addUser.scanEntry,
		ui.widgets.addUser.currentStep,
		ui.widgets.addUser.progressBar,
	)
}

func (ui *ui) buildDisplayAddUserString() {
	title := widget.NewLabel("Scan or paste this on your friend's device:")

	ui.widgets.addUser.qrCode = &canvas.Image{
		FillMode: canvas.ImageFillContain,
	}
	ui.widgets.addUser.qrCode.SetMinSize(fyne.Size{Height: 256, Width: 256})

	ui.widgets.addUser.displayEntry = widget.NewEntry()
	ui.widgets.addUser.displayEntry.ActionItem = widget.NewButtonWithIcon("", theme.ContentCopyIcon(), func() { ui.window.Clipboard().SetContent(ui.widgets.addUser.displayEntry.Text) })

	ui.containers.displayAddUserString = container.New(
		layout.NewBorderLayout(title, nil, nil, nil),
		title,
		container.NewVBox(
			ui.widgets.addUser.qrCode,
			ui.widgets.addUser.displayEntry,
		),
	)
}

func (ui *ui) UserAdded(u chat.User) {
	ui.widgets.addUser.currentStep.Text = "Success!"
	ui.widgets.addUser.currentStep.Refresh()
	ui.widgets.addUser.progressBar.Stop()
	ui.widgets.addUser.progressBar.Hide()
	newUser := makeUser(u)
	ui.users.add(newUser)
	if ui.state.currentView == viewTypeAddUser {
		if fyne.CurrentDevice().IsMobile() {
			ui.mobileBack()
		} else {
			ui.showMainContainer()
		}
	}
	if !ui.state.initialSyncIncomplete {
		fyne.DoAndWait(func() {
			ui.NewDirectMessage(u) // TODO: no DM state exists for the user yet
			ui.showDialog(dialog.NewInformation("New contact added", u.Name+" was added as a friend", ui.window), nil)
		})
	}
}

func (ui *ui) AddUserRequestRejected(peer string) {
	fyne.DoAndWait(func() {
		ui.showDialog(dialog.NewError(errors.New("Friend request rejected, make sure you scan the device quickly"), ui.window), nil)
	})
}
