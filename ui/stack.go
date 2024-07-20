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
	// TODO: if there's an open dialog, close it

	if len(fyneUI.viewStack) == 1 {
		if drv, ok := fyneUI.app.Driver().(mobile.Driver); ok {
			drv.(mobile.Driver).GoBack()
		}
		return
	}
	displayView := fyneUI.viewStack[len(fyneUI.viewStack)-2]
	fyneUI.viewStack = fyneUI.viewStack[0 : len(fyneUI.viewStack)-1]

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
