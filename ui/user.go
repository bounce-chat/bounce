package ui

import (
	"fyne.io/fyne/v2/data/binding"
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
	log "github.com/sirupsen/logrus"
)

type user struct {
	id   uuid.UUID
	name binding.String
}

func (u user) getName() string {
	str, err := u.name.Get()
	if err != nil {
		log.WithFields(log.Fields{
			"user_id": u.id,
			"error":   err.Error(),
		}).Fatal("data bindings broken for user name")
	}
	return str
}

func (fyneUI *Fyne) SetUserName(userID uuid.UUID, name string) {
	u, ok := fyneUI.users.get(userID)
	if !ok {
		log.WithFields(log.Fields{
			"user_id": userID,
		}).Error("cannot set name of unknown user")
		return
	}

	u.name.Set(name)
}

func (fyneUI *Fyne) UserNameUpdated(uuun chat.UpdateUserUpdateName) {
	if uuun.User == fyneUI.profile.id {
		for _, dm := range fyneUI.dms {
			ti, err := fyneUI.userChangedName(uuun.ID, uuun.User, uuun.OldName, uuun.Name, uuun.Timestamp)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error creating thread item for user name change")
			} else {
				fyneUI.appendThreadItem(dm, ti)
			}
		}
		for _, g := range fyneUI.groups {
			ti, err := fyneUI.userChangedName(uuun.ID, uuun.User, uuun.OldName, uuun.Name, uuun.Timestamp)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error creating thread item for user name change")
			} else {
				fyneUI.appendThreadItem(g, ti)
			}
		}
	} else {
		dm, ok := fyneUI.dms[uuun.User]
		if ok {
			ti, err := fyneUI.userChangedName(uuun.ID, uuun.User, uuun.OldName, uuun.Name, uuun.Timestamp)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error creating thread item for user name change")
			} else {
				fyneUI.appendThreadItem(dm, ti)
			}
		}

		for _, g := range fyneUI.groups {
			if g.users.contains(uuun.User) {
				ti, err := fyneUI.userChangedName(uuun.ID, uuun.User, uuun.OldName, uuun.Name, uuun.Timestamp)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error creating thread item for user name change")
				} else {
					fyneUI.appendThreadItem(g, ti)
				}
			}
		}
	}
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
