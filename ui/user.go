package ui

import (
	"strings"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
	log "github.com/sirupsen/logrus"
)

type user struct {
	id               uuid.UUID
	name             string
	alias            string
	initials         string
	images           []uuid.UUID
	introductionTime int64
}

func makeUser(id uuid.UUID, name string) *user {
	u := &user{
		id:   id,
		name: name,
	}
	u.setInitials()

	return u
}

func (u *user) getDisplayName() string {
	if len(u.alias) > 0 {
		return u.alias
	}
	return u.name
}

func (u *user) setInitials() {
	parts := strings.Split(u.getDisplayName(), " ")
	if len(parts) == 1 {
		r, n := utf8.DecodeRuneInString(parts[0])
		if r == utf8.RuneError {
			log.WithFields(log.Fields{
				"rune_error": r,
				"size":       n,
			}).Error("error setting user initials")
			return
		}
		u.initials = string(r)
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

		u.initials = string(firstRune) + string(lastRune)
	}
}

func (ui *ui) SetUserState(chatUser chat.User) {
	u, ok := ui.users.get(chatUser.ID)
	if !ok {
		log.WithFields(log.Fields{
			"user_id": chatUser.ID,
		}).Error("cannot set state of unknown user")
		return
	}

	// Rename the user
	u.name = chatUser.Name
	u.alias = chatUser.Alias
	u.setInitials()

	ui.messages.renameUser(chatUser.ID, u.getDisplayName(), u.initials)

	dm, ok := ui.threads.getDM(chatUser.ID)
	if ok {
		fyne.DoAndWait(func() {
			dm.user.images = chatUser.Images
			dm.editIcon.images = chatUser.Images
			dm.headerIcon.images = chatUser.Images
			dm.button.threadImage.images = chatUser.Images

			dm.username.Text = u.getDisplayName()
			dm.username.Refresh()
			dm.headerUsername.Text = u.getDisplayName()
			dm.headerUsername.Refresh()

			if len(u.alias) > 0 && u.alias != u.name {
				dm.realName.Show()
			} else {
				dm.realName.Hide()
			}

			dm.headerIcon.setString(u.initials)
			dm.editIcon.setString(u.initials)
			dm.button.setName(u.getDisplayName(), u.initials)

			dm.chatHistoryScroll().Refresh()
		})
	}

	ui.threads.rangeFunc(func(t thread) {
		if g, ok := t.(*group); ok {
			if g.users.contains(chatUser.ID) {
				fyne.DoAndWait(func() { g.chatHistoryScroll().Refresh() })
			}
		}
	})

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
	u.images = chatUser.Images
	if chatUser.ID == ui.state.profile.id {
		ui.widgets.editProfile.profileIcon.images = ui.state.profile.images
		fyne.DoAndWait(func() {
			ui.widgets.editProfile.profileIcon.Refresh()
		})
	}

	// Update the chat bubbles that have an icon
	ui.messages.updateUserImage(chatUser.ID, chatUser.Images)
	if chatUser.ID != ui.state.profile.id {
		ui.threads.rangeFunc(func(t thread) {
			if g, ok := t.(*group); ok {
				if g.users.contains(chatUser.ID) {
					fyne.DoAndWait(func() { g.chatHistoryScroll().Refresh() })
				}
			}
		})
	}
}

func (ui *ui) UserNameUpdated(uuun chat.UpdateUserUpdateName) {
	if uuun.User == ui.state.profile.id {
		ui.threads.rangeFunc(func(t thread) {
			ti, err := ui.userChangedName(uuun.ID, uuun.User, uuun.OldName, uuun.Name, uuun.Timestamp)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error creating thread item for user name change")
			} else {
				fyne.DoAndWait(func() { ui.appendThreadItem(t, ti) })
			}
		})
	} else {
		dm, ok := ui.threads.getDM(uuun.User)
		if ok {
			ti, err := ui.userChangedName(uuun.ID, uuun.User, uuun.OldName, uuun.Name, uuun.Timestamp)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error creating thread item for user name change")
			} else {
				fyne.DoAndWait(func() { ui.appendThreadItem(dm, ti) })
			}
		}

		ui.threads.rangeFunc(func(t thread) {
			if g, ok := t.(*group); ok {
				if g.users.contains(uuun.User) {
					ti, err := ui.userChangedName(uuun.ID, uuun.User, uuun.OldName, uuun.Name, uuun.Timestamp)
					if err != nil {
						log.WithFields(log.Fields{
							"error": err.Error(),
						}).Error("error creating thread item for user name change")
					} else {
						fyne.DoAndWait(func() { ui.appendThreadItem(g, ti) })
					}
				}
			}
		})
	}
}

func (ui *ui) UserImageUpdated(uuui chat.UpdateUserUpdateImage) {
	if uuui.User == ui.state.profile.id {
		ui.threads.rangeFunc(func(t thread) {
			ti, err := ui.userChangedImage(uuui.ID, uuui.User, uuui.Timestamp)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error creating thread item for user image change")
			} else {
				fyne.DoAndWait(func() { ui.appendThreadItem(t, ti) })
			}
		})
	} else {
		dm, ok := ui.threads.getDM(uuui.User)
		if ok {
			ti, err := ui.userChangedImage(uuui.ID, uuui.User, uuui.Timestamp)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error creating thread item for user image change")
			} else {
				fyne.DoAndWait(func() { ui.appendThreadItem(dm, ti) })
			}
		}

		ui.threads.rangeFunc(func(t thread) {
			if g, ok := t.(*group); ok {
				if g.users.contains(uuui.User) {
					ti, err := ui.userChangedImage(uuui.ID, uuui.User, uuui.Timestamp)
					if err != nil {
						log.WithFields(log.Fields{
							"error": err.Error(),
						}).Error("error creating thread item for user image change")
					} else {
						fyne.DoAndWait(func() { ui.appendThreadItem(g, ti) })
					}
				}
			}
		})
	}
}

func (ui *ui) UserAliased(udsa chat.UpdateDMSetAlias) {
	if udsa.User == ui.state.profile.id {
		ui.threads.rangeFunc(func(t thread) {
			ti, err := ui.userAliasSet(udsa)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error creating thread item for user alias set")
			} else {
				fyne.DoAndWait(func() { ui.appendThreadItem(t, ti) })
			}
		})
	} else {
		dm, ok := ui.threads.getDM(udsa.User)
		if ok {
			ti, err := ui.userAliasSet(udsa)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error creating thread item for user alias set")
			} else {
				fyne.DoAndWait(func() { ui.appendThreadItem(dm, ti) })
			}
		}

		ui.threads.rangeFunc(func(t thread) {
			if g, ok := t.(*group); ok {
				if g.users.contains(udsa.User) {
					ti, err := ui.userAliasSet(udsa)
					if err != nil {
						log.WithFields(log.Fields{
							"error": err.Error(),
						}).Error("error creating thread item for user alias set")
					} else {
						fyne.DoAndWait(func() { ui.appendThreadItem(g, ti) })
					}
				}
			}
		})
	}
}

func (ui *ui) UserOnline(userID uuid.UUID) {
	fyne.DoAndWait(func() {
		log.WithFields(log.Fields{
			"user_id": userID,
		}).Info("user is online")
	})
}

func (ui *ui) UserOffline(userID uuid.UUID) {
	fyne.DoAndWait(func() {
		log.WithFields(log.Fields{
			"user_id": userID,
		}).Info("user is offline")
	})
}
