package ui

import (
	"errors"
	"time"

	"fyne.io/fyne/v2"
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
	log "github.com/sirupsen/logrus"
)

var errUnknownThread = errors.New("unknown thread")
var errUnknownActor = errors.New("unknown actor")
var errUnknownUser = errors.New("unknown user")

type threadable interface {
	getID() uuid.UUID
	isSeen() bool
	markSeen()
	countsAsUnread() bool
	getType() string
	getState() int
	setState(int)
	getAuthor() uuid.UUID
	getTimestamp() int64
	populateTemplate(fyne.CanvasObject)
}

type chatBubbleData struct {
	id        uuid.UUID
	frameType string
	author    uuid.UUID
	username  string
	initials  string
	text      string
	outgoing  bool
	direct    bool
	writtenAt int64
	expiresAt int64
	seen      bool
	state     int
	mergeMode int
	//profileButton fyne.CanvasObject
}

func (cbd *chatBubbleData) getID() uuid.UUID {
	return cbd.id
}

func (cbd *chatBubbleData) isSeen() bool {
	return cbd.seen
}

func (cbd *chatBubbleData) markSeen() {
	cbd.seen = true
}

func (cbd *chatBubbleData) countsAsUnread() bool {
	return true
}

func (cbd *chatBubbleData) getType() string {
	return cbd.frameType
}

func (cbd *chatBubbleData) getState() int {
	return cbd.state
}

func (cbd *chatBubbleData) getAuthor() uuid.UUID {
	return cbd.author
}

func (cbd *chatBubbleData) getTimestamp() int64 {
	return cbd.writtenAt
}

func (cbd *chatBubbleData) setState(state int) {
	cbd.state = state
}

func (cbd *chatBubbleData) populateTemplate(obj fyne.CanvasObject) {
	obj.(*fyne.Container).Objects[0].(*chatBubble).setData(cbd) // TODO: just pass cbd
	obj.(*fyne.Container).Objects[0].(*chatBubble).Refresh()
	obj.(*fyne.Container).Objects[0].(*chatBubble).Show()
	obj.(*fyne.Container).Objects[1].(*statusChange).Hide()
}

type statusChangeData struct {
	id           uuid.UUID
	frameType    string
	author       uuid.UUID
	timestamp    int64
	changeString string
	seen         bool
}

func (scd *statusChangeData) getID() uuid.UUID {
	return scd.id
}

func (scd *statusChangeData) isSeen() bool {
	return scd.seen
}

func (scd *statusChangeData) markSeen() {
	scd.seen = true
}

func (scd *statusChangeData) countsAsUnread() bool {
	return false
}

func (scd *statusChangeData) getType() string {
	return scd.frameType
}

func (scd *statusChangeData) getState() int {
	return statePending //TODO
}

func (scd *statusChangeData) setState(state int) {
	return
}

func (scd *statusChangeData) getAuthor() uuid.UUID {
	return scd.author
}

func (scd *statusChangeData) getTimestamp() int64 {
	return scd.timestamp
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
		chat.TypeUpdateGroup,
		"changed the group name to "+ugn.Name,
		ugn.Timestamp,
		ugn.Seen,
	)
}

func (fyneUI *Fyne) newUpdateGroupRetention(ugr chat.UpdateGroupRetention) (*threadItem, error) {
	return fyneUI.newStatusChangeThreadItem(
		ugr.ID,
		ugr.Thread,
		ugr.Actor,
		chat.TypeUpdateGroup,
		"changed the message retention to "+getRetentionName(ugr.Retention),
		ugr.Timestamp,
		ugr.Seen,
	)
}

func (fyneUI *Fyne) newUpdateGroupAddUser(ugau chat.UpdateGroupAddUser) (*threadItem, error) {
	return fyneUI.newStatusChangeThreadItem(
		ugau.ID,
		ugau.Thread,
		ugau.Actor,
		chat.TypeUpdateGroup,
		"added "+ugau.User.Name+" to the group",
		ugau.Timestamp,
		ugau.Seen,
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
			chat.TypeUpdateGroup,
			"left the group",
			ugru.Timestamp,
			ugru.Seen,
		)
	}

	return fyneUI.newStatusChangeThreadItem(
		ugru.ID,
		ugru.Thread,
		ugru.Actor,
		chat.TypeUpdateGroup,
		"removed "+removedUser.getName()+" from the group", // TODO: bind username:
		ugru.Timestamp,
		ugru.Seen,
	)
}

func (fyneUI *Fyne) newUpdateGroupClearHistory(ugch chat.UpdateGroupClearHistory) (*threadItem, error) {
	ti, err := fyneUI.newStatusChangeThreadItem(
		ugch.ID,
		ugch.Thread,
		ugch.Actor,
		chat.TypeUpdateGroup,
		"cleared the chat history",
		ugch.Timestamp,
		ugch.Seen,
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

	u, exists := fyneUI.users.get(gm.Author)
	if !exists {
		u = fyneUI.deletedUser
		// TODO: make sure to make this unique with a text color for deleted users
	}

	outgoing := gm.Author == fyneUI.profile.id
	username := u.getName()
	initials := u.getInitials()
	var notification *fyne.Notification
	if !outgoing {
		groupName, err := group.name.Get()
		if err != nil {
			log.Fatal("data bindings are broken")
		}
		notification = fyne.NewNotification(groupName, u.getName()+": "+gm.Text)
	} else {
		username = "You"
	}

	state := statePending
	if gm.Undeliverable {
		state = stateError
	}
	for _, id := range gm.DeliveredTo {
		if id == fyneUI.profile.id {
			if state == statePending {
				state = stateSynced
			}
		} else {
			state = stateDelivered
		}
	}
	for _, rr := range gm.ReadReceipts {
		if rr.Actor != fyneUI.profile.id {
			state = stateRead
			break
		}
	}
	return &threadItem{
		id: gm.ID,
		widgetData: &chatBubbleData{
			id:        gm.ID,
			frameType: chat.TypeGroupMessage,
			author:    gm.Author,
			username:  username,
			initials:  initials,
			text:      gm.Text,
			outgoing:  outgoing,
			direct:    false,
			writtenAt: gm.WrittenAt,
			expiresAt: gm.ExpiresAt,
			seen:      gm.Seen,
			state:     state,
			mergeMode: mergeModeStandalone, //TODO
		}, // TODO: add SavedAt and show a difference if it's large
		notification: notification,
		setButton: func(tb *threadButton) {
			displayName := u.getName() // TODO: bind last message button text?
			if gm.Author == fyneUI.profile.id {
				displayName = "You"
			}
			group.button.setLastMessage(displayName, gm.Text)
			group.button.setLastMessageTime(time.Unix(gm.WrittenAt, 0))
			group.setLastMessageTime(gm.WrittenAt)
			group.chatHistoryScroll().Refresh()
			if outgoing {
				tb.showLastMessageState(state)
			} else {
				tb.hideLastMessageState()
			}
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
	username := u.getName()
	initials := u.getInitials()
	var notification *fyne.Notification
	if !outgoing {
		notification = fyne.NewNotification(u.getName(), dm.Text)
	} else {
		username = "You"
	}

	state := statePending
	if dm.Undeliverable {
		state = stateError
	}
	for _, id := range dm.DeliveredTo {
		if id == fyneUI.profile.id {
			if state == statePending {
				state = stateSynced
			}
		} else {
			state = stateDelivered
		}
	}
	for _, rr := range dm.ReadReceipts {
		if rr.Actor != fyneUI.profile.id {
			state = stateRead
			break
		}
	}
	return &threadItem{
		id: dm.ID,
		widgetData: &chatBubbleData{
			id:        dm.ID,
			frameType: chat.TypeDirectMessage,
			author:    dm.Author,
			username:  username,
			initials:  initials,
			text:      dm.Text,
			outgoing:  outgoing,
			direct:    true,
			writtenAt: dm.WrittenAt,
			expiresAt: dm.ExpiresAt,
			seen:      dm.Seen,
			state:     state,
			mergeMode: mergeModeStandalone, //TODO
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
			if outgoing {
				tb.showLastMessageState(state)
			} else {
				tb.hideLastMessageState()
			}
		},
		timestamp: dm.WrittenAt, // TODO: SavedAt would be more consistent across restarts but causes inconsistencies when re-adding to a group, as it gets reset
	}, nil
}

func (fyneUI *Fyne) newUpdateDMRetention(udmr chat.UpdateDMRetention) (*threadItem, error) {
	return fyneUI.newStatusChangeThreadItem(
		udmr.ID,
		udmr.Thread,
		udmr.Actor,
		chat.TypeUpdateDM,
		"changed the message retention to "+getRetentionName(udmr.Retention),
		udmr.Timestamp,
		udmr.Seen,
	)
}

func (fyneUI *Fyne) newUpdateDMClearHistory(udmch chat.UpdateDMClearHistory) (*threadItem, error) {
	ti, err := fyneUI.newStatusChangeThreadItem(
		udmch.ID,
		udmch.Thread,
		udmch.Actor,
		chat.TypeUpdateDM,
		"cleared the chat history",
		udmch.Timestamp,
		udmch.Seen,
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
		chat.TypeUpdateGroup,
		"made "+newAdmin.getName()+" an admin", // TODO: bind?
		ugap.Timestamp,
		ugap.Seen,
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
		chat.TypeUpdateGroup,
		"removed "+oldAdmin.getName()+" as an admin", // TODO: bind?
		ugad.Timestamp,
		ugad.Seen,
	)
}

func (fyneUI *Fyne) newUpdateGroupUserManagementRestricted(ugumr chat.UpdateGroupUserManagementRestricted) (*threadItem, error) {
	return fyneUI.newStatusChangeThreadItem(
		ugumr.ID,
		ugumr.Thread,
		ugumr.Actor,
		chat.TypeUpdateGroup,
		"restricted user management",
		ugumr.Timestamp,
		ugumr.Seen,
	)
}

func (fyneUI *Fyne) newUpdateGroupUserManagementUnrestricted(ugumu chat.UpdateGroupUserManagementUnrestricted) (*threadItem, error) {
	return fyneUI.newStatusChangeThreadItem(
		ugumu.ID,
		ugumu.Thread,
		ugumu.Actor,
		chat.TypeUpdateGroup,
		"unrestricted user management",
		ugumu.Timestamp,
		ugumu.Seen,
	)
}

func (fyneUI *Fyne) newUpdateGroupEditsRestricted(uger chat.UpdateGroupEditsRestricted) (*threadItem, error) {
	return fyneUI.newStatusChangeThreadItem(
		uger.ID,
		uger.Thread,
		uger.Actor,
		chat.TypeUpdateGroup,
		"restricted group edits",
		uger.Timestamp,
		uger.Seen,
	)
}

func (fyneUI *Fyne) newUpdateGroupEditsUnrestricted(ugeu chat.UpdateGroupEditsUnrestricted) (*threadItem, error) {
	return fyneUI.newStatusChangeThreadItem(
		ugeu.ID,
		ugeu.Thread,
		ugeu.Actor,
		chat.TypeUpdateGroup,
		"unrestricted group edits",
		ugeu.Timestamp,
		ugeu.Seen,
	)
}

func (fyneUI *Fyne) newUpdateGroupPostingRestricted(ugpr chat.UpdateGroupPostingRestricted) (*threadItem, error) {
	return fyneUI.newStatusChangeThreadItem(
		ugpr.ID,
		ugpr.Thread,
		ugpr.Actor,
		chat.TypeUpdateGroup,
		"restricted posting",
		ugpr.Timestamp,
		ugpr.Seen,
	)
}

func (fyneUI *Fyne) newUpdateGroupPostingUnrestricted(ugpu chat.UpdateGroupPostingUnrestricted) (*threadItem, error) {
	return fyneUI.newStatusChangeThreadItem(
		ugpu.ID,
		ugpu.Thread,
		ugpu.Actor,
		chat.TypeUpdateGroup,
		"unrestricted posting",
		ugpu.Timestamp,
		ugpu.Seen,
	)
}

func (fyneUI *Fyne) newUpdateGroupUserBlocked(ubg chat.UserBlockedGroup) (*threadItem, error) {
	return fyneUI.newStatusChangeThreadItem(
		ubg.ID,
		ubg.Thread,
		ubg.Actor,
		chat.TypeUpdateGroup,
		"blocked the group",
		ubg.Timestamp,
		ubg.Seen,
	)
}

func (fyneUI *Fyne) newGroupCreated(id, groupID, actorID uuid.UUID, timestamp int64) (*threadItem, error) {
	return fyneUI.newStatusChangeThreadItem(
		id,
		groupID,
		actorID,
		chat.TypeGroupCreation,
		"created the group",
		timestamp,
		true,
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
			frameType:    chat.TypeUpdateUser,
			author:       userID,
			timestamp:    timestamp,
			changeString: changeString,
			seen:         true,
		},
		setButton:      nil,
		timestamp:      timestamp,
		dontBumpThread: true,
	}, nil
}

func (fyneUI *Fyne) newStatusChangeThreadItem(id, threadID, actorID uuid.UUID, frameType, action string, timestamp int64, seen bool) (*threadItem, error) {
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
			frameType:    frameType,
			author:       actorID,
			timestamp:    timestamp,
			changeString: changeString,
			seen:         seen,
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
