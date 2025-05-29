package ui

import (
	"io"

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

func (fyneUI *Fyne) mobileBack() {
	// If there's only one view left in the history, we're at the beginning and should close the app
	if len(fyneUI.viewStack) == 1 {
		if drv, ok := fyneUI.app.Driver().(mobile.Driver); ok {
			drv.(mobile.Driver).GoBack()
		}
		return
	}

	// If the current view is a dialog, hide that dialog and run any cleanup tasks required
	currentView := fyneUI.viewStack[len(fyneUI.viewStack)-1]
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
	displayView := fyneUI.viewStack[len(fyneUI.viewStack)-2]

	// Remove the current view from history
	fyneUI.viewStack = fyneUI.viewStack[0 : len(fyneUI.viewStack)-1]

	// Set the UI to the view we want to display
	switch displayView.viewType {
	case viewTypeAllThreads:
		fyneUI.mainWindow.SetContent(fyneUI.mainContainer)
		fyneUI.mainContainer.Show()
	case viewTypeThread:
		t, ok := fyneUI.getThread(displayView.context)
		if !ok {
			log.WithFields(log.Fields{
				"type": displayView.viewType,
			}).Warn("cannot display unknown thread when handling mobile back button")
			fyneUI.mainWindow.SetContent(fyneUI.mainContainer)
			fyneUI.mainContainer.Show()
		} else {
			fyneUI.displayThread(t)
		}
	case viewTypeSettings:
		fyneUI.mainWindow.SetContent(fyneUI.settingsContainer)
		fyneUI.settingsContainer.Show()
	case viewTypeNewSyncDevice:
		fyneUI.newSyncDeviceWidgets.syncStringInput.Show()
		fyneUI.mainWindow.SetContent(fyneUI.newSyncDevice)
		fyneUI.newSyncDevice.Show()
	case viewTypeNewInstall:
		fyneUI.mainWindow.SetContent(fyneUI.newInstall)
		fyneUI.newInstall.Show()
	case viewTypeProfileCreator:
		fyneUI.mainWindow.SetContent(fyneUI.newProfileCreator)
		fyneUI.newProfileCreator.Show()
	case viewTypeNewGroup:
		fyneUI.clearNewGroupSelectors()
		fyneUI.mainWindow.SetContent(fyneUI.newGroup)
		fyneUI.newGroup.Show()
	case viewTypeNewDM:
		fyneUI.newDMUserSearchEntry.Text = ""
		fyneUI.newDMUserSearchEntry.Refresh()
		fyneUI.refreshAllUsersDMLinks()
		fyneUI.mainWindow.SetContent(fyneUI.newDM)
		fyneUI.newDM.Show()
	case viewTypeMenu:
		fyneUI.mainWindow.SetContent(fyneUI.mobileMenu)
		fyneUI.mobileMenu.Show()
	case viewTypeImportContact:
		fyneUI.mainWindow.SetContent(fyneUI.importContact)
		fyneUI.importContact.Show()
	case viewTypeDisplaySyncString:
		err := fyneUI.syncString.Set(fyneUI.callbacks.GetNewSyncString())
		if err != nil {
			log.Fatal("data bindings are broken")
		}
		// TODO: update the QR code data
		fyneUI.mainWindow.SetContent(fyneUI.displaySyncString)
		fyneUI.displaySyncString.Show()
	case viewTypeDisplayAddUserString:
		err := fyneUI.addUserString.Set(fyneUI.callbacks.GetNewAddUserString())
		if err != nil {
			log.Fatal("data bindings are broken")
		}
		// TODO: update the QR code data
		fyneUI.mainWindow.SetContent(fyneUI.displayAddUserString)
		fyneUI.displayAddUserString.Show()
	case viewTypeAddUser:
		fyneUI.mainWindow.SetContent(fyneUI.addUser)
		fyneUI.addUser.Show()
	case viewTypeAbout:
		fyneUI.mainWindow.SetContent(fyneUI.about)
		fyneUI.about.Show()
	case viewTypeEditProfile:
		profileImage := newDefaultImage(fyneUI.profile.id, fyneUI.profile.initials, 128, fyneUI.callbacks.GetFileData, func() {
			dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
				if reader == nil {
					return
				}

				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Debug("error selecting new profile image")
					return
				}

				data, err := io.ReadAll(reader)
				reader.Close()
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Debug("error reading new profile image")
					return
				}

				// TODO: make sure data is a valid image, allow for editing, etc
				// TODO: don't actualy set it until they hit save?

				fyneUI.callbacks.UpdateProfileImage(data)
			}, fyneUI.mainWindow).Show() // We do not use showDialog here because on mobile this uses a native intent
		})
		profileImage.images = fyneUI.profile.images
		fyneUI.profileIcon.Objects = []fyne.CanvasObject{profileImage}
		fyneUI.profileIcon.Refresh()

		fyneUI.profileNameEntry.Text = fyneUI.profile.getName()
		fyneUI.profileNameEntry.Refresh()
		fyneUI.profileNameEntry.FocusLost()

		fyneUI.profileOptions.Refresh()
		fyneUI.mainWindow.SetContent(fyneUI.editProfile)
		fyneUI.editProfile.Show()
	case viewTypeNameNewDevice:
		fyneUI.mainWindow.SetContent(fyneUI.nameNewDevice)
		fyneUI.nameNewDevice.Show()
	default:
		log.WithFields(log.Fields{
			"type": displayView.viewType,
		}).Warn("cannot display unknown view type when handling mobile back button")
		if drv, ok := fyneUI.app.Driver().(mobile.Driver); ok {
			drv.(mobile.Driver).GoBack()
		}
	}

	// If we're not looking at a thread, unset the active thread
	if displayView.viewType != viewTypeThread {
		fyneUI.activeThread = uuid.Nil
	}
}

func (fyneUI *Fyne) showDialog(d dialog.Dialog, cleanup func()) {
	if _, hooked := hookedDialogs[d]; !hooked {
		d.SetOnClosed(func() {
			if fyne.CurrentDevice().IsMobile() {
				if len(fyneUI.viewStack) == 1 {
					log.Warn("view stack has one member when hiding dialog")
					return
				}
				fyneUI.viewStack = fyneUI.viewStack[0 : len(fyneUI.viewStack)-1]
			}
			if cleanup != nil {
				cleanup()
			}
		})
		hookedDialogs[d] = true
	}
	d.Show()
	if fyne.CurrentDevice().IsMobile() {
		fyneUI.viewStack = append(
			fyneUI.viewStack,
			view{
				viewType: viewTypeDialog,
				dialog:   d,
			},
		)
	}
}
