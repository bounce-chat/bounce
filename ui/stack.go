package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/mobile"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

const viewTypeAllThreads = 0
const viewTypeThread = 1
const viewTypeDMSettings = 2
const viewTypeGroupSettings = 3
const viewTypeSettings = 4
const viewTypeNewSyncDevice = 5
const viewTypeNewInstall = 6
const viewTypeProfileCreator = 7
const viewTypeNewGroup = 8
const viewTypeNewDM = 9
const viewTypeMenu = 10
const viewTypeImportContact = 11
const viewTypeDisplaySyncString = 12
const viewTypeDisplayAddUserString = 13
const viewTypeAddUser = 14
const viewTypeAbout = 15
const viewTypeEditProfile = 16
const viewTypeNameNewDevice = 17
const viewTypeDialog = 18

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
	if len(ui.viewStack) == 1 {
		if drv, ok := ui.app.Driver().(mobile.Driver); ok {
			drv.(mobile.Driver).GoBack()
		}
		return
	}

	// If the current view is a dialog, hide that dialog and run any cleanup tasks required
	currentView := ui.viewStack[len(ui.viewStack)-1]
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
	displayView := ui.viewStack[len(ui.viewStack)-2]

	// Remove the current view from history
	ui.viewStack = ui.viewStack[0 : len(ui.viewStack)-1]

	// Set the UI to the view we want to display
	switch displayView.viewType {
	case viewTypeAllThreads:
		ui.mainWindow.SetContent(ui.mainContainer)
		ui.mainContainer.Show()
	case viewTypeThread:
		t, ok := ui.getThread(displayView.context)
		if !ok {
			log.WithFields(log.Fields{
				"type": displayView.viewType,
			}).Warn("cannot display unknown thread when handling mobile back button")
			ui.mainWindow.SetContent(ui.mainContainer)
			ui.mainContainer.Show()
		} else {
			ui.displayThread(t)
		}
	case viewTypeSettings:
		ui.mainWindow.SetContent(ui.settingsContainer)
		ui.settingsContainer.Show()
	case viewTypeNewSyncDevice:
		ui.newSyncDeviceWidgets.syncStringInput.Show()
		ui.mainWindow.SetContent(ui.newSyncDevice)
		ui.newSyncDevice.Show()
	case viewTypeNewInstall:
		ui.mainWindow.SetContent(ui.newInstall)
		ui.newInstall.Show()
	case viewTypeProfileCreator:
		ui.mainWindow.SetContent(ui.newProfileCreator)
		ui.newProfileCreator.Show()
	case viewTypeNewGroup:
		ui.clearNewGroupSelectors()
		ui.mainWindow.SetContent(ui.newGroup)
		ui.newGroup.Show()
	case viewTypeNewDM:
		ui.newDMUserSearchEntry.Text = ""
		ui.newDMUserSearchEntry.Refresh()
		ui.refreshAllUsersDMLinks()
		ui.mainWindow.SetContent(ui.newDM)
		ui.newDM.Show()
	case viewTypeMenu:
		ui.mainWindow.SetContent(ui.mobileMenu)
		ui.mobileMenu.Show()
	case viewTypeImportContact:
		ui.mainWindow.SetContent(ui.importContact)
		ui.importContact.Show()
	case viewTypeDisplaySyncString:
		err := ui.syncString.Set(ui.bounce.GetNewSyncString())
		if err != nil {
			log.Fatal("data bindings are broken")
		}
		// TODO: update the QR code data
		ui.mainWindow.SetContent(ui.displaySyncString)
		ui.displaySyncString.Show()
	case viewTypeDisplayAddUserString:
		err := ui.addUserString.Set(ui.bounce.GetNewAddUserString())
		if err != nil {
			log.Fatal("data bindings are broken")
		}
		// TODO: update the QR code data
		ui.mainWindow.SetContent(ui.displayAddUserString)
		ui.displayAddUserString.Show()
	case viewTypeAddUser:
		ui.mainWindow.SetContent(ui.addUser)
		ui.addUser.Show()
	case viewTypeAbout:
		ui.mainWindow.SetContent(ui.about)
		ui.about.Show()
	case viewTypeEditProfile:
		ui.profileIcon.id = ui.profile.id
		ui.profileIcon.images = ui.profile.images
		initials, err := ui.profile.initials.Get()
		if err != nil {
			log.Fatal("data bindings are broken")
		}
		ui.profileIcon.foregroundText.Text = initials
		ui.profileIcon.Refresh()

		ui.profileNameEntry.Text = ui.profile.getName()
		ui.profileNameEntry.Refresh()
		ui.profileNameEntry.FocusLost()

		ui.profileOptions.Refresh()
		ui.mainWindow.SetContent(ui.editProfile)
		ui.editProfile.Show()
	case viewTypeNameNewDevice:
		ui.mainWindow.SetContent(ui.nameNewDevice)
		ui.nameNewDevice.Show()
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
		ui.activeThread = uuid.Nil
	}
}

func (ui *ui) showDialog(d dialog.Dialog, cleanup func()) {
	if _, hooked := hookedDialogs[d]; !hooked {
		d.SetOnClosed(func() {
			if fyne.CurrentDevice().IsMobile() {
				if len(ui.viewStack) == 1 {
					log.Warn("view stack has one member when hiding dialog")
					return
				}
				ui.viewStack = ui.viewStack[0 : len(ui.viewStack)-1]
			}
			if cleanup != nil {
				cleanup()
			}
		})
		hookedDialogs[d] = true
	}
	d.Show()
	if fyne.CurrentDevice().IsMobile() {
		ui.viewStack = append(
			ui.viewStack,
			view{
				viewType: viewTypeDialog,
				dialog:   d,
			},
		)
	}
}
