package ui

import (
	"errors"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/hkparker/bounce/chat"
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

	ui.addUserWidgets.entry.Text = ""
	ui.addUserWidgets.entry.Refresh()
	ui.addUserWidgets.progressBar.Hide()
	ui.addUserWidgets.currentStep.Text = ""
	ui.addUserWidgets.currentStep.Refresh()
	ui.addUserWidgets.currentStep.Hide()

	ui.addUser.Show()
	ui.mainWindow.SetContent(ui.addUser)

	ui.mainWindow.Canvas().Focus(ui.addUserWidgets.entry)
}

func (ui *ui) buildAddUser() {
	ui.addUserWidgets = &addUserWidgets{
		currentStep: widget.NewLabel(""),
		progressBar: widget.NewProgressBarInfinite(),
		entry:       widget.NewEntry(),
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
					ui.addUser.Refresh()
					ui.showDialog(dialog.NewError(errors.New("Error sending friend request: "+err.Error()), ui.mainWindow), nil)
				} else {
					ui.addUserWidgets.currentStep.Text = "Waiting for response..."
					ui.addUserWidgets.currentStep.Refresh()
					ui.addUser.Refresh()
				}
			})
		}()
	}

	ui.addUser = container.New(
		layout.NewBorderLayout(closeBar, nil, nil, nil),
		closeBar,
		container.NewVBox(
			widget.NewLabel("Paste in the string or scan the QR code to add friend"),
			ui.addUserWidgets.entry,
			ui.addUserWidgets.currentStep,
			ui.addUserWidgets.progressBar,
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
	//ui.showMainContainer() // TODO: only if still showing the add user container, and actually go to the last screen
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
