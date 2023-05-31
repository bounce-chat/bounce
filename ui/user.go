package ui

import (
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

type user struct {
	id   uuid.UUID
	name string // TODO: bind this, support update functions
}

func (fyneUI *Fyne) UserIsOnline(userID uuid.UUID) {
	log.WithFields(log.Fields{
		"user_id": userID,
	}).Info("user is online")
}

func (fyneUI *Fyne) UserIsOffline(userID uuid.UUID) {
	log.WithFields(log.Fields{
		"user_id": userID,
	}).Info("user is offline")
}
