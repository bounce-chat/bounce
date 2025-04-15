package ui

import (
	"strings"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/data/binding"
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
	log "github.com/sirupsen/logrus"
)

type user struct {
	id       uuid.UUID
	name     binding.String
	initials binding.String
}

func makeUser(id uuid.UUID, name string) *user {
	u := &user{
		id:       id,
		name:     binding.NewString(),
		initials: binding.NewString(),
	}

	u.name.Set(name)
	u.setInitials() // TODO: required for chat bubble icons to have initials
	u.name.AddListener(binding.NewDataListener(func() {
		u.setInitials()
	}))

	return u
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

func (u user) getInitials() string {
	str, err := u.initials.Get()
	if err != nil {
		log.WithFields(log.Fields{
			"user_id": u.id,
			"error":   err.Error(),
		}).Fatal("data bindings broken for user initials")
	}
	return str
}

func (u user) setInitials() {
	name := u.getName()
	parts := strings.Split(name, " ")
	if len(parts) == 1 {
		r, n := utf8.DecodeRuneInString(parts[0])
		if r == utf8.RuneError {
			log.WithFields(log.Fields{
				"rune_error": r,
				"size":       n,
			}).Error("error setting user initials")
			return
		}
		u.initials.Set(string(r))
	} else {
		firstRune, n := utf8.DecodeRuneInString(parts[0])
		if firstRune == utf8.RuneError {
			log.WithFields(log.Fields{
				"rune_error": firstRune,
				"size":       n,
			}).Error("error setting user initials")
			return
		}

		lastRune, n := utf8.DecodeRuneInString(parts[len(parts)-1])
		if lastRune == utf8.RuneError {
			log.WithFields(log.Fields{
				"rune_error": lastRune,
				"size":       n,
			}).Error("error setting user initials")
			return
		}

		u.initials.Set(string(firstRune) + string(lastRune))
	}
}

func (fyneUI *Fyne) SetUserName(userID uuid.UUID, name string) {
	fyne.Do(func() {
		u, ok := fyneUI.users.get(userID)
		if !ok {
			log.WithFields(log.Fields{
				"user_id": userID,
			}).Error("cannot set name of unknown user")
			return
		}

		u.name.Set(name)

		fyneUI.messages.renameUser(userID, u.getName(), u.getInitials())
		dm, ok := fyneUI.dms[userID]
		if ok {
			dm.chatHistoryScroll().Refresh()
		}
		for _, g := range fyneUI.groups {
			if g.users.contains(userID) {
				g.chatHistoryScroll().Refresh()
			}
		}
	})
}

func (fyneUI *Fyne) UserNameUpdated(uuun chat.UpdateUserUpdateName) {
	fyne.Do(func() {
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
	})
}

func (fyneUI *Fyne) UserOnline(userID uuid.UUID) {
	fyne.Do(func() {
		log.WithFields(log.Fields{
			"user_id": userID,
		}).Info("user is online")
	})
}

func (fyneUI *Fyne) UserOffline(userID uuid.UUID) {
	fyne.Do(func() {
		log.WithFields(log.Fields{
			"user_id": userID,
		}).Info("user is offline")
	})
}
