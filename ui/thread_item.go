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

var errUnknownThread = errors.New("unknown thread")
var errUnknownActor = errors.New("unknown actor")
var errUnknownUser = errors.New("unknown user")

var deletedUser = &user{
	id:   uuid.Nil,
	name: "-deleted-",
}

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
	return fyneUI.newStatusChangeThreadItem(
		ugn.ID,
		ugn.Thread,
		ugn.Actor,
		"changed the group name to "+ugn.Name,
		ugn.Timestamp,
	)
}

func (fyneUI *Fyne) newUpdateGroupRetention(ugr chat.UpdateGroupRetention) (*threadItem, error) {
	return fyneUI.newStatusChangeThreadItem(
		ugr.ID,
		ugr.Thread,
		ugr.Actor,
		"changed the message retention to "+getRetentionName(ugr.Retention),
		ugr.Timestamp,
	)
}

func (fyneUI *Fyne) newUpdateGroupAddUser(ugau chat.UpdateGroupAddUser) (*threadItem, error) {
	return fyneUI.newStatusChangeThreadItem(
		ugau.ID,
		ugau.Thread,
		ugau.Actor,
		"added "+ugau.User.Name+" to the group",
		ugau.Timestamp,
	)
}

func (fyneUI *Fyne) newUpdateGroupRemoveUser(ugru chat.UpdateGroupRemoveUser) (*threadItem, error) {
	removedUser, ok := fyneUI.users.get(ugru.User)
	if !ok {
		return &threadItem{}, errUnknownUser
	}

	if ugru.User == ugru.Actor {
		return fyneUI.newStatusChangeThreadItem(
			ugru.ID,
			ugru.Thread,
			ugru.Actor,
			"left the group",
			ugru.Timestamp,
		)
	}

	return fyneUI.newStatusChangeThreadItem(
		ugru.ID,
		ugru.Thread,
		ugru.Actor,
		"removed "+removedUser.name+" from the group",
		ugru.Timestamp,
	)
}

func (fyneUI *Fyne) newUpdateGroupClearHistory(ugch chat.UpdateGroupClearHistory) (*threadItem, error) {
	ti, err := fyneUI.newStatusChangeThreadItem(
		ugch.ID,
		ugch.Thread,
		ugch.Actor,
		"cleared the chat history",
		ugch.Timestamp,
	)
	ti.timestamp = ugch.ClearTime
	return ti, err
}

func (fyneUI *Fyne) newGroupMessage(gm chat.GroupMessage) (*threadItem, error) {
	group, exists := fyneUI.groups[gm.Thread]
	if !exists {
		log.WithFields(log.Fields{
			"group_id": gm.Thread,
		}).Error("group does not exist on message receive, ignoring the message")
		return &threadItem{}, errUnknownThread
	}

	user, exists := fyneUI.users.get(gm.Author)
	if !exists {
		user = deletedUser
		// TODO: make sure to make this unique with a text color for deleted users
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
		timestamp: gm.WrittenAt, // TODO: SavedAt would be more consistent across restarts but causes inconsistencies when re-adding to a group, as it gets reset
	}, nil
}

func (fyneUI *Fyne) newDirectMessage(dm chat.DirectMessage) (*threadItem, error) {
	dmThread, exists := fyneUI.dms[dm.Thread]
	if !exists {
		log.WithFields(log.Fields{
			"user_id": dm.Thread,
		}).Error("dm thread does not exist on message receive, ignoring the message")
		return &threadItem{}, errUnknownThread
	}

	u, exists := fyneUI.users.get(dm.Author)
	if !exists {
		log.WithFields(log.Fields{
			"user_id": dm.Author,
		}).Error("received a direct message from user ID not known to UI")
		return &threadItem{}, errUnknownUser
	}

	outgoing := dm.Author == fyneUI.profile.id
	displayName := u.name
	var notification *fyne.Notification
	if !outgoing {
		notification = fyne.NewNotification(u.name, dm.Text)
	} else {
		displayName = "You"
	}

	return &threadItem{
		id:           dm.ID,
		widget:       newChatBubble(displayName, dm.ID, dm.Text, outgoing, dm.WrittenAt, nil), // TODO: add SavedAt and show a difference if it's large
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
		timestamp: dm.WrittenAt, // TODO: SavedAt would be more consistent across restarts but causes inconsistencies when re-adding to a group, as it gets reset
	}, nil
}

func (fyneUI *Fyne) newUpdateDMRetention(udmr chat.UpdateDMRetention) (*threadItem, error) {
	return fyneUI.newStatusChangeThreadItem(
		udmr.ID,
		udmr.Thread,
		udmr.Actor,
		"changed the message retention to "+getRetentionName(udmr.Retention),
		udmr.Timestamp,
	)
}

func (fyneUI *Fyne) newUpdateDMClearHistory(udmch chat.UpdateDMClearHistory) (*threadItem, error) {
	ti, err := fyneUI.newStatusChangeThreadItem(
		udmch.ID,
		udmch.Thread,
		udmch.Actor,
		"cleared the chat history",
		udmch.Timestamp,
	)
	ti.timestamp = udmch.ClearTime
	return ti, err
}

func (fyneUI *Fyne) newUpdateGroupAdminPromoted(ugap chat.UpdateGroupAdminPromoted) (*threadItem, error) {
	newAdmin, ok := fyneUI.users.get(ugap.UserID)
	if !ok {
		return &threadItem{}, errUnknownUser
	}

	return fyneUI.newStatusChangeThreadItem(
		ugap.ID,
		ugap.Thread,
		ugap.Actor,
		"made "+newAdmin.name+" an admin",
		ugap.Timestamp,
	)
}

func (fyneUI *Fyne) newUpdateGroupAdminDemoted(ugad chat.UpdateGroupAdminDemoted) (*threadItem, error) {
	oldAdmin, ok := fyneUI.users.get(ugad.UserID)
	if !ok {
		return &threadItem{}, errUnknownUser
	}

	return fyneUI.newStatusChangeThreadItem(
		ugad.ID,
		ugad.Thread,
		ugad.Actor,
		"removed "+oldAdmin.name+" as an admin",
		ugad.Timestamp,
	)
}

func (fyneUI *Fyne) newUpdateGroupUserManagementRestricted(ugumr chat.UpdateGroupUserManagementRestricted) (*threadItem, error) {
	return fyneUI.newStatusChangeThreadItem(
		ugumr.ID,
		ugumr.Thread,
		ugumr.Actor,
		"restricted user management",
		ugumr.Timestamp,
	)
}

func (fyneUI *Fyne) newUpdateGroupUserManagementUnrestricted(ugumu chat.UpdateGroupUserManagementUnrestricted) (*threadItem, error) {
	return fyneUI.newStatusChangeThreadItem(
		ugumu.ID,
		ugumu.Thread,
		ugumu.Actor,
		"unrestricted user management",
		ugumu.Timestamp,
	)
}

func (fyneUI *Fyne) newUpdateGroupEditsRestricted(uger chat.UpdateGroupEditsRestricted) (*threadItem, error) {
	return fyneUI.newStatusChangeThreadItem(
		uger.ID,
		uger.Thread,
		uger.Actor,
		"restricted group edits",
		uger.Timestamp,
	)
}

func (fyneUI *Fyne) newUpdateGroupEditsUnrestricted(ugeu chat.UpdateGroupEditsUnrestricted) (*threadItem, error) {
	return fyneUI.newStatusChangeThreadItem(
		ugeu.ID,
		ugeu.Thread,
		ugeu.Actor,
		"unrestricted group edits",
		ugeu.Timestamp,
	)
}

func (fyneUI *Fyne) newUpdateGroupPostingRestricted(ugpr chat.UpdateGroupPostingRestricted) (*threadItem, error) {
	return fyneUI.newStatusChangeThreadItem(
		ugpr.ID,
		ugpr.Thread,
		ugpr.Actor,
		"restricted posting",
		ugpr.Timestamp,
	)
}

func (fyneUI *Fyne) newUpdateGroupPostingUnrestricted(ugpu chat.UpdateGroupPostingUnrestricted) (*threadItem, error) {
	return fyneUI.newStatusChangeThreadItem(
		ugpu.ID,
		ugpu.Thread,
		ugpu.Actor,
		"unrestricted posting",
		ugpu.Timestamp,
	)
}

func (fyneUI *Fyne) newUpdateGroupUserBlocked(ubg chat.UserBlockedGroup) (*threadItem, error) {
	return fyneUI.newStatusChangeThreadItem(
		ubg.ID,
		ubg.Thread,
		ubg.Actor,
		"blocked the group",
		ubg.Timestamp,
	)
}

func (fyneUI *Fyne) newGroupCreated(id, groupID, actorID uuid.UUID, timestamp int64) (*threadItem, error) {
	return fyneUI.newStatusChangeThreadItem(
		id,
		groupID,
		actorID,
		"created the group",
		timestamp,
	)
}

func (fyneUI *Fyne) newStatusChangeThreadItem(id, threadID, actorID uuid.UUID, changeString string, timestamp int64) (*threadItem, error) {
	t, ok := fyneUI.getThread(threadID)
	if !ok {
		log.WithFields(log.Fields{
			"thread_id": threadID,
		}).Error("thread not found for status change thread item")
		return &threadItem{}, errUnknownThread
	}

	actor, ok := fyneUI.users.get(actorID)
	if !ok {
		return &threadItem{}, errUnknownActor
	}
	actorName := actor.name
	if actorID == fyneUI.profile.id {
		actorName = "You"
	}

	changeString = actorName + " " + changeString
	changeLabel := newStatusChange(id, timestamp, changeString)

	return &threadItem{
		id:     id,
		widget: changeLabel,
		setButton: func(tb *threadButton) {
			t.getButton().setLastAction(changeString)
			t.getButton().setLastMessageTime(time.Unix(timestamp, 0))
			t.setLastMessageTime(timestamp)
			t.chatHistoryScroll().Refresh()
		},
		timestamp: timestamp,
	}, nil
}
