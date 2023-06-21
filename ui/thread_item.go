package ui

import (
	"errors"

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

type threadItem struct {
	id           uuid.UUID
	source       interface{}
	widget       fyne.Widget
	notification fyne.Notification
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
	actor, ok := fyneUI.users.get(ugn.Actor)
	if !ok {
		return &threadItem{}, errUnknownActorInUpdateGroupName
	}

	changeString := actor.name + " changed the group name to " + ugn.Name
	changeLabel := widget.NewLabel(changeString)
	changeLabel.Alignment = fyne.TextAlignCenter

	return &threadItem{
		id:        ugn.ID,
		source:    ugn,
		widget:    changeLabel,
		timestamp: ugn.Timestamp,
	}, nil
}

func (fyneUI *Fyne) newUpdateGroupRetention(ugr chat.UpdateGroupRetention) (*threadItem, error) {
	actor, ok := fyneUI.users.get(ugr.Actor)
	if !ok {
		return &threadItem{}, errUnknownActorInUpdateGroupRetention
	}

	changeString := actor.name + " changed the group retention to " + getRetentionName(ugr.Retention)
	changeLabel := widget.NewLabel(changeString)
	changeLabel.Alignment = fyne.TextAlignCenter

	return &threadItem{
		id:        ugr.ID,
		source:    ugr,
		widget:    changeLabel,
		timestamp: ugr.Timestamp,
	}, nil
}

func (fyneUI *Fyne) newUpdateGroupAddUser(ugau chat.UpdateGroupAddUser) (*threadItem, error) {
	actor, ok := fyneUI.users.get(ugau.Actor)
	if !ok {
		return &threadItem{}, errUnknownActorInUpdateGroupAddUser
	}

	changeString := actor.name + " added " + ugau.User.Name + " to the group"
	changeLabel := widget.NewLabel(changeString)
	changeLabel.Alignment = fyne.TextAlignCenter

	return &threadItem{
		id:        ugau.ID,
		source:    ugau,
		widget:    changeLabel,
		timestamp: ugau.Timestamp,
	}, nil
}

func (fyneUI *Fyne) newUpdateGroupClearHistory(ugch chat.UpdateGroupClearHistory) (*threadItem, error) {
	actor, ok := fyneUI.users.get(ugch.Actor)
	if !ok {
		return &threadItem{}, errUnknownActorInUpdateGroupClearHistory
	}

	changeString := actor.name + " cleared the chat history"
	changeLabel := widget.NewLabel(changeString)
	changeLabel.Alignment = fyne.TextAlignCenter

	return &threadItem{
		id:        ugch.ID,
		source:    ugch,
		widget:    changeLabel,
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
	profileButton := widget.NewButtonWithIcon("", newEmbeddedResource("assets/not_found.png"), func() { // TODO: get image from user
		log.Info("user wants to open the profile of " + user.name)
		// TODO: display this user's profile
	})
	if outgoing {
		profileButton = nil
	}

	return &threadItem{
		id:        gm.ID,
		source:    gm,
		widget:    newChatBubble(user.name, gm.ID, gm.Text, outgoing, gm.WrittenAt, profileButton), // TODO: add SavedAt and show a difference if it's large
		timestamp: gm.SavedAt,
	}, nil
}

func (fyneUI *Fyne) newDirectMessage(dm chat.DirectMessage) (*threadItem, error) {
	u, exists := fyneUI.users.get(dm.Author)
	if !exists {
		log.WithFields(log.Fields{
			"user_id": dm.Author,
		}).Error("received a direct message from user ID not known to UI")
		return &threadItem{}, errUserNotFoundForDirectMessage
	}
	outgoing := dm.Author == fyneUI.profile.id
	profileButton := widget.NewButtonWithIcon("", newEmbeddedResource("assets/not_found.png"), func() { // TODO: get image from user
		log.Info("user wants to open the profile of " + u.name)
		// TODO: display this user's profile
	})
	if outgoing {
		profileButton = nil
	}

	return &threadItem{
		id:        dm.ID,
		source:    dm,
		widget:    newChatBubble(u.name, dm.ID, dm.Text, outgoing, dm.WrittenAt, profileButton), // TODO: add SavedAt and show a difference if it's large
		timestamp: dm.SavedAt,
	}, nil
}
