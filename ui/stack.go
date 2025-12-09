package ui

import (
	"bytes"
	"image"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/mobile"
	"github.com/google/uuid"
	"github.com/rymdport/go-qrcode"
	log "github.com/sirupsen/logrus"
)

const (
	viewTypeAllThreads = iota
	viewTypeThread
	viewTypeDMSettings
	viewTypeGroupSettings
	viewTypeSettings
	viewTypeNewSyncDevice
	viewTypeNewInstall
	viewTypeProfileCreator
	viewTypeNewGroup
	viewTypeNewDM
	viewTypeMenu
	viewTypeDisplaySyncString
	viewTypeAddUser
	viewTypeAbout
	viewTypeEditProfile
	viewTypeNameNewDevice
	viewTypeDialog
	viewTypeImageViewer
)

var hookedDialogs = make(map[dialog.Dialog]bool)

type view struct {
	viewType int
	context  uuid.UUID
	dialog   dialog.Dialog
}

type dialogWithCallback struct {
	dialog   dialog.Dialog
	callback func()
}

func (ui *ui) mobileBack() {
	// If there's only one view left in the history, we're at the beginning and should close the app
	if len(ui.state.viewStack) == 1 {
		if drv, ok := ui.app.Driver().(mobile.Driver); ok {
			drv.(mobile.Driver).GoBack()
		}
		return
	}

	// If the current view is a dialog, hide that dialog and run any cleanup tasks required
	currentView := ui.state.viewStack[len(ui.state.viewStack)-1]
	if currentView.viewType == viewTypeDialog {
		if currentView.dialog != nil {
			// view stack is popped and cleanup is ran in dialog closed callback
			currentView.dialog.Hide()
			return
		} else {
			log.Warn("view type dialog in view stack is missing dialog")
		}
		return
	}

	// Grab the view that occured before the current one as the one we want to display
	displayView := ui.state.viewStack[len(ui.state.viewStack)-2]

	// Remove the current view from history
	ui.state.viewStack = ui.state.viewStack[0 : len(ui.state.viewStack)-1]

	// Set the UI to the view we want to display
	switch displayView.viewType {
	case viewTypeAllThreads:
		ui.window.SetContent(ui.views.main)
	case viewTypeThread:
		t, ok := ui.threads.get(displayView.context)
		if !ok {
			log.WithFields(log.Fields{
				"type": displayView.viewType,
			}).Warn("cannot display unknown thread when handling mobile back button")
			ui.window.SetContent(ui.views.main)
			ui.views.main.Show()
		} else {
			ui.displayThread(t)
		}
	case viewTypeSettings:
		ui.window.SetContent(ui.views.settings)
	case viewTypeNewSyncDevice:
		ui.widgets.newSyncDevice.syncStringInput.Show()
		ui.window.SetContent(ui.views.newSyncDevice)
	case viewTypeNewInstall:
		ui.window.SetContent(ui.views.newInstall)
	case viewTypeProfileCreator:
		ui.window.SetContent(ui.views.newProfileCreator)
	case viewTypeNewGroup:
		ui.clearNewGroupSelectors()
		ui.window.SetContent(ui.views.newGroup)
	case viewTypeNewDM:
		ui.widgets.newDM.searchEntry.Text = ""
		ui.widgets.newDM.searchEntry.Refresh()
		ui.refreshAllUsersDMLinks()
		ui.window.SetContent(ui.views.newDM)
	case viewTypeMenu:
		ui.window.SetContent(ui.views.mobileMenu)
	case viewTypeDisplaySyncString:
		newSyncString := ui.bounce.GetNewSyncString()
		ui.widgets.addSyncDevice.entry.SetText(newSyncString)
		qrData, err := qrcode.Encode(newSyncString, qrcode.Medium, 256)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error QR encoding add device string")
		} else {
			qrImg, _, err := image.Decode(bytes.NewReader(qrData))
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error decoding QR data as image")
			} else {
				ui.widgets.addSyncDevice.qrCode.Image = qrImg
				ui.widgets.addSyncDevice.qrCode.Refresh()
			}
		}
		ui.window.SetContent(ui.views.addSyncDevice)
	case viewTypeAddUser:
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
		ui.window.SetContent(ui.views.addUser)
	case viewTypeAbout:
		ui.window.SetContent(ui.views.about)
	case viewTypeEditProfile:
		ui.widgets.editProfile.profileIcon.id = ui.state.profile.id
		ui.widgets.editProfile.profileIcon.images = ui.state.profile.images
		ui.widgets.editProfile.profileIcon.foregroundText.Text = ui.state.profile.initials
		ui.widgets.editProfile.profileIcon.Refresh()

		ui.widgets.editProfile.profileNameEntry.Text = ui.state.profile.getDisplayName()
		ui.widgets.editProfile.profileNameEntry.Refresh()
		ui.widgets.editProfile.profileNameEntry.FocusLost()

		ui.containers.profileOptions.Refresh()
		ui.window.SetContent(ui.views.editProfile)
	case viewTypeNameNewDevice:
		ui.window.SetContent(ui.views.nameNewDevice)
	case viewTypeImageViewer:
		ui.window.SetContent(ui.widgets.imageViewer.viewer)
	default:
		log.WithFields(log.Fields{
			"type": displayView.viewType,
		}).Warn("cannot display unknown view type when handling mobile back button")
		if drv, ok := ui.app.Driver().(mobile.Driver); ok {
			drv.(mobile.Driver).GoBack()
		}
	}

	// If we're not looking at a thread, unset the active thread
	if displayView.viewType != viewTypeThread {
		ui.state.activeThread = uuid.Nil
	}
}

func (ui *ui) showDialog(d dialog.Dialog, cleanup func()) {
	if _, hooked := hookedDialogs[d]; !hooked {
		d.SetOnClosed(func() {
			if fyne.CurrentDevice().IsMobile() {
				if len(ui.state.viewStack) == 1 {
					log.Warn("view stack has one member when hiding dialog")
					return
				}
				ui.state.viewStack = ui.state.viewStack[0 : len(ui.state.viewStack)-1]
			}
			if cleanup != nil {
				cleanup()
			}
		})
		hookedDialogs[d] = true
	}
	d.Show()
	if fyne.CurrentDevice().IsMobile() {
		ui.state.viewStack = append(
			ui.state.viewStack,
			view{
				viewType: viewTypeDialog,
				dialog:   d,
			},
		)
	}
}
