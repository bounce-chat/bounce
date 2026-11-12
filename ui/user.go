package ui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
	"github.com/bounce-chat/bounce/chat"
	log "github.com/sirupsen/logrus"
)

type user struct {
	id               chat.ID
	name             string
	alias            string
	initials         string
	images           []chat.ID
	notes            string
	introductionTime int64
	blocked          bool
}

func makeUser(chatUser chat.User) *user {
	u := &user{
		id:               chatUser.ID,
		name:             chatUser.Name,
		images:           chatUser.Images,
		alias:            chatUser.Alias,
		notes:            chatUser.Notes,
		introductionTime: chatUser.IntroductionTime,
		blocked:          chatUser.Blocked,
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
	split := strings.Split(u.getDisplayName(), " ")
	parts := []string{}
	for _, s := range split {
		if s != "" {
			parts = append(parts, s)
		}
	}
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

		if unicode.IsLetter(lastRune) {
			u.initials = string(firstRune) + string(lastRune)
		} else {
			u.initials = string(firstRune)
		}
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

	// Everything the UI thread reads about this user changes together, on the UI thread: the shared user record,
	// the search indexes keyed by its display name, and the widgets that show it.  The thread list is read on that
	// thread too, so its entry is replaced where no refresh can find the thread missing.
	fyne.DoAndWait(func() {
		u.name = chatUser.Name
		u.alias = chatUser.Alias
		u.notes = chatUser.Notes
		u.images = chatUser.Images
		u.blocked = chatUser.Blocked
		u.setInitials()

		ui.messages.renameUser(chatUser.ID, u.getDisplayName(), u.initials)
		ui.messages.updateUserImage(chatUser.ID, chatUser.Images)

		// Re-add the user to the stores they are in, to regenerate their search tokens
		allUserStoresMutex.Lock()
		for _, us := range allUserStores {
			if us.contains(chatUser.ID) {
				us.remove(chatUser.ID)
				us.add(u)
			}
		}
		allUserStoresMutex.Unlock()

		if dm, ok := ui.threads.getDM(chatUser.ID); ok {
			dm.user.images = chatUser.Images
			dm.editIcon.images = chatUser.Images
			dm.editIcon.setBackground()
			dm.editIcon.Refresh()
			dm.headerIcon.images = chatUser.Images
			dm.headerIcon.setBackground()
			dm.headerIcon.Refresh()
			dm.buttonData.images = chatUser.Images

			dm.username.Text = u.getDisplayName()
			dm.username.Refresh()
			dm.headerUserbutton.name.Segments[0].(*widget.TextSegment).Text = u.getDisplayName()
			dm.headerUserbutton.Refresh()

			if len(u.alias) > 0 && u.alias != u.name {
				dm.realName.Show()
			} else {
				dm.realName.Hide()
			}

			dm.notesEntry.SetText(chatUser.Notes)

			dm.headerIcon.setString(u.initials)
			dm.editIcon.setString(u.initials)
			dm.buttonData.setName(u.getDisplayName(), u.initials)

			if u.blocked {
				dm.entry.Disable()
			} else {
				dm.entry.Enable()
			}

			dm.chatHistoryScroll().Refresh()

			// Redo the thread's search tokens under its new name
			items := ui.threads.associatedItems(chatUser.ID)
			ui.threads.remove(chatUser.ID)
			ui.threads.add(chatUser.ID, dm)
			ui.threads.bulkAssociate(dm, items)
		}

		if chatUser.ID == ui.state.profile.id {
			ui.widgets.editProfile.profileIcon.images = u.images
			ui.widgets.editProfile.profileIcon.setBackground()
			ui.widgets.editProfile.profileIcon.Refresh()
		}

		// Every bubble of theirs in a group shows the name and icon
		ui.threads.rangeFunc(func(t thread) {
			if g, ok := t.(*group); ok && g.users.contains(chatUser.ID) {
				g.chatHistoryScroll().Refresh()
			}
		})

		ui.containers.threads.Refresh()
	})
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

func (ui *ui) UserOnline(userID chat.ID) {
	fyne.DoAndWait(func() {
		log.WithFields(log.Fields{
			"user_id": userID,
		}).Debug("user is online")
	})
}

func (ui *ui) UserOffline(userID chat.ID) {
	fyne.DoAndWait(func() {
		log.WithFields(log.Fields{
			"user_id": userID,
		}).Debug("user is offline")
	})
}
