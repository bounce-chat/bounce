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
	images   []uuid.UUID
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

func (fyneUI *Fyne) SetUserState(chatUser chat.User) {
	u, ok := fyneUI.users.get(chatUser.ID)
	if !ok {
		log.WithFields(log.Fields{
			"user_id": chatUser.ID,
		}).Error("cannot set state of unknown user")
		return
	}

	// Rename the user
	u.name.Set(chatUser.Name)

	fyneUI.messages.renameUser(chatUser.ID, u.getName(), u.getInitials())
	dm, ok := fyneUI.dms[chatUser.ID]
	if ok {
		fyne.Do(func() { dm.chatHistoryScroll().Refresh() })
	}
	for _, g := range fyneUI.groups {
		if g.users.contains(chatUser.ID) {
			fyne.Do(func() { g.chatHistoryScroll().Refresh() })
		}
	}

	// Re-add user to any user stores they are in, in order to regenerate ngram search tokens
	allUserStoresMutex.Lock()
	for _, us := range allUserStores {
		if us.contains(chatUser.ID) {
			us.remove(chatUser.ID)
			us.add(u)
		}
	}
	allUserStoresMutex.Unlock()

	// Set the images
	if chatUser.ID == fyneUI.profile.id {
		fyneUI.profileIcon.Objects[0].(*defaultImage).images = fyneUI.profile.images
		fyne.Do(func() { fyneUI.profileIcon.Refresh() })
	}

	// Update the chat bubbles that have an icon
	fyneUI.messages.updateUserImage(chatUser.ID, chatUser.Images)
	if chatUser.ID != fyneUI.profile.id {
		for _, g := range fyneUI.groups {
			if g.users.contains(chatUser.ID) {
				fyne.Do(func() { g.chatHistoryScroll().Refresh() })
			}
		}
	}

	// Update the images if there is a DM open
	dm, exists := fyneUI.dms[chatUser.ID]
	if !exists {
		return
	}

	dm.user.images = chatUser.Images
	dm.editIcon.images = chatUser.Images
	dm.headerIcon.images = chatUser.Images
	dm.button.threadImage.images = chatUser.Images
	fyne.Do(func() {
		dm.editIcon.Refresh()
		dm.headerIcon.Refresh()
		dm.button.threadImage.Refresh()
	})
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
				fyne.Do(func() { fyneUI.appendThreadItem(dm, ti) })
			}
		}
		for _, g := range fyneUI.groups {
			ti, err := fyneUI.userChangedName(uuun.ID, uuun.User, uuun.OldName, uuun.Name, uuun.Timestamp)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error creating thread item for user name change")
			} else {
				fyne.Do(func() { fyneUI.appendThreadItem(g, ti) })
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
				fyne.Do(func() { fyneUI.appendThreadItem(dm, ti) })
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
					fyne.Do(func() { fyneUI.appendThreadItem(g, ti) })
				}
			}
		}
	}
}

func (fyneUI *Fyne) UserImageUpdated(uuui chat.UpdateUserUpdateImage) {
	if uuui.User == fyneUI.profile.id {
		for _, dm := range fyneUI.dms {
			ti, err := fyneUI.userChangedImage(uuui.ID, uuui.User, uuui.Timestamp)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error creating thread item for user image change")
			} else {
				fyne.Do(func() { fyneUI.appendThreadItem(dm, ti) })
			}
		}
		for _, g := range fyneUI.groups {
			ti, err := fyneUI.userChangedImage(uuui.ID, uuui.User, uuui.Timestamp)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error creating thread item for user image change")
			} else {
				fyne.Do(func() { fyneUI.appendThreadItem(g, ti) })
			}
		}
	} else {
		dm, ok := fyneUI.dms[uuui.User]
		if ok {
			ti, err := fyneUI.userChangedImage(uuui.ID, uuui.User, uuui.Timestamp)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error creating thread item for user image change")
			} else {
				fyne.Do(func() { fyneUI.appendThreadItem(dm, ti) })
			}
		}

		for _, g := range fyneUI.groups {
			if g.users.contains(uuui.User) {
				ti, err := fyneUI.userChangedImage(uuui.ID, uuui.User, uuui.Timestamp)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error creating thread item for user image change")
				} else {
					fyne.Do(func() { fyneUI.appendThreadItem(g, ti) })
				}
			}
		}
	}
}

func (fyneUI *Fyne) UserOnline(userID uuid.UUID) {
	fyne.DoAndWait(func() {
		log.WithFields(log.Fields{
			"user_id": userID,
		}).Info("user is online")
	})
}

func (fyneUI *Fyne) UserOffline(userID uuid.UUID) {
	fyne.DoAndWait(func() {
		log.WithFields(log.Fields{
			"user_id": userID,
		}).Info("user is offline")
	})
}
