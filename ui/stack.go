package ui

import (
	"fyne.io/fyne/v2/driver/mobile"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

const viewTypeAllThreads = 0
const viewTypeThread = 1
const viewTypeDMSettings = 2
const viewTypeGroupSettings = 3

type view struct {
	viewType int
	context  uuid.UUID
}

func (fyneUI *Fyne) mobileBack() {
	// If there is an open dialog, the back button should close it, and reset any changes
	if fyneUI.activeDialog != nil {
		fyneUI.activeDialog.Hide()
		fyneUI.activeDialog = nil
		if fyneUI.activeDialogCleanup != nil {
			fyneUI.activeDialogCleanup()
			fyneUI.activeDialogCleanup = nil
		}
		return
	}

	// If there's only one view left in the history, we're at the beginning and should close the app
	if len(fyneUI.viewStack) == 1 {
		if drv, ok := fyneUI.app.Driver().(mobile.Driver); ok {
			drv.(mobile.Driver).GoBack()
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
	default:
		log.WithFields(log.Fields{
			"type": displayView.viewType,
		}).Warn("cannot display unknown view type when handling mobile back button")
		if drv, ok := fyneUI.app.Driver().(mobile.Driver); ok {
			drv.(mobile.Driver).GoBack()
		}
	}
}
