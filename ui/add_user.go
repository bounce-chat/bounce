package ui

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"io"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/hkparker/bounce/chat"
	"github.com/rymdport/go-qrcode"
	log "github.com/sirupsen/logrus"
)

type addUserWidgets struct {
	currentStep *widget.Label
	progressBar *widget.ProgressBarInfinite
	entry       *widget.Entry
}

func (ui *ui) showAddUser() {
	if fyne.CurrentDevice().IsMobile() {
		ui.viewStack = append(ui.viewStack, view{viewType: viewTypeAddUser})
	}
	ui.currentView = viewTypeAddUser

	ui.addUserWidgets.entry.Text = ""
	ui.addUserWidgets.entry.Refresh()
	ui.addUserWidgets.progressBar.Hide()
	ui.addUserWidgets.currentStep.Text = ""
	ui.addUserWidgets.currentStep.Refresh()
	ui.addUserWidgets.currentStep.Hide()

	newAddUserString := ui.bounce.GetNewAddUserString()
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

	ui.addUserContent.Objects = []fyne.CanvasObject{ui.displayAddUserString}
	ui.addUserContent.Refresh()
	ui.mainWindow.SetContent(ui.addUser)
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

	var shareButton *widget.Button
	var scanButton *widget.Button
	shareButton = widget.NewButton("Share", func() {
		shareButton.Importance = widget.HighImportance
		shareButton.Refresh()
		scanButton.Importance = widget.MediumImportance
		scanButton.Refresh()

		ui.addUserContent.Objects = []fyne.CanvasObject{ui.displayAddUserString}
		ui.addUserContent.Refresh()
	})
	shareButton.Importance = widget.HighImportance
	scanButton = widget.NewButton("Scan", func() {
		scanButton.Importance = widget.HighImportance
		scanButton.Refresh()
		shareButton.Importance = widget.MediumImportance
		shareButton.Refresh()

		ui.addUserContent.Objects = []fyne.CanvasObject{ui.scanUser}
		ui.addUserContent.Refresh()
		if !fyne.CurrentDevice().IsMobile() {
			ui.mainWindow.Canvas().Focus(ui.addUserWidgets.entry)
		}
	})
	scanButton.Importance = widget.MediumImportance
	header := container.NewVBox(
		closeBar,
		container.NewCenter(container.NewHBox(
			shareButton,
			scanButton,
		)),
	)
	ui.addUserContent = container.NewStack()
	ui.addUser = container.New(
		layout.NewBorderLayout(header, nil, nil, nil),
		header,
		ui.addUserContent,
	)
}

func (ui *ui) buildScanUser() {
	ui.addUserWidgets = &addUserWidgets{
		currentStep: widget.NewLabel(""),
		progressBar: widget.NewProgressBarInfinite(),
		entry:       widget.NewEntry(),
	}

	ui.addUserWidgets.entry.OnSubmitted = func(str string) {
		ui.addUserWidgets.progressBar.Start()
		ui.addUserWidgets.progressBar.Show()
		go func() {
			if ui.networkState == networkStateStarting || ui.networkState == networkStateOffline {
				fyne.Do(func() {
					ui.addUserWidgets.currentStep.Text = "Waiting for network..."
					ui.addUserWidgets.currentStep.Refresh()
					ui.addUserWidgets.currentStep.Show()
				})
				for {
					time.Sleep(500 * time.Millisecond) // TODO: use a callback for this?
					if ui.networkState == networkStateOnline {
						break
					}
				}
			}

			ui.addUserWidgets.currentStep.Text = "Sending friend request..."
			ui.addUserWidgets.currentStep.Refresh()
			ui.addUserWidgets.currentStep.Show()
			err := ui.bounce.RequestToAddUser(strings.TrimSpace(str))
			fyne.Do(func() {
				if err != nil {
					ui.addUserWidgets.entry.Text = ""
					ui.scanUser.Refresh()
					ui.showDialog(dialog.NewError(errors.New("Error sending friend request: "+err.Error()), ui.mainWindow), nil)
				} else {
					ui.addUserWidgets.currentStep.Text = "Waiting for response..."
					ui.addUserWidgets.currentStep.Refresh()
					ui.addUser.Refresh()
				}
			})
		}()
	}
	ui.addUserWidgets.entry.ActionItem = widget.NewButtonWithIcon("", theme.MediaPhotoIcon(), func() {})

	importSelector := dialog.NewFileOpen(func(handler fyne.URIReadCloser, err error) {
		if handler == nil {
			// Selection canceled
			return
		}
		data, err := io.ReadAll(handler)
		if err != nil {
			ui.showDialog(dialog.NewError(errors.New("error reading file: "+err.Error()), ui.mainWindow), nil)
			return
		}
		newUser, err := ui.bounce.ImportUser(data)
		if err != nil {
			ui.showDialog(dialog.NewError(errors.New("error importing user: "+err.Error()), ui.mainWindow), nil)
			return
		}

		// TODO: add this user to the store as a pending user, waiting on confirmation
		ui.showDialog(dialog.NewInformation("Success", fmt.Sprintf("%s has been imported", newUser.Name), ui.mainWindow), nil)
		uName := binding.NewString()
		uName.Set(newUser.Name)
		ui.users.add(&user{id: newUser.ID, name: uName})
	}, ui.mainWindow)

	ui.scanUser = container.NewVBox(
		widget.NewLabel("Paste in the string or scan the QR code to add friend"),
		ui.addUserWidgets.entry,
		ui.addUserWidgets.currentStep,
		ui.addUserWidgets.progressBar,
		widget.NewAccordion(
			&widget.AccordionItem{
				Title: "Advanced Options",
				Detail: container.NewVBox(
					widget.NewButton("Import User", func() { importSelector.Show() }), // We don't use showDialog here because file selector is native on mobile
				),
			},
		),
	)
}

func (ui *ui) buildDisplayAddUserString() {
	title := widget.NewLabel("Scan or paste this on your friend's device:")

	ui.addUserQRCode = &canvas.Image{
		FillMode: canvas.ImageFillContain,
	}
	ui.addUserQRCode.SetMinSize(fyne.Size{Height: 256, Width: 256})

	ui.addUserStringEntry = widget.NewEntry()
	ui.addUserStringEntry.ActionItem = widget.NewButtonWithIcon("", theme.ContentCopyIcon(), func() { ui.mainWindow.Clipboard().SetContent(ui.addUserStringEntry.Text) })

	exportLabel := widget.NewLabel("Share File")
	exportLabel.TextStyle = fyne.TextStyle{Bold: true}

	exportNameEntry := widget.NewEntry()
	exportExpirationSelect := widget.NewSelect(expirationSelections, nil)
	exportExpirationSelect.Selected = expirationOneHour.display
	oneTimeUse := widget.NewCheck("One-Time Use", nil)
	oneTimeUse.Checked = true

	exportContactForm := widget.NewForm(
		&widget.FormItem{
			Text:   "Name:",
			Widget: exportNameEntry,
		},
		&widget.FormItem{
			Text:   "Expires:",
			Widget: exportExpirationSelect,
		},
		&widget.FormItem{
			Text:   "",
			Widget: oneTimeUse,
		},
	)
	exportContactForm.SubmitText = "Export"

	var showError error
	var showInformation dialog.Dialog
	selectorCleanup := func() {
		if showInformation != nil {
			ui.showDialog(showInformation, nil)
		}
		if showError != nil {
			ui.showDialog(dialog.NewError(showError, ui.mainWindow), nil)
		}
	}
	fileSelector := dialog.NewFileSave(func(handler fyne.URIWriteCloser, err error) {
		showError = nil
		showInformation = nil
		if err != nil {
			showError = errors.New("error opening file: " + err.Error())
			return
		}
		if handler == nil {
			return
		}

		expiration := int64(0)
		if exportExpirationSelect.Selected != expirationNever.display {
			increment, ok := expirationIncrements[exportExpirationSelect.Selected]
			if !ok {
				log.WithFields(log.Fields{
					"selection": exportExpirationSelect.Selected,
				}).Fatal("unsupported selection for export expiration")
			}
			expiration = time.Now().Unix() + increment
		}

		profileData := ui.bounce.ExportContact(exportNameEntry.Text, expiration, oneTimeUse.Checked)
		_, err = handler.Write(profileData)
		if err != nil {
			showError = errors.New("error writing file: " + err.Error())
			return
		}
		err = handler.Close()
		if err != nil {
			showError = errors.New("error closing file: " + err.Error())
			return
		}

		showInformation = dialog.NewInformation("Success", "Contact export written to "+handler.URI().Name(), ui.mainWindow)
	}, ui.mainWindow)

	exportContactForm.OnSubmit = func() {
		if exportNameEntry.Text == "" {
			ui.showDialog(dialog.NewError(errors.New("Please select a name for this export"), ui.mainWindow), nil)
			return
		}
		fileSelector.SetFileName("profile.bounce") // TODO: set the user's current profile name
		ui.showDialog(fileSelector, selectorCleanup)
	}

	exportContactMenu := container.NewVBox(
		exportLabel,
		exportContactForm,
	)

	ui.displayAddUserString = container.New(
		layout.NewBorderLayout(title, nil, nil, nil),
		title,
		container.NewVBox(
			ui.addUserQRCode,
			ui.addUserStringEntry,
			widget.NewAccordion(
				&widget.AccordionItem{
					Title: "Advanced Options",
					Detail: container.NewVBox(
						exportContactMenu,
					),
				},
			),
		),
	)
}

func (ui *ui) UserAdded(u chat.User) {
	ui.addUserWidgets.currentStep.Text = "Success!"
	ui.addUserWidgets.currentStep.Refresh()
	ui.addUserWidgets.progressBar.Stop()
	ui.addUserWidgets.progressBar.Hide()
	newUser := makeUser(u.ID, u.Name)
	ui.users.add(newUser)
	if ui.currentView == viewTypeAddUser {
		if fyne.CurrentDevice().IsMobile() {
			ui.mobileBack()
		} else {
			ui.showMainContainer()
		}
	}
	if !ui.initialSyncIncomplete {
		fyne.DoAndWait(func() {
			ui.NewDirectMessage(u)
			ui.showDialog(dialog.NewInformation("New contact added", u.Name+" was added as a friend", ui.mainWindow), nil)
		})
	}
}

func (ui *ui) AddUserRequestRejected(peer string) {
	fyne.DoAndWait(func() {
		ui.showDialog(dialog.NewError(errors.New("Friend request rejected, make sure you scan the device quickly"), ui.mainWindow), nil)
	})
}
