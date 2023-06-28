package ui

import (
	"errors"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
	log "github.com/sirupsen/logrus"
)

var errUnknownActorInUpdateGroupName = errors.New("unknown actor in update group name")
var errUnknownActorInUpdateGroupRetention = errors.New("unknown actor in update group retention")
var errUnknownActorInUpdateGroupAddUser = errors.New("unknown actor in update group add user")
var errUnknownActorInUpdateGroupClearHistory = errors.New("unknown actor in update group clear history")
var errUserNotFoundForGroupMessage = errors.New("user not found for group message")
var errGroupNotFoundForGroupMessage = errors.New("group not found for group message")
var errUserNotFoundForDirectMessage = errors.New("user not found for group message")
var errUnknownActorInUpdateDMRetention = errors.New("unknown actor in update dm retention")
var errUnknownActorInUpdateDMClearHistory = errors.New("unknown actor in update dm clear history")
var errUnknownActorInUpdateGroupUserManagementRestricted = errors.New("unknown actor in update group restrict user management")
var errUpdateForUnknownGroup = errors.New("group update targets unknown group")

type threadItem struct {
	id           uuid.UUID
	widget       fyne.Widget
	notification *fyne.Notification
	setButton    func(*threadButton)
	timestamp    int64
}

type threadItems []*threadItem

func (tis threadItems) Len() int {
	return len(tis)
}
func (tis threadItems) Swap(i, j int) {
	tis[i], tis[j] = tis[j], tis[i]
}
func (tis threadItems) Less(i, j int) bool {
	return tis[i].timestamp < tis[j].timestamp
}

func (fyneUI *Fyne) newUpdateGroupName(ugn chat.UpdateGroupName) (*threadItem, error) {
	g, exists := fyneUI.groups[ugn.Thread]
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugn.Thread,
		}).Error("group does not exist on message receive, ignoring the message")
		return &threadItem{}, errUpdateForUnknownGroup
	}

	actor, ok := fyneUI.users.get(ugn.Actor)
	if !ok {
		return &threadItem{}, errUnknownActorInUpdateGroupName
	}
	actorName := actor.name
	if ugn.Actor == fyneUI.profile.id {
		actorName = "You"
	}

	changeString := actorName + " changed the group name to " + ugn.Name
	changeLabel := widget.NewLabel(changeString)
	changeLabel.Alignment = fyne.TextAlignCenter

	return &threadItem{
		id:     ugn.ID,
		widget: changeLabel,
		setButton: func(tb *threadButton) {
			g.button.setLastAction(changeString)
			g.button.setLastMessageTime(time.Unix(ugn.Timestamp, 0))
			g.setLastMessageTime(ugn.Timestamp)
			g.chatHistoryScroll().Refresh()
		},
		timestamp: ugn.Timestamp,
	}, nil
}

func (fyneUI *Fyne) newUpdateGroupRetention(ugr chat.UpdateGroupRetention) (*threadItem, error) {
	g, exists := fyneUI.groups[ugr.Thread]
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugr.Thread,
		}).Error("group does not exist on message receive, ignoring the message")
		return &threadItem{}, errUpdateForUnknownGroup
	}

	actor, ok := fyneUI.users.get(ugr.Actor)
	if !ok {
		return &threadItem{}, errUnknownActorInUpdateGroupRetention
	}
	actorName := actor.name
	if ugr.Actor == fyneUI.profile.id {
		actorName = "You"
	}

	changeString := actorName + " changed the group retention to " + getRetentionName(ugr.Retention)
	changeLabel := widget.NewLabel(changeString)
	changeLabel.Alignment = fyne.TextAlignCenter

	return &threadItem{
		id:     ugr.ID,
		widget: changeLabel,
		setButton: func(tb *threadButton) {
			g.button.setLastAction(changeString)
			g.button.setLastMessageTime(time.Unix(ugr.Timestamp, 0))
			g.setLastMessageTime(ugr.Timestamp)
			g.chatHistoryScroll().Refresh()
		},
		timestamp: ugr.Timestamp,
	}, nil
}

func (fyneUI *Fyne) newUpdateGroupAddUser(ugau chat.UpdateGroupAddUser) (*threadItem, error) {
	g, exists := fyneUI.groups[ugau.Thread]
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugau.Thread,
		}).Error("group does not exist on message receive, ignoring the message")
		return &threadItem{}, errUpdateForUnknownGroup
	}

	actor, ok := fyneUI.users.get(ugau.Actor)
	if !ok {
		return &threadItem{}, errUnknownActorInUpdateGroupAddUser
	}
	actorName := actor.name
	if ugau.Actor == fyneUI.profile.id {
		actorName = "You"
	}

	changeString := actorName + " added " + ugau.User.Name + " to the group"
	changeLabel := widget.NewLabel(changeString)
	changeLabel.Alignment = fyne.TextAlignCenter

	return &threadItem{
		id:     ugau.ID,
		widget: changeLabel,
		setButton: func(tb *threadButton) {
			g.button.setLastAction(changeString)
			g.button.setLastMessageTime(time.Unix(ugau.Timestamp, 0))
			g.setLastMessageTime(ugau.Timestamp)
			g.chatHistoryScroll().Refresh()
		},
		timestamp: ugau.Timestamp,
	}, nil
}

func (fyneUI *Fyne) newUpdateGroupClearHistory(ugch chat.UpdateGroupClearHistory) (*threadItem, error) {
	g, exists := fyneUI.groups[ugch.Thread]
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugch.Thread,
		}).Error("group does not exist on message receive, ignoring the message")
		return &threadItem{}, errUpdateForUnknownGroup
	}

	actor, ok := fyneUI.users.get(ugch.Actor)
	if !ok {
		return &threadItem{}, errUnknownActorInUpdateGroupClearHistory
	}
	changeString := actor.name + " cleared the chat history"
	changeLabel := widget.NewLabel(changeString)
	changeLabel.Alignment = fyne.TextAlignCenter

	return &threadItem{
		id:     ugch.ID,
		widget: changeLabel,
		setButton: func(tb *threadButton) {
			g.button.setLastAction(changeString)
			g.button.setLastMessageTime(time.Unix(ugch.Timestamp, 0))
			g.setLastMessageTime(ugch.Timestamp)
			g.chatHistoryScroll().Refresh()
		},
		timestamp: ugch.ClearTime,
	}, nil
}

func (fyneUI *Fyne) newGroupMessage(gm chat.GroupMessage) (*threadItem, error) {
	group, exists := fyneUI.groups[gm.Thread]
	if !exists {
		log.WithFields(log.Fields{
			"group_id": gm.Thread,
		}).Error("group does not exist on message receive, ignoring the message")
		return &threadItem{}, errGroupNotFoundForGroupMessage
	}

	user, exists := group.users.get(gm.Author)
	if !exists {
		log.WithFields(log.Fields{
			"user_id": gm.Author,
		}).Error("group received a message from user ID not in thread")
		return &threadItem{}, errUserNotFoundForGroupMessage
	}

	outgoing := gm.Author == fyneUI.profile.id
	displayName := user.name
	var profileButton *widget.Button
	var notification *fyne.Notification
	if !outgoing {
		groupName, err := group.name.Get()
		if err != nil {
			log.Fatal("data bindings are broken")
		}
		notification = fyne.NewNotification(groupName, user.name+": "+gm.Text)
		profileButton = widget.NewButtonWithIcon("", newEmbeddedResource("assets/not_found.png"), func() { // TODO: get image from user
			log.Info("user wants to open the profile of " + user.name)
			// TODO: display this user's profile
		})
	} else {
		displayName = "You"
	}

	return &threadItem{
		id:           gm.ID,
		widget:       newChatBubble(displayName, gm.ID, gm.Text, outgoing, gm.WrittenAt, profileButton), // TODO: add SavedAt and show a difference if it's large
		notification: notification,
		setButton: func(tb *threadButton) {
			displayName := user.name
			if gm.Author == fyneUI.profile.id {
				displayName = "You"
			}
			group.button.setLastMessage(displayName, gm.Text)
			group.button.setLastMessageTime(time.Unix(gm.SavedAt, 0))
			group.setLastMessageTime(gm.SavedAt)
			group.chatHistoryScroll().Refresh()
		},
		timestamp: gm.SavedAt,
	}, nil
}

func (fyneUI *Fyne) newDirectMessage(dm chat.DirectMessage) (*threadItem, error) {
	dmThread, exists := fyneUI.dms[dm.Thread]
	if !exists {
		log.WithFields(log.Fields{
			"user_id": dm.Thread,
		}).Error("dm thread does not exist on message receive, ignoring the message")
		return &threadItem{}, errUserNotFoundForDirectMessage
	}

	u, exists := fyneUI.users.get(dm.Author)
	if !exists {
		log.WithFields(log.Fields{
			"user_id": dm.Author,
		}).Error("received a direct message from user ID not known to UI")
		return &threadItem{}, errUserNotFoundForDirectMessage
	}

	outgoing := dm.Author == fyneUI.profile.id
	displayName := u.name
	var profileButton *widget.Button
	var notification *fyne.Notification
	if !outgoing {
		notification = fyne.NewNotification(u.name, dm.Text)
		profileButton = widget.NewButtonWithIcon("", newEmbeddedResource("assets/not_found.png"), func() { // TODO: get image from user
			log.Info("user wants to open the profile of " + u.name)
			// TODO: display this user's profile
		})
	} else {
		displayName = "You"
	}

	return &threadItem{
		id:           dm.ID,
		widget:       newChatBubble(displayName, dm.ID, dm.Text, outgoing, dm.WrittenAt, profileButton), // TODO: add SavedAt and show a difference if it's large
		notification: notification,
		setButton: func(tb *threadButton) {
			displayName := u.name
			if dm.Author == fyneUI.profile.id {
				displayName = "You"
			}
			dmThread.button.setLastMessage(displayName, dm.Text)
			dmThread.button.setLastMessageTime(time.Unix(dm.SavedAt, 0))
			dmThread.setLastMessageTime(dm.SavedAt)
			dmThread.chatHistoryScroll().Refresh()
		},
		timestamp: dm.SavedAt,
	}, nil
}

func (fyneUI *Fyne) newUpdateDMRetention(udmr chat.UpdateDMRetention) (*threadItem, error) {
	dmThread, exists := fyneUI.dms[udmr.Thread]
	if !exists {
		log.WithFields(log.Fields{
			"user_id": udmr.Thread,
		}).Error("dm thread does not exist on update retention")
		return &threadItem{}, errUserNotFoundForDirectMessage
	}

	actor, ok := fyneUI.users.get(udmr.Actor)
	if !ok {
		return &threadItem{}, errUnknownActorInUpdateDMRetention
	}
	actorName := actor.name
	if udmr.Actor == fyneUI.profile.id {
		actorName = "You"
	}

	changeString := actorName + " changed the group retention to " + getRetentionName(udmr.Retention)
	changeLabel := widget.NewLabel(changeString)
	changeLabel.Alignment = fyne.TextAlignCenter

	return &threadItem{
		id:     udmr.ID,
		widget: changeLabel,
		setButton: func(tb *threadButton) {
			dmThread.button.setLastAction(changeString)
			dmThread.button.setLastMessageTime(time.Unix(udmr.Timestamp, 0))
			dmThread.setLastMessageTime(udmr.Timestamp)
			dmThread.chatHistoryScroll().Refresh()
		},
		timestamp: udmr.Timestamp,
	}, nil
}

func (fyneUI *Fyne) newUpdateDMClearHistory(udmch chat.UpdateDMClearHistory) (*threadItem, error) {
	dmThread, exists := fyneUI.dms[udmch.Thread]
	if !exists {
		log.WithFields(log.Fields{
			"user_id": udmch.Thread,
		}).Error("dm thread does not exist on clear history")
		return &threadItem{}, errUserNotFoundForDirectMessage
	}

	actor, ok := fyneUI.users.get(udmch.Actor)
	if !ok {
		return &threadItem{}, errUnknownActorInUpdateDMClearHistory
	}

	changeString := actor.name + " cleared the chat history"
	changeLabel := widget.NewLabel(changeString)
	changeLabel.Alignment = fyne.TextAlignCenter

	return &threadItem{
		id:     udmch.ID,
		widget: changeLabel,
		setButton: func(tb *threadButton) {
			dmThread.button.setLastAction(changeString)
			dmThread.button.setLastMessageTime(time.Unix(udmch.Timestamp, 0))
			dmThread.setLastMessageTime(udmch.Timestamp)
			dmThread.chatHistoryScroll().Refresh()
		},
		timestamp: udmch.ClearTime,
	}, nil
}

func (fyneUI *Fyne) newUpdateGroupUserManagementRestricted(ugumr chat.UpdateGroupUserManagementRestricted) (*threadItem, error) {
	g, exists := fyneUI.groups[ugumr.Thread]
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugumr.Thread,
		}).Error("group does not exist on message receive, ignoring the message")
		return &threadItem{}, errUpdateForUnknownGroup
	}

	actor, ok := fyneUI.users.get(ugumr.Actor)
	if !ok {
		return &threadItem{}, errUnknownActorInUpdateGroupUserManagementRestricted
	}

	changeString := actor.name + " unrestricted user management"
	changeLabel := widget.NewLabel(changeString)
	changeLabel.Alignment = fyne.TextAlignCenter

	return &threadItem{
		id:     ugumr.ID,
		widget: changeLabel,
		setButton: func(tb *threadButton) {
			g.button.setLastAction(changeString)
			g.button.setLastMessageTime(time.Unix(ugumr.Timestamp, 0))
			g.setLastMessageTime(ugumr.Timestamp)
			g.chatHistoryScroll().Refresh()
		},
		timestamp: ugumr.Timestamp,
	}, nil
}
