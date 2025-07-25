package ui

import (
	"errors"
	"image"
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
	id               uuid.UUID
	frameType        string
	author           uuid.UUID
	iconImages       []uuid.UUID
	username         string
	initials         string
	text             string
	imageAttachments []*chat.ImageAttachment
	fileAttachments  []*chat.FileAttachment
	outgoing         bool
	direct           bool
	writtenAt        int64
	expiresAt        int64
	seen             bool
	state            int
	mergeMode        int
	imageDisplay     func([]image.Image, [][]byte, int)
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
	obj.(*fyne.Container).Objects[0].(*chatBubble).setData(cbd)
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

func (ui *ui) newUpdateGroupName(ugn chat.UpdateGroupName) (*threadItem, error) {
	return ui.newStatusChangeThreadItem(
		ugn.ID,
		ugn.Thread,
		ugn.Actor,
		chat.TypeUpdateGroup,
		"changed the group name to "+ugn.Name,
		ugn.Timestamp,
		ugn.Seen,
	)
}

func (ui *ui) newUpdateGroupRetention(ugr chat.UpdateGroupRetention) (*threadItem, error) {
	return ui.newStatusChangeThreadItem(
		ugr.ID,
		ugr.Thread,
		ugr.Actor,
		chat.TypeUpdateGroup,
		"changed the message retention to "+getRetentionName(ugr.Retention),
		ugr.Timestamp,
		ugr.Seen,
	)
}

func (ui *ui) newUpdateGroupAddUser(ugau chat.UpdateGroupAddUser) (*threadItem, error) {
	return ui.newStatusChangeThreadItem(
		ugau.ID,
		ugau.Thread,
		ugau.Actor,
		chat.TypeUpdateGroup,
		"added "+ugau.User.Name+" to the group",
		ugau.Timestamp,
		ugau.Seen,
	)
}

func (ui *ui) newUpdateGroupRemoveUser(ugru chat.UpdateGroupRemoveUser) (*threadItem, error) {
	removedUser, ok := ui.users.get(ugru.User)
	if !ok {
		return &threadItem{}, errUnknownUser
	}

	if ugru.User == ugru.Actor {
		return ui.newStatusChangeThreadItem(
			ugru.ID,
			ugru.Thread,
			ugru.Actor,
			chat.TypeUpdateGroup,
			"left the group",
			ugru.Timestamp,
			ugru.Seen,
		)
	}

	return ui.newStatusChangeThreadItem(
		ugru.ID,
		ugru.Thread,
		ugru.Actor,
		chat.TypeUpdateGroup,
		"removed "+removedUser.getName()+" from the group", // TODO: bind username:
		ugru.Timestamp,
		ugru.Seen,
	)
}

func (ui *ui) newUpdateGroupClearHistory(ugch chat.UpdateGroupClearHistory) (*threadItem, error) {
	ti, err := ui.newStatusChangeThreadItem(
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

func (ui *ui) newGroupMessage(gm chat.GroupMessage) (*threadItem, error) {
	group, exists := ui.threads.getGroup(gm.Thread)
	if !exists {
		log.WithFields(log.Fields{
			"group_id": gm.Thread,
		}).Error("group does not exist on message receive, ignoring the message")
		return &threadItem{}, errUnknownThread
	}

	u, exists := ui.users.get(gm.Author)
	if !exists {
		u = makeUser(uuid.Nil, "-deleted-")
		// TODO: make sure to make this unique with a text color for deleted users
	}

	outgoing := gm.Author == ui.state.profile.id
	username := u.getName()
	initials := u.getInitials()
	var notification *fyne.Notification
	if !outgoing {
		groupName, err := group.name.Get()
		if err != nil {
			log.Fatal("data bindings are broken")
		}

		notificationString := u.getName() + ": " + gm.Text
		if len(gm.Text) == 0 {
			notificationString = u.getName() + " posted "

			if len(gm.ImageAttachments) > 1 {
				notificationString += "images"
			} else if len(gm.ImageAttachments) == 1 {
				notificationString += "an image"
			}

			if len(gm.FileAttachments) > 1 {
				if len(gm.ImageAttachments) != 0 {
					notificationString += " and "
				}
				notificationString += "files"
			} else if len(gm.FileAttachments) == 1 {
				if len(gm.ImageAttachments) != 0 {
					notificationString += " and "
				}
				notificationString += "a file"
			}
		}

		notification = fyne.NewNotification(groupName, notificationString)
	} else {
		username = "You"
	}

	state := statePending
	if gm.Undeliverable {
		state = stateError
	}
	for _, id := range gm.DeliveredTo {
		if id == ui.state.profile.id {
			if state == statePending {
				state = stateSynced
			}
		} else {
			state = stateDelivered
		}
	}
	for _, rr := range gm.ReadReceipts {
		if rr.Actor != ui.state.profile.id {
			state = stateRead
			break
		}
	}
	imageAttachments := []*chat.ImageAttachment{}
	for i := 0; i < len(gm.ImageAttachments); i++ {
		imageAttachments = append(imageAttachments, &gm.ImageAttachments[i])
	}
	fileAttachments := []*chat.FileAttachment{}
	for i := 0; i < len(gm.FileAttachments); i++ {
		fileAttachments = append(fileAttachments, &gm.FileAttachments[i])
	}
	return &threadItem{
		id: gm.ID,
		widgetData: &chatBubbleData{
			id:               gm.ID,
			frameType:        chat.TypeGroupMessage,
			author:           gm.Author,
			iconImages:       u.images,
			username:         username,
			initials:         initials,
			text:             gm.Text,
			imageAttachments: imageAttachments,
			fileAttachments:  fileAttachments,
			outgoing:         outgoing,
			direct:           false,
			writtenAt:        gm.WrittenAt,
			expiresAt:        gm.ExpiresAt,
			seen:             gm.Seen,
			state:            state,
			mergeMode:        mergeModeStandalone,
			imageDisplay:     ui.showImageViewer,
		}, // TODO: add SavedAt and show a difference if it's large
		notification: notification,
		setButton: func(tb *threadButton) {
			displayName := u.getName() // TODO: bind last message button text?
			mine := gm.Author == ui.state.profile.id
			if mine {
				displayName = "You"
			}
			if len(gm.Text) == 0 {
				changeString := displayName + " posted "

				if len(gm.ImageAttachments) > 1 {
					changeString += "images"
				} else if len(gm.ImageAttachments) == 1 {
					changeString += "an image"
				}

				if len(gm.FileAttachments) > 1 {
					if len(gm.ImageAttachments) != 0 {
						changeString += " and "
					}
					changeString += "files"
				} else if len(gm.FileAttachments) == 1 {
					if len(gm.ImageAttachments) != 0 {
						changeString += " and "
					}
					changeString += "a file"
				}

				tb.setLastAction(changeString, mine) // TODO: possible to bind actor's name?
			} else {
				group.button.setLastMessage(displayName, gm.Text, mine)
			}
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

func (ui *ui) newDirectMessage(dm chat.DirectMessage) (*threadItem, error) {
	dmThread, exists := ui.threads.getDM(dm.Thread)
	if !exists {
		log.WithFields(log.Fields{
			"user_id": dm.Thread,
		}).Error("dm thread does not exist on message receive, ignoring the message")
		return &threadItem{}, errUnknownThread
	}

	u, exists := ui.users.get(dm.Author)
	if !exists {
		log.WithFields(log.Fields{
			"user_id": dm.Author,
		}).Error("received a direct message from user ID not known to UI")
		return &threadItem{}, errUnknownUser
	}

	outgoing := dm.Author == ui.state.profile.id
	username := u.getName()
	initials := u.getInitials()
	var notification *fyne.Notification
	if !outgoing {
		notificationString := dm.Text
		if len(dm.Text) == 0 {
			notificationString = "posted "

			if len(dm.ImageAttachments) > 1 {
				notificationString += "images"
			} else if len(dm.ImageAttachments) == 1 {
				notificationString += "an image"
			}

			if len(dm.FileAttachments) > 1 {
				if len(dm.ImageAttachments) != 0 {
					notificationString += " and "
				}
				notificationString += "files"
			} else if len(dm.FileAttachments) == 1 {
				if len(dm.ImageAttachments) != 0 {
					notificationString += " and "
				}
				notificationString += "a file"
			}
		}
		notification = fyne.NewNotification(u.getName(), notificationString)
	} else {
		username = "You"
	}

	state := statePending
	if dm.Undeliverable {
		state = stateError
	}
	for _, id := range dm.DeliveredTo {
		if id == ui.state.profile.id {
			if state == statePending {
				state = stateSynced
			}
		} else {
			state = stateDelivered
		}
	}
	for _, rr := range dm.ReadReceipts {
		if rr.Actor != ui.state.profile.id {
			state = stateRead
			break
		}
	}
	imageAttachments := []*chat.ImageAttachment{}
	for i := 0; i < len(dm.ImageAttachments); i++ {
		imageAttachments = append(imageAttachments, &dm.ImageAttachments[i])
	}
	fileAttachments := []*chat.FileAttachment{}
	for i := 0; i < len(dm.FileAttachments); i++ {
		fileAttachments = append(fileAttachments, &dm.FileAttachments[i])
	}
	return &threadItem{
		id: dm.ID,
		widgetData: &chatBubbleData{
			id:               dm.ID,
			frameType:        chat.TypeDirectMessage,
			author:           dm.Author,
			username:         username,
			initials:         initials,
			text:             dm.Text,
			imageAttachments: imageAttachments,
			fileAttachments:  fileAttachments,
			outgoing:         outgoing,
			direct:           true,
			writtenAt:        dm.WrittenAt,
			expiresAt:        dm.ExpiresAt,
			seen:             dm.Seen,
			state:            state,
			mergeMode:        mergeModeStandalone,
			imageDisplay:     ui.showImageViewer,
		}, // TODO: add SavedAt and show a difference if it's large
		notification: notification,
		setButton: func(tb *threadButton) {
			displayName := u.getName()
			mine := dm.Author == ui.state.profile.id
			if mine {
				displayName = "You"
			}
			if len(dm.Text) == 0 {
				changeString := displayName + " posted "

				if len(dm.ImageAttachments) > 1 {
					changeString += "images"
				} else if len(dm.ImageAttachments) == 1 {
					changeString += "an image"
				}

				if len(dm.FileAttachments) > 1 {
					changeString += " and files"
				} else if len(dm.FileAttachments) == 1 {
					changeString += " and a file"
				}

				tb.setLastAction(changeString, mine) // TODO: possible to bind actor's name?
			} else {
				dmThread.getButton().setLastMessage(displayName, dm.Text, mine)
			}
			dmThread.getButton().setLastMessageTime(time.Unix(dm.WrittenAt, 0))
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

func (ui *ui) newUpdateDMRetention(udmr chat.UpdateDMRetention) (*threadItem, error) {
	return ui.newStatusChangeThreadItem(
		udmr.ID,
		udmr.Thread,
		udmr.Actor,
		chat.TypeUpdateDM,
		"changed the message retention to "+getRetentionName(udmr.Retention),
		udmr.Timestamp,
		udmr.Seen,
	)
}

func (ui *ui) newUpdateDMClearHistory(udmch chat.UpdateDMClearHistory) (*threadItem, error) {
	ti, err := ui.newStatusChangeThreadItem(
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

func (ui *ui) userAliasSet(udsa chat.UpdateDMSetAlias) (*threadItem, error) {
	u, ok := ui.users.get(udsa.User)
	if !ok {
		log.WithFields(log.Fields{
			"user_id": udsa.User,
		}).Error("cannot create alias thread item for unknown user")
		return &threadItem{}, errUnknownUser
	}

	changeString := u.getName() + " has been aliased to " + udsa.Alias

	return &threadItem{
		id: udsa.ID,
		widgetData: &statusChangeData{
			id:           udsa.ID,
			frameType:    chat.TypeUpdateDM,
			author:       ui.state.profile.id,
			timestamp:    udsa.Timestamp,
			changeString: changeString,
			seen:         true,
		},
		setButton:      nil,
		timestamp:      udsa.Timestamp,
		dontBumpThread: true,
	}, nil
}

func (ui *ui) newUpdateGroupAdminPromoted(ugap chat.UpdateGroupAdminPromoted) (*threadItem, error) {
	newAdmin, ok := ui.users.get(ugap.UserID)
	if !ok {
		return &threadItem{}, errUnknownUser
	}

	return ui.newStatusChangeThreadItem(
		ugap.ID,
		ugap.Thread,
		ugap.Actor,
		chat.TypeUpdateGroup,
		"made "+newAdmin.getName()+" an admin", // TODO: bind?
		ugap.Timestamp,
		ugap.Seen,
	)
}

func (ui *ui) newUpdateGroupAdminDemoted(ugad chat.UpdateGroupAdminDemoted) (*threadItem, error) {
	oldAdmin, ok := ui.users.get(ugad.UserID)
	if !ok {
		return &threadItem{}, errUnknownUser
	}

	return ui.newStatusChangeThreadItem(
		ugad.ID,
		ugad.Thread,
		ugad.Actor,
		chat.TypeUpdateGroup,
		"removed "+oldAdmin.getName()+" as an admin", // TODO: bind?
		ugad.Timestamp,
		ugad.Seen,
	)
}

func (ui *ui) newUpdateGroupUserManagementRestricted(ugumr chat.UpdateGroupUserManagementRestricted) (*threadItem, error) {
	return ui.newStatusChangeThreadItem(
		ugumr.ID,
		ugumr.Thread,
		ugumr.Actor,
		chat.TypeUpdateGroup,
		"restricted user management",
		ugumr.Timestamp,
		ugumr.Seen,
	)
}

func (ui *ui) newUpdateGroupUserManagementUnrestricted(ugumu chat.UpdateGroupUserManagementUnrestricted) (*threadItem, error) {
	return ui.newStatusChangeThreadItem(
		ugumu.ID,
		ugumu.Thread,
		ugumu.Actor,
		chat.TypeUpdateGroup,
		"unrestricted user management",
		ugumu.Timestamp,
		ugumu.Seen,
	)
}

func (ui *ui) newUpdateGroupEditsRestricted(uger chat.UpdateGroupEditsRestricted) (*threadItem, error) {
	return ui.newStatusChangeThreadItem(
		uger.ID,
		uger.Thread,
		uger.Actor,
		chat.TypeUpdateGroup,
		"restricted group edits",
		uger.Timestamp,
		uger.Seen,
	)
}

func (ui *ui) newUpdateGroupEditsUnrestricted(ugeu chat.UpdateGroupEditsUnrestricted) (*threadItem, error) {
	return ui.newStatusChangeThreadItem(
		ugeu.ID,
		ugeu.Thread,
		ugeu.Actor,
		chat.TypeUpdateGroup,
		"unrestricted group edits",
		ugeu.Timestamp,
		ugeu.Seen,
	)
}

func (ui *ui) newUpdateGroupPostingRestricted(ugpr chat.UpdateGroupPostingRestricted) (*threadItem, error) {
	return ui.newStatusChangeThreadItem(
		ugpr.ID,
		ugpr.Thread,
		ugpr.Actor,
		chat.TypeUpdateGroup,
		"restricted posting",
		ugpr.Timestamp,
		ugpr.Seen,
	)
}

func (ui *ui) newUpdateGroupPostingUnrestricted(ugpu chat.UpdateGroupPostingUnrestricted) (*threadItem, error) {
	return ui.newStatusChangeThreadItem(
		ugpu.ID,
		ugpu.Thread,
		ugpu.Actor,
		chat.TypeUpdateGroup,
		"unrestricted posting",
		ugpu.Timestamp,
		ugpu.Seen,
	)
}

func (ui *ui) newUpdateGroupUserBlocked(ubg chat.UserBlockedGroup) (*threadItem, error) {
	return ui.newStatusChangeThreadItem(
		ubg.ID,
		ubg.Thread,
		ubg.Actor,
		chat.TypeUpdateGroup,
		"blocked the group",
		ubg.Timestamp,
		ubg.Seen,
	)
}

func (ui *ui) newUpdateGroupUserChangedGroupImage(ugci chat.UpdateGroupUserChangedGroupImage) (*threadItem, error) {
	return ui.newStatusChangeThreadItem(
		ugci.ID,
		ugci.Thread,
		ugci.Actor,
		chat.TypeUpdateGroup,
		"changed the group image",
		ugci.Timestamp,
		ugci.Seen,
	)
}

func (ui *ui) newGroupCreated(id, groupID, actorID uuid.UUID, timestamp int64) (*threadItem, error) {
	return ui.newStatusChangeThreadItem(
		id,
		groupID,
		actorID,
		chat.TypeGroupCreation,
		"created the group",
		timestamp,
		true,
	)
}

func (ui *ui) userChangedName(id, userID uuid.UUID, oldName, newName string, timestamp int64) (*threadItem, error) {
	user, ok := ui.users.get(userID)
	if !ok {
		return &threadItem{}, errUnknownActor
	}

	action := " changed their name to "
	if user.id == ui.state.profile.id {
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

func (ui *ui) userChangedImage(id, userID uuid.UUID, timestamp int64) (*threadItem, error) {
	user, ok := ui.users.get(userID)
	if !ok {
		return &threadItem{}, errUnknownActor
	}

	changeString := user.getName() + " changed their profile image"
	if user.id == ui.state.profile.id {
		changeString = "You changed your profile image"
	}

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

func (ui *ui) newStatusChangeThreadItem(id, threadID, actorID uuid.UUID, frameType, action string, timestamp int64, seen bool) (*threadItem, error) {
	t, ok := ui.threads.get(threadID)
	if !ok {
		log.WithFields(log.Fields{
			"thread_id": threadID,
		}).Error("thread not found for status change thread item")
		return &threadItem{}, errUnknownThread
	}

	actor, ok := ui.users.get(actorID)
	if !ok {
		return &threadItem{}, errUnknownActor
	}
	actorName := actor.getName()
	if actorID == ui.state.profile.id {
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
			t.getButton().setLastAction(changeString, false) // TODO: possible to bind actor's name?
			t.getButton().setLastMessageTime(time.Unix(timestamp, 0))
			t.setLastMessageTime(timestamp)
			t.chatHistoryScroll().Refresh()
		},
		timestamp: timestamp,
	}, nil
}
