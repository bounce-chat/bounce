package ui

import (
	"time"

	"github.com/hkparker/bounce/chat"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

type group struct {
	id                                 uuid.UUID
	name                               binding.String
	users                              *userStore
	admins                             []uuid.UUID
	restrictUserManagement             bool
	restrictGroupEdits                 bool
	restrictPosting                    bool
	lastUserManagementPermissionUpdate int64
	lastGroupEditsPermissionUpdate     int64
	lastPostingPermissionUpdate        int64
	lastNameUpdate                     int64
	lastRetentionUpdate                int64
	pendingUsers                       *userStore
	notificationsMutedUntil            int64
	editContainer                      *fyne.Container
	retentionSelection                 *widget.Select
	view                               *fyne.Container
	header                             *fyne.Container
	button                             *threadButton
	notificationsEnabledCheck          *widget.Check
	restrictUserManagementCheck        *widget.Check
	restrictGroupEditsCheck            *widget.Check
	restrictPostingCheck               *widget.Check
	scroll                             *container.Scroll
	availableNewUsersScroll            *container.Scroll
	currentUsersContainer              *fyne.Container
	entry                              *threadEntry
	entryBar                           *fyne.Container
	lastMessage                        int64
}

func (group *group) getID() uuid.UUID {
	return group.id
}

func (group *group) getView() *fyne.Container {
	return group.view
}
func (group *group) getEntry() *threadEntry {
	return group.entry
}

func (group *group) chatHistoryScroll() *container.Scroll {
	return group.scroll
}

func (group *group) getButton() *threadButton {
	return group.button
}

func (group *group) getLastMessageTime() int64 {
	return group.lastMessage
}

func (group *group) setLastMessageTime(time int64) {
	group.lastMessage = time
}

func (group *group) getNotificationsMutedUntil() int64 {
	return group.notificationsMutedUntil
}

func (fyneUI *Fyne) amAdmin(g *group) bool {
	for _, id := range g.admins {
		if fyneUI.profile.id == id {
			return true
		}
	}

	return false
}

func (fyneUI *Fyne) OpenNewGroupChat(bounceGroup chat.Group) { // TODO: rename "create and open"?
	fyneUI.NewGroupChat(bounceGroup)

	group, exists := fyneUI.groups[bounceGroup.ID]
	if exists {
		fyneUI.showMainContainer()
		fyneUI.displayThread(group)
	} else {
		log.Error("cannot open newly created group because the UI isn't aware of it")
	}
}

func (fyneUI *Fyne) NewGroupChat(bounceGroup chat.Group) {
	if _, exists := fyneUI.groups[bounceGroup.ID]; exists {
		log.WithFields(log.Fields{
			"id":   bounceGroup.ID,
			"name": bounceGroup.Name,
		}).Warn("requested to create a group that already exists, ignored")
		return
	}

	group := &group{
		id:                      bounceGroup.ID,
		name:                    binding.NewString(),
		users:                   newUserStore(),
		admins:                  bounceGroup.Admins,
		restrictUserManagement:  bounceGroup.RestrictUserManagement,
		restrictGroupEdits:      bounceGroup.RestrictGroupEdits,
		restrictPosting:         bounceGroup.RestrictPosting,
		pendingUsers:            newUserStore(),
		scroll:                  container.NewVScroll(container.NewVBox()),
		availableNewUsersScroll: container.NewVScroll(container.NewVBox()),
		currentUsersContainer:   container.NewMax(),
		lastMessage:             time.Now().Unix(),
	}
	for _, userID := range bounceGroup.UserIDs {
		user, exists := fyneUI.users.get(userID)
		if !exists {
			log.WithFields(log.Fields{
				"user_id": userID,
			}).Error("attempted to create group with user unknown to UI")
			return
		} else {
			group.users.add(user)
		}
	}

	err := group.name.Set(bounceGroup.Name)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("data bindings are broken")
	}

	group.notificationsEnabledCheck = widget.NewCheck("Enable notifications", func(_ bool) {})
	group.notificationsMutedUntil, err = fyneUI.callbacks.GetGroupMutedUntil(group.id)
	if err != nil {
		log.WithFields(log.Fields{
			"group_id": group.id,
		}).Error("cannot find muted until for group")
		return
		// TODO: fatal?
	}
	enabled := group.notificationsMutedUntil != chat.MutedForever
	group.notificationsEnabledCheck.SetChecked(enabled)

	group.restrictUserManagementCheck = widget.NewCheck("Restrict User Management", func(_ bool) {})
	group.restrictGroupEditsCheck = widget.NewCheck("Restrict Group Edits", func(_ bool) {})
	group.restrictPostingCheck = widget.NewCheck("Restrict Posting", func(_ bool) {})
	group.restrictUserManagementCheck.SetChecked(group.restrictUserManagement)
	group.restrictGroupEditsCheck.SetChecked(group.restrictGroupEdits)
	group.restrictPostingCheck.SetChecked(group.restrictPosting)

	fyneUI.buildEditThreadContainer(group)
	editButton := widget.NewButton("Edit", func() {
		fyneUI.refreshUserSelections(group)
		fyneUI.showEditThreadContainer(group)
	})
	groupIconCanvas := canvas.NewImageFromResource(newEmbeddedResource("assets/not_found.png"))
	groupIconCanvas.FillMode = canvas.ImageFillContain
	groupIconCanvas.SetMinSize(fyne.NewSize(32, 32))

	groupLabelText := widget.NewLabel(bounceGroup.Name)
	groupLabel := container.NewHBox(
		groupIconCanvas,
		groupLabelText,
	)

	groupLabelText.Bind(group.name)
	groupLabelText.TextStyle = fyne.TextStyle{Bold: true}
	groupButtons := container.NewMax(editButton)
	group.header = container.New(
		layout.NewBorderLayout(nil, nil, groupLabel, groupButtons),
		groupLabel,
		groupButtons,
	)

	entry := newThreadEntry(5)
	group.entry = entry
	entry.OnChanged = func(_ string) {
		fyneUI.callbacks.TypingInGroup(group.id)
	}
	entry.customOnSubmitted = func() {
		fyneUI.callbacks.SendGroupMessage(chat.GroupMessage{
			Thread: group.id,
			Text:   entry.Text,
		})

		entry.Text = ""
		entry.Refresh()
	}

	group.entryBar = container.NewMax(entry)

	group.button = newThreadButton(todoImage(), bounceGroup.Name, func() {
		fyneUI.displayThread(group)
		fyneUI.callbacks.GroupConnectionDesired(group.id)
	})
	// Keep the last message time counter up to date
	go func() {
		for {
			time.Sleep(1 * time.Minute)
			group.button.updateLastMessageTimeText()
		}
	}()

	// Make sure chaning the name of the group also updates the thread button
	group.name.AddListener(binding.NewDataListener(func() {
		newName, err := group.name.Get()
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("data bindings are broken")
		}
		button := group.getButton()
		button.threadName.Segments[0].(*widget.TextSegment).Text = newName
		button.threadName.Refresh()
	}))

	group.view = container.New(
		layout.NewBorderLayout(group.header, group.entryBar, nil, nil),
		group.header,
		group.entryBar,
		group.scroll,
	)
	fyneUI.groups[group.id] = group
	fyneUI.refreshThreadOrder()
}

func (fyneUI *Fyne) DisplayGroupMessage(gm chat.GroupMessage) {
	g, exists := fyneUI.groups[gm.Thread]
	if !exists {
		log.WithFields(log.Fields{
			"group_id": gm.Thread,
		}).Error("group not found for group message")
		return
	}

	ti, err := fyneUI.newGroupMessage(gm)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for group message")
		return
	}

	fyneUI.threadWithMessageMutex.Lock()
	fyneUI.threadWithMessage[gm.ID] = g
	fyneUI.threadWithMessageMutex.Unlock()

	if !fyneUI.isActive(g) && !(gm.Author == fyneUI.profile.id) {
		g.button.addUnread()
	}

	fyneUI.appendThreadItem(g, ti)
}

func (fyneUI *Fyne) RenameGroup(ugn chat.UpdateGroupName) {
	g, exists := fyneUI.groups[ugn.Thread]
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugn.Thread,
		}).Error("cannot update name for unknown group")
		return
	}

	ti, err := fyneUI.newUpdateGroupName(ugn)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for updating group name")
		return
	}
	fyneUI.appendThreadItem(g, ti)

	if ugn.Timestamp > g.lastNameUpdate {
		g.lastNameUpdate = ugn.Timestamp
		err = g.name.Set(ugn.Name)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("data bindings are broken")
		}
	}
}

func (fyneUI *Fyne) GroupRetentionChanged(ugr chat.UpdateGroupRetention) {
	g, exists := fyneUI.groups[ugr.Thread]
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugr.Thread,
		}).Error("cannot update retention for unknown group")
		return
	}

	ti, err := fyneUI.newUpdateGroupRetention(ugr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for updating group retention")
		return
	}
	fyneUI.appendThreadItem(g, ti)

	if ugr.Timestamp > g.lastRetentionUpdate {
		g.lastRetentionUpdate = ugr.Timestamp
		g.retentionSelection.Selected = getRetentionName(ugr.Retention)
		g.retentionSelection.Refresh()
	}
}

func (fyneUI *Fyne) GroupChatHistoryCleared(ugch chat.UpdateGroupClearHistory) {
	g, exists := fyneUI.groups[ugch.Thread]
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugch.Thread,
		}).Error("cannot clear history for unknown group")
		return
	}

	ti, err := fyneUI.newUpdateGroupClearHistory(ugch)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for clearing group history")
		return
	}
	fyneUI.appendThreadItem(g, ti)

	// TODO: it's possible we cleared messages and there are messages newer than the clear time
	// that we want to preserve.  In that case, these messages will not have been removed from the
	// history scroll, and we should insert the message about the clearing above them, so it makes
	// sense.  This will require we know the timestamp of the clearing update in the frontend,
	// in fact everything that goes into a thread should follow an interface that has timestamps.
	// once that's done we can insertion sort these in.
}

func (fyneUI *Fyne) GroupMutedUntilChanged(groupID uuid.UUID, mutedUntil int64) {
	if group, exists := fyneUI.groups[groupID]; exists {
		group.notificationsMutedUntil = mutedUntil
		enabled := mutedUntil != chat.MutedForever
		group.notificationsEnabledCheck.SetChecked(enabled)
	} else {
		log.WithFields(log.Fields{
			"group_id": groupID,
		}).Warn("cannot notify messages cleared for group that doesn't exist")
	}
}

func (fyneUI *Fyne) AdminPromoted(ugap chat.UpdateGroupAdminPromoted) {
	g, exists := fyneUI.groups[ugap.Thread]
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugap.Thread,
		}).Error("cannot promote admin for group that doesn't exist")
		return
	}

	ti, err := fyneUI.newUpdateGroupAdminPromoted(ugap)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for update group admin promoted")
		return
	}
	fyneUI.appendThreadItem(g, ti)

	// TODO: set this user as an admin as long as this is the latest update
}

func (fyneUI *Fyne) AdminDemoted(ugad chat.UpdateGroupAdminDemoted) {
	g, exists := fyneUI.groups[ugad.Thread]
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugad.Thread,
		}).Error("cannot demote admin for group that doesn't exist")
		return
	}

	ti, err := fyneUI.newUpdateGroupAdminDemoted(ugad)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for update group admin demoted")
		return
	}
	fyneUI.appendThreadItem(g, ti)

	// TODO: remove this user as an admin as long as this is the latest update
}

func (fyneUI *Fyne) UserManagementRestricted(ugumr chat.UpdateGroupUserManagementRestricted) {
	g, exists := fyneUI.groups[ugumr.Thread]
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugumr.Thread,
		}).Error("cannot restrict user management for group that doesn't exist")
		return
	}

	ti, err := fyneUI.newUpdateGroupUserManagementRestricted(ugumr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for update group user management restricted")
		return
	}
	fyneUI.appendThreadItem(g, ti)

	if ugumr.Timestamp > g.lastUserManagementPermissionUpdate {
		g.lastUserManagementPermissionUpdate = ugumr.Timestamp
		g.restrictUserManagementCheck.SetChecked(true)
		g.restrictUserManagement = true
		// TODO: update the edit container so that non-admins can't do this
	}
}

func (fyneUI *Fyne) UserManagementUnrestricted(ugumu chat.UpdateGroupUserManagementUnrestricted) {
	g, exists := fyneUI.groups[ugumu.Thread]
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugumu.Thread,
		}).Error("cannot unrestrict user management for group that doesn't exist")
		return
	}

	ti, err := fyneUI.newUpdateGroupUserManagementUnrestricted(ugumu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for update group user management unrestricted")
		return
	}
	fyneUI.appendThreadItem(g, ti)

	if ugumu.Timestamp > g.lastUserManagementPermissionUpdate {
		g.lastUserManagementPermissionUpdate = ugumu.Timestamp
		g.restrictUserManagementCheck.SetChecked(false)
		g.restrictUserManagement = false
		// TODO: update the edit container so that non-admins can do this
	}
}

func (fyneUI *Fyne) GroupEditsRestricted(uger chat.UpdateGroupEditsRestricted) {
	g, exists := fyneUI.groups[uger.Thread]
	if !exists {
		log.WithFields(log.Fields{
			"group_id": uger.Thread,
		}).Error("cannot restrict edits for group that doesn't exist")
		return
	}

	ti, err := fyneUI.newUpdateGroupEditsRestricted(uger)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for update group edits restricted")
		return
	}
	fyneUI.appendThreadItem(g, ti)

	if uger.Timestamp > g.lastGroupEditsPermissionUpdate {
		g.lastGroupEditsPermissionUpdate = uger.Timestamp
		g.restrictGroupEditsCheck.SetChecked(true)
		g.restrictGroupEdits = true
		// TODO: update the edit container so that non-admins can't do this
	}
}

func (fyneUI *Fyne) GroupEditsUnrestricted(ugeu chat.UpdateGroupEditsUnrestricted) {
	g, exists := fyneUI.groups[ugeu.Thread]
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugeu.Thread,
		}).Error("cannot unrestrict edits for group that doesn't exist")
		return
	}

	ti, err := fyneUI.newUpdateGroupEditsUnrestricted(ugeu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for update group edits unrestricted")
		return
	}
	fyneUI.appendThreadItem(g, ti)

	if ugeu.Timestamp > g.lastGroupEditsPermissionUpdate {
		g.lastGroupEditsPermissionUpdate = ugeu.Timestamp
		g.restrictGroupEditsCheck.SetChecked(false)
		g.restrictGroupEdits = false
		// TODO: update the edit container so that non-admins can do this
	}
}

func (fyneUI *Fyne) PostingRestricted(ugpr chat.UpdateGroupPostingRestricted) {
	g, exists := fyneUI.groups[ugpr.Thread]
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugpr.Thread,
		}).Error("cannot restrict posting for group that doesn't exist")
		return
	}

	ti, err := fyneUI.newUpdateGroupPostingRestricted(ugpr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for update group posting restricted")
		return
	}
	fyneUI.appendThreadItem(g, ti)

	if ugpr.Timestamp > g.lastPostingPermissionUpdate {
		g.lastPostingPermissionUpdate = ugpr.Timestamp
		g.restrictPostingCheck.SetChecked(true)
		g.restrictPosting = true
		// TODO: update the edit container so that non-admins can't do this
	}
}

func (fyneUI *Fyne) PostingUnrestricted(ugpu chat.UpdateGroupPostingUnrestricted) {
	g, exists := fyneUI.groups[ugpu.Thread]
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugpu.Thread,
		}).Error("cannot unrestrict posting for group that doesn't exist")
		return
	}

	ti, err := fyneUI.newUpdateGroupPostingUnrestricted(ugpu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for update group posting unrestricted")
	}
	fyneUI.appendThreadItem(g, ti)

	if ugpu.Timestamp > g.lastPostingPermissionUpdate {
		g.lastPostingPermissionUpdate = ugpu.Timestamp
		g.restrictPostingCheck.SetChecked(false)
		g.restrictPosting = false
		// TODO: update the edit container so that non-admins can do this
	}
}
