package ui

import (
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
	log "github.com/sirupsen/logrus"
)

type invite struct {
	id          uuid.UUID
	name        string
	initial     string
	inviteLabel *widget.RichText //canvas.Text
	icon        *defaultImage
	nameLabel   *widget.RichText
	images      []uuid.UUID
	view        *fyne.Container
	button      *threadButton
	timestamp   int64
	// TODO: widgets for the things, name, etc
}

func (ui *ui) buildNewGroupChatInvite(bounceGroup chat.Group) {
	invitedString := "you have been invited to join"
	if bounceGroup.InvitedBy != uuid.Nil {
		invitingUser, ok := ui.users.get(bounceGroup.InvitedBy)
		if ok {
			invitedString = invitingUser.getDisplayName() + " has invited you to join" // TODO: truncation
		}
	}

	i := &invite{
		id:   bounceGroup.ID,
		name: bounceGroup.Name,
		inviteLabel: widget.NewRichText(
			&widget.TextSegment{
				Text: invitedString,
				Style: widget.RichTextStyle{
					TextStyle: fyne.TextStyle{
						Italic: true,
					},
				},
			},
		),
		nameLabel: widget.NewRichText(
			&widget.TextSegment{
				Text: bounceGroup.Name,
				Style: widget.RichTextStyle{
					SizeName: theme.SizeNameHeadingText,
				},
			},
		),
		timestamp: bounceGroup.CreatedAt,
	}

	r, n := utf8.DecodeRuneInString(i.name)
	if r == utf8.RuneError {
		log.WithFields(log.Fields{
			"rune_error": r,
			"size":       n,
		}).Error("error setting group inital")
	} else {
		i.initial = string(r)
	}

	i.icon = newDefaultImage(i.id, i.images, i.initial, 128, ui.bounce.GetFileData, nil)

	acceptButton := widget.NewButton("Join Group", func() {})
	acceptButton.Importance = widget.HighImportance

	rejectButton := widget.NewButton("Reject Group", func() {}) // TODO: confirm dialog
	rejectButton.Importance = widget.DangerImportance

	userList := container.NewVBox(widget.NewLabel("Current Users"))
	for _, bounceUser := range bounceGroup.Users {
		admin := false
		for _, adminID := range bounceGroup.Admins {
			if bounceUser.ID == adminID {
				admin = true
				break
			}
		}

		u, ok := ui.users.get(bounceUser.ID)
		if !ok {
			log.Warn("unknown user in group we are invited to")
			continue
		}
		icon := newDefaultImage(u.id, u.images, u.initials, theme.IconInlineSize()*2, ui.bounce.GetFileData, nil)

		userList.Add(newUserButton(icon, u.name, admin, nil))
	}
	if len(bounceGroup.Invites) > 0 {
		userList.Add(widget.NewLabel("Invited Users"))
		for _, invitedID := range bounceGroup.Invites {
			u, ok := ui.users.get(invitedID)
			if !ok {
				log.Warn("unknown user in group we are invited to")
				continue
			}

			icon := newDefaultImage(u.id, u.images, u.initials, theme.IconInlineSize()*2, ui.bounce.GetFileData, nil)

			userList.Add(newUserButton(icon, u.name, false, nil))
		}
	}

	userListScroll := container.NewVScroll(userList)

	i.view = container.NewCenter(
		container.NewVBox(
			container.NewCenter(i.inviteLabel),
			container.NewCenter(i.icon),
			container.NewCenter(i.nameLabel),
			container.NewCenter(container.New(layout.NewGridLayoutWithColumns(2), acceptButton, rejectButton)),
			userListScroll,
		),
	)

	userListScroll.SetMinSize(fyne.Size{Height: 300})

	openThread := func() {
		ui.displayThread(i)
		if fyne.CurrentDevice().IsMobile() {
			ui.state.viewStack = append(ui.state.viewStack, view{viewType: viewTypeThread, context: i.id})
		}
		ui.state.currentView = viewTypeThread
	}
	i.button = newThreadButton(newDefaultImage(i.id, i.images, i.initial, 64, ui.bounce.GetFileData, openThread), i.name, openThread)

	ui.threads.add(bounceGroup.ID, i)
	ui.refreshThreadOrder()
}

func (i *invite) getID() uuid.UUID {
	return i.id
}

func (i *invite) getView() *fyne.Container {
	return i.view
}

func (i *invite) getEntry() *threadEntry {
	return nil
}

func (i *invite) chatHistoryScroll() *chatHistory {
	return nil
}

func (i *invite) getButton() *threadButton {
	return i.button
}

func (i *invite) getTypingIndicator() *typingIndicator {
	return nil
}

func (i *invite) getLastMessageTime() int64 {
	return i.timestamp
}

func (i *invite) setLastMessageTime(timestamp int64) {

}

func (i *invite) getNotificationsMutedUntil() int64 {
	return 0
}

func (i *invite) refreshReadReceiptSettingSelection([]string) {}

func (i *invite) refreshTypingIndicatorSettingSelection([]string) {}

func (i *invite) getEditIcon() *defaultImage {
	return nil
}

func (i *invite) getHeaderIcon() *defaultImage {
	return nil
}
