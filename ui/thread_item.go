package ui

import (
	"errors"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/data/binding"
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
	log "github.com/sirupsen/logrus"
)

var errUnknownThread = errors.New("unknown thread")
var errUnknownActor = errors.New("unknown actor")
var errUnknownUser = errors.New("unknown user")

type threadable interface {
	getID() uuid.UUID
	isRead() bool
	populateTemplate(fyne.CanvasObject)
}

type chatBubbleData struct {
	id          uuid.UUID
	author      uuid.UUID
	displayName binding.String
	initials    binding.String
	text        string
	outgoing    bool
	direct      bool
	writtenAt   int64
	read        bool
	//profileButton fyne.CanvasObject
}

func (cbd *chatBubbleData) getID() uuid.UUID {
	return cbd.id
}

func (cbd *chatBubbleData) isRead() bool {
	return cbd.read
}

func (cbd *chatBubbleData) populateTemplate(obj fyne.CanvasObject) {
	obj.(*fyne.Container).Objects[0].(*chatBubble).setData(cbd.displayName, cbd.initials, cbd.author, cbd.id, cbd.text, cbd.outgoing, cbd.direct, cbd.writtenAt)
	obj.(*fyne.Container).Objects[0].(*chatBubble).Refresh()
	obj.(*fyne.Container).Objects[0].(*chatBubble).Show()
	obj.(*fyne.Container).Objects[1].(*statusChange).Hide()
}

type statusChangeData struct {
	id           uuid.UUID
	timestamp    int64
	changeString string
	read         bool
}

func (scd *statusChangeData) getID() uuid.UUID {
	return scd.id
}

func (scd *statusChangeData) isRead() bool {
	return scd.read
}

func (scd *statusChangeData) populateTemplate(obj fyne.CanvasObject) {
	obj.(*fyne.Container).Objects[1].(*statusChange).setData(scd.id, scd.timestamp, scd.changeString)
	obj.(*fyne.Container).Objects[1].(*statusChange).Refresh()
	obj.(*fyne.Container).Objects[1].(*statusChange).Show()
	obj.(*fyne.Container).Objects[0].(*chatBubble).Hide()
}

type threadItem struct {
	id             uuid.UUID
	widgetData     threadable
	notification   *fyne.Notification
	setButton      func(*threadButton)
	timestamp      int64
	dontBumpThread bool
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
		"removed "+removedUser.getName()+" from the group", // TODO: bind username:
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
		user = fyneUI.deletedUser
		// TODO: make sure to make this unique with a text color for deleted users
	}

	outgoing := gm.Author == fyneUI.profile.id
	displayName := user.name
	var notification *fyne.Notification
	if !outgoing {
		groupName, err := group.name.Get()
		if err != nil {
			log.Fatal("data bindings are broken")
		}
		notification = fyne.NewNotification(groupName, user.getName()+": "+gm.Text)
	} else {
		displayName = binding.NewString()
		displayName.Set("You")
	}

	return &threadItem{
		id: gm.ID,
		widgetData: &chatBubbleData{
			id:          gm.ID,
			author:      gm.Author,
			displayName: displayName,
			initials:    user.initials,
			text:        gm.Text,
			outgoing:    outgoing,
			direct:      false,
			writtenAt:   gm.WrittenAt,
		}, // TODO: add SavedAt and show a difference if it's large
		notification: notification,
		setButton: func(tb *threadButton) {
			displayName := user.getName() // TODO: bind last message button text?
			if gm.Author == fyneUI.profile.id {
				displayName = "You"
			}
			group.button.setLastMessage(displayName, gm.Text)
			group.button.setLastMessageTime(time.Unix(gm.WrittenAt, 0))
			group.setLastMessageTime(gm.WrittenAt)
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
		notification = fyne.NewNotification(u.getName(), dm.Text)
	} else {
		displayName = binding.NewString()
		displayName.Set("You")
	}

	return &threadItem{
		id: dm.ID,
		widgetData: &chatBubbleData{
			id:          dm.ID,
			author:      dm.Author,
			displayName: displayName,
			initials:    u.initials,
			text:        dm.Text,
			outgoing:    outgoing,
			direct:      true,
			writtenAt:   dm.WrittenAt,
		}, // TODO: add SavedAt and show a difference if it's large
		notification: notification,
		setButton: func(tb *threadButton) {
			displayName := u.getName()
			if dm.Author == fyneUI.profile.id {
				displayName = "You"
			}
			dmThread.button.setLastMessage(displayName, dm.Text)
			dmThread.button.setLastMessageTime(time.Unix(dm.WrittenAt, 0))
			dmThread.setLastMessageTime(dm.WrittenAt)
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
		"made "+newAdmin.getName()+" an admin", // TODO: bind?
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
		"removed "+oldAdmin.getName()+" as an admin", // TODO: bind?
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

func (fyneUI *Fyne) userChangedName(id, userID uuid.UUID, oldName, newName string, timestamp int64) (*threadItem, error) {
	user, ok := fyneUI.users.get(userID)
	if !ok {
		return &threadItem{}, errUnknownActor
	}

	action := " changed their name to "
	if user.id == fyneUI.profile.id {
		oldName = "You"
		action = " changed your name to "
	}

	changeString := oldName + action + newName

	return &threadItem{
		id: id,
		widgetData: &statusChangeData{
			id:           id,
			timestamp:    timestamp,
			changeString: changeString,
		},
		setButton:      nil,
		timestamp:      timestamp,
		dontBumpThread: true,
	}, nil
}

func (fyneUI *Fyne) newStatusChangeThreadItem(id, threadID, actorID uuid.UUID, action string, timestamp int64) (*threadItem, error) {
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
	actorName := actor.getName()
	if actorID == fyneUI.profile.id {
		actorName = "You"
	}

	changeString := actorName + " " + action

	return &threadItem{
		id: id,
		widgetData: &statusChangeData{
			id:           id,
			timestamp:    timestamp,
			changeString: changeString,
		},
		setButton: func(tb *threadButton) {
			t.getButton().setLastAction(changeString) // TODO: possible to bind actor's name?
			t.getButton().setLastMessageTime(time.Unix(timestamp, 0))
			t.setLastMessageTime(timestamp)
			t.chatHistoryScroll().Refresh()
		},
		timestamp: timestamp,
	}, nil
}
