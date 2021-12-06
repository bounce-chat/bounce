package ui

import (
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

func (fyneUI *Fyne) SyncDeviceRequestAccepted(id uuid.UUID, name string) {
	log.Info("request to be added to an existing device group has been accepted")
	fyneUI.profile = &user{id: id, name: name}
	fyneUI.initialStateSet = true
	fyneUI.showMainContainer()
}

func (fyneUI *Fyne) SyncDeviceRequestRejected() {
	log.Info("request to be added to an existing device group has been rejected")
}
