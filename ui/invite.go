package ui

import (
	"time"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
	log "github.com/sirupsen/logrus"
)

type invite struct {
	id             uuid.UUID
	name           string
	initial        string
	members        []uuid.UUID
	invited        []uuid.UUID
	admins         []uuid.UUID
	invitedBy      uuid.UUID
	inviteLabel    *widget.RichText
	icon           *defaultImage
	nameLabel      *widget.RichText
	images         []uuid.UUID
	view           *fyne.Container
	button         *threadButton
	userListScroll *container.Scroll
	timestamp      int64
}

func (ui *ui) buildNewGroupChatInvite(bounceGroup chat.Group) {
	i := &invite{
		id:        bounceGroup.ID,
		name:      bounceGroup.Name,
		images:    bounceGroup.Images,
		invitedBy: bounceGroup.InvitedBy,
		inviteLabel: widget.NewRichText(
			&widget.TextSegment{
				Text: "",
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
		timestamp: bounceGroup.InvitedAt,
	}

	//i.inviteLabel.Truncation = fyne.TextTruncateEllipsis // TODO: truncate with centered minimum width
	//i.nameLabel.Truncation = fyne.TextTruncateEllipsis
	i.setInitial()

	i.icon = newDefaultImage(i.id, i.images, i.initial, 128, ui.bounce.GetFileData, nil)

	acceptButton := widget.NewButton("Join Group", func() {
		err := ui.bounce.AcceptInvite(i.id)
		if err != nil {
			ui.showDialog(dialog.NewError(err, ui.window), nil)
		}
	})
	acceptButton.Importance = widget.HighImportance

	blockWarning := widget.NewLabel("You can prevent groups that are harassing you from ever being able to invite you again by permanently blocking the group")
	blockWarning.Wrapping = fyne.TextWrapWord
	blockCheck := widget.NewCheck("Permanently block group", nil)
	rejectConfirmDialog := dialog.NewCustomConfirm("Reject Group?", "Reject", "Cancel", container.NewVBox(blockWarning, blockCheck), func(confirmed bool) {
		defer blockCheck.SetChecked(false)

		if !confirmed {
			return
		}

		if blockCheck.Checked {
			ui.bounce.BlockGroup(i.id)
		} else {
			ui.bounce.RejectInvite(i.id)
		}
	}, ui.window)
	rejectButton := widget.NewButton("Reject Group", func() {
		ui.showDialog(rejectConfirmDialog, nil)
	})
	rejectButton.Importance = widget.DangerImportance

	for _, bounceUser := range bounceGroup.Users {
		admin := false
		for _, adminID := range bounceGroup.Admins {
			if bounceUser.ID == adminID {
				admin = true
				break
			}
		}

		i.members = append(i.members, bounceUser.ID)
		if admin {
			i.admins = append(i.admins, bounceUser.ID)
		}
	}
	if len(bounceGroup.Invites) > 0 {
		for _, invitedUser := range bounceGroup.Invites {
			i.invited = append(i.invited, invitedUser.ID)
		}
	}

	i.userListScroll = container.NewVScroll(container.NewVBox())

	desktopView := container.NewCenter(
		container.NewVBox(
			container.NewCenter(i.inviteLabel),
			container.NewCenter(i.icon),
			container.NewCenter(i.nameLabel),
			container.NewCenter(container.New(layout.NewGridLayoutWithColumns(2), acceptButton, rejectButton)),
			i.userListScroll,
		),
	)
	if fyne.CurrentDevice().IsMobile() {
		closeButton := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() {
			if fyne.CurrentDevice().IsMobile() {
				ui.mobileBack()
			} else {
				ui.showMainContainer()
			}
		})
		closeButton.Importance = widget.LowImportance

		closeBar := container.New(
			layout.NewBorderLayout(nil, nil, closeButton, nil),
			closeButton,
		)

		i.view = container.New(
			layout.NewBorderLayout(closeBar, nil, nil, nil),
			closeBar,
			desktopView,
		)
	} else {
		i.view = desktopView
	}

	openThread := func() {
		ui.displayThread(i)
		if fyne.CurrentDevice().IsMobile() {
			ui.state.viewStack = append(ui.state.viewStack, view{viewType: viewTypeThread, context: i.id})
		}
		ui.state.currentView = viewTypeThread
	}
	i.button = newThreadButton(newDefaultImage(i.id, i.images, i.initial, 64, ui.bounce.GetFileData, openThread), i.name, openThread)

	ui.refreshInvitedBy(i)
	ui.refreshInviteUsers(i)
	ui.threads.add(bounceGroup.ID, i)
	ui.refreshThreadOrder()
}

func (ui *ui) refreshInviteUsers(i *invite) {
	userList := container.NewVBox()
	userList.Add(widget.NewLabel("Current Users"))

	adminMap := map[uuid.UUID]bool{}
	for _, adminID := range i.admins {
		adminMap[adminID] = true
	}

	for _, memberID := range i.members {
		u, ok := ui.users.get(memberID)
		if !ok {
			log.Warn("unknown user in group we are invited to")
			continue
		}
		icon := newDefaultImage(u.id, u.images, u.initials, theme.IconInlineSize()*2, ui.bounce.GetFileData, nil)

		_, admin := adminMap[memberID]
		userList.Add(newUserButton(icon, u.name, admin, nil))
	}

	if len(i.invited) > 0 {
		userList.Add(widget.NewLabel("Invited Users"))
	}

	for _, invitedID := range i.invited {
		u, ok := ui.users.get(invitedID)
		if !ok {
			log.Warn("unknown user in group we are invited to")
			continue
		}

		icon := newDefaultImage(u.id, u.images, u.initials, theme.IconInlineSize()*2, ui.bounce.GetFileData, nil)

		userList.Add(newUserButton(icon, u.name, false, nil))
	}

	i.userListScroll.Content = userList

	scrollHeight := float32(0)
	n := 0
	for i, obj := range userList.Objects {
		n = i
		if i == 8 {
			break
		}
		scrollHeight += obj.MinSize().Height
	}
	scrollHeight += theme.Padding() * float32(n+1)
	i.userListScroll.SetMinSize(fyne.Size{Height: scrollHeight, Width: 250})
	i.userListScroll.Refresh()
}

func (ui *ui) refreshInvitedBy(i *invite) {
	invitedString := "you have been invited to join"
	if i.invitedBy != uuid.Nil {
		invitingUser, ok := ui.users.get(i.invitedBy)
		if ok {
			invitedString = invitingUser.getDisplayName() + " has invited you to join"
		}
	}

	i.inviteLabel.Segments[0].(*widget.TextSegment).Text = invitedString
	i.inviteLabel.Refresh()

	i.button.setLastAction(invitedString, false)
	i.button.setLastMessageTime(time.Unix(i.timestamp, 0))
}

func (i *invite) setInitial() {
	r, n := utf8.DecodeRuneInString(i.name)
	if r == utf8.RuneError {
		log.WithFields(log.Fields{
			"rune_error": r,
			"size":       n,
		}).Error("error setting group inital")
	} else {
		i.initial = string(r)
	}
}

func (i *invite) getID() uuid.UUID {
	return i.id
}

func (i *invite) getName() string {
	return i.name
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

func (i *invite) setOpened() {
}

func (i *invite) hasBeenOpened() bool {
	return true
}
