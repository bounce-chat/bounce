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

type addUser struct {
	currentStep  *widget.Label
	progressBar  *widget.ProgressBarInfinite
	scanEntry    *widget.Entry
	displayEntry *widget.Entry
	qrCode       *canvas.Image
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

	var shareButton *widget.Button
	var scanButton *widget.Button
	shareButton = widget.NewButton("Share", func() {
		shareButton.Importance = widget.HighImportance
		shareButton.Refresh()
		scanButton.Importance = widget.MediumImportance
		scanButton.Refresh()

		ui.containers.addUserContent.Objects = []fyne.CanvasObject{ui.containers.displayAddUserString}
		ui.containers.addUserContent.Refresh()
	})
	shareButton.Importance = widget.HighImportance
	scanButton = widget.NewButton("Scan", func() {
		scanButton.Importance = widget.HighImportance
		scanButton.Refresh()
		shareButton.Importance = widget.MediumImportance
		shareButton.Refresh()

		ui.containers.addUserContent.Objects = []fyne.CanvasObject{ui.containers.scanUser}
		ui.containers.addUserContent.Refresh()
		if !fyne.CurrentDevice().IsMobile() {
			ui.window.Canvas().Focus(ui.widgets.addUser.scanEntry)
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

	importSelector := dialog.NewFileOpen(func(handler fyne.URIReadCloser, err error) {
		if handler == nil {
			// Selection canceled
			return
		}
		data, err := io.ReadAll(handler)
		if err != nil {
			ui.showDialog(dialog.NewError(errors.New("error reading file: "+err.Error()), ui.window), nil)
			return
		}
		newUser, err := ui.bounce.ImportUser(data)
		if err != nil {
			ui.showDialog(dialog.NewError(errors.New("error importing user: "+err.Error()), ui.window), nil)
			return
		}

		// TODO: add this user to the store as a pending user, waiting on confirmation
		ui.showDialog(dialog.NewInformation("Success", fmt.Sprintf("%s has been imported", newUser.Name), ui.window), nil)
		uName := binding.NewString()
		uName.Set(newUser.Name)
		ui.users.add(&user{id: newUser.ID, name: uName})
	}, ui.window)

	ui.containers.scanUser = container.NewVBox(
		widget.NewLabel("Paste in the string or scan the QR code to add friend"),
		ui.widgets.addUser.scanEntry,
		ui.widgets.addUser.currentStep,
		ui.widgets.addUser.progressBar,
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

	ui.widgets.addUser.qrCode = &canvas.Image{
		FillMode: canvas.ImageFillContain,
	}
	ui.widgets.addUser.qrCode.SetMinSize(fyne.Size{Height: 256, Width: 256})

	ui.widgets.addUser.displayEntry = widget.NewEntry()
	ui.widgets.addUser.displayEntry.ActionItem = widget.NewButtonWithIcon("", theme.ContentCopyIcon(), func() { ui.window.Clipboard().SetContent(ui.widgets.addUser.displayEntry.Text) })

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
			ui.showDialog(dialog.NewError(showError, ui.window), nil)
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

		showInformation = dialog.NewInformation("Success", "Contact export written to "+handler.URI().Name(), ui.window)
	}, ui.window)

	exportContactForm.OnSubmit = func() {
		if exportNameEntry.Text == "" {
			ui.showDialog(dialog.NewError(errors.New("Please select a name for this export"), ui.window), nil)
			return
		}
		fileSelector.SetFileName("profile.bounce") // TODO: set the user's current profile name
		ui.showDialog(fileSelector, selectorCleanup)
	}

	exportContactMenu := container.NewVBox(
		exportLabel,
		exportContactForm,
	)

	ui.containers.displayAddUserString = container.New(
		layout.NewBorderLayout(title, nil, nil, nil),
		title,
		container.NewVBox(
			ui.widgets.addUser.qrCode,
			ui.widgets.addUser.displayEntry,
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
	ui.widgets.addUser.currentStep.Text = "Success!"
	ui.widgets.addUser.currentStep.Refresh()
	ui.widgets.addUser.progressBar.Stop()
	ui.widgets.addUser.progressBar.Hide()
	newUser := makeUser(u.ID, u.Name)
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
			ui.NewDirectMessage(u)
			ui.showDialog(dialog.NewInformation("New contact added", u.Name+" was added as a friend", ui.window), nil)
		})
	}
}

func (ui *ui) AddUserRequestRejected(peer string) {
	fyne.DoAndWait(func() {
		ui.showDialog(dialog.NewError(errors.New("Friend request rejected, make sure you scan the device quickly"), ui.window), nil)
	})
}
