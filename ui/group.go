package ui

import (
	"sync"
	"time"

	"github.com/hkparker/bounce/chat"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/dialog"
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
	lastAdminAction                    map[uuid.UUID]int64
	lastAdminActionMutex               sync.Mutex
	pendingUsers                       *userStore
	notificationsMutedUntil            int64
	editContainer                      *fyne.Container
	editThreadNameEntry                *widget.Entry
	retentionSelection                 *widget.Select
	clearHistoryButton                 *widget.Button
	addUsersButton                     *widget.Button
	view                               *fyne.Container
	header                             *fyne.Container
	button                             *threadButton
	notificationsEnabledCheck          *widget.Check
	restrictUserManagementCheck        *widget.Check
	restrictGroupEditsCheck            *widget.Check
	restrictPostingCheck               *widget.Check
	scroll                             *container.Scroll
	availableNewUsersScroll            *container.Scroll
	currentAdminsContainer             *fyne.Container
	currentUsersContainer              *fyne.Container
	pendingUsersContainer              *fyne.Container
	adminChecks                        map[uuid.UUID]*widget.Check
	adminChecksMutex                   sync.Mutex
	removeUserButtons                  map[uuid.UUID]*widget.Button
	removeUserButtonsMutex             sync.Mutex
	editUserDialogs                    map[uuid.UUID]dialog.Dialog
	editUserDialogsMutex               sync.Mutex
	entry                              *threadEntry
	entryBar                           *fyne.Container
	lastMessage                        int64
}

func (g *group) getID() uuid.UUID {
	return g.id
}

func (g *group) getView() *fyne.Container {
	return g.view
}
func (g *group) getEntry() *threadEntry {
	return g.entry
}

func (g *group) chatHistoryScroll() *container.Scroll {
	return g.scroll
}

func (g *group) getButton() *threadButton {
	return g.button
}

func (g *group) getLastMessageTime() int64 {
	return g.lastMessage
}

func (g *group) setLastMessageTime(time int64) {
	g.lastMessage = time
}

func (g *group) getNotificationsMutedUntil() int64 {
	return g.notificationsMutedUntil
}

func (g *group) getLastAdminAction(userID uuid.UUID) int64 {
	g.lastAdminActionMutex.Lock()
	defer g.lastAdminActionMutex.Unlock()

	lastTime, ok := g.lastAdminAction[userID]
	if !ok {
		return 0
	}
	return lastTime
}

func (g *group) setLastAdminAction(userID uuid.UUID, timestamp int64) {
	g.lastAdminActionMutex.Lock()
	defer g.lastAdminActionMutex.Unlock()

	g.lastAdminAction[userID] = timestamp
}

func (g *group) isAdmin(userID uuid.UUID) bool {
	for _, id := range g.admins {
		if id == userID {
			return true
		}
	}
	return false
}

func (g *group) getAdminCheck(userID uuid.UUID) *widget.Check {
	g.adminChecksMutex.Lock()
	defer g.adminChecksMutex.Unlock()

	check, ok := g.adminChecks[userID]
	if ok {
		return check
	}

	check = widget.NewCheck("Admin", func(set bool) {})
	check.SetChecked(g.isAdmin(userID))
	g.adminChecks[userID] = check
	return check
}

func (fyneUI *Fyne) getRemoveUserButton(g *group, userID uuid.UUID) *widget.Button {
	g.removeUserButtonsMutex.Lock()
	defer g.removeUserButtonsMutex.Unlock()

	button, ok := g.removeUserButtons[userID]
	if ok {
		return button
	}

	confirmRemoveUser := dialog.NewConfirm(
		"Remove user?",
		"Are you sure you want to remove this user from the group?",
		func(confirmed bool) {
			if confirmed {
				fyneUI.callbacks.RemoveUser(g.id, userID)
				fyneUI.getEditUserDialog(g, userID).Hide()
			}
		},
		fyneUI.mainWindow,
	)

	button = widget.NewButton("Remove from group", func() {
		confirmRemoveUser.Show()
	})
	g.removeUserButtons[userID] = button
	return button
}

func (fyneUI *Fyne) getEditUserDialog(g *group, userID uuid.UUID) dialog.Dialog {
	g.editUserDialogsMutex.Lock()
	g.editUserDialogsMutex.Unlock()

	d, ok := g.editUserDialogs[userID]
	if ok {
		return d
	}

	u, ok := g.users.get(userID)
	if !ok {
		log.WithFields(log.Fields{
			"group_id": g.id,
			"user_id":  userID,
		}).Fatal("cannot create edit user dialog for user not in group")
	}

	var userDetailsDialog dialog.Dialog

	editUserContainer := container.NewVBox(
		widget.NewLabel(u.name),
		widget.NewButton("Direct Message", func() {
			// Close the dialog
			if userDetailsDialog == nil {
				log.Fatal("userDetailsDialog used before assignment, this should be impossible")
			}
			userDetailsDialog.Hide()

			// Show the DM
			dm, dmExists := fyneUI.dms[u.id]
			if !dmExists {
				fyneUI.NewDirectMessage(chat.User{
					ID:   u.id,
					Name: u.name,
				})
				dm, dmExists = fyneUI.dms[u.id]
				if !dmExists {
					log.Fatal("DM doesn't exist immediately after creation")
				}
			}
			fyneUI.showMainContainer()
			fyneUI.callbacks.UserConnectionDesired(u.id)
			fyneUI.displayThread(dm)
		}),
		fyneUI.getRemoveUserButton(g, u.id),
		g.getAdminCheck(u.id),
	)
	userDetailsDialog = dialog.NewCustomConfirm(u.name, "Apply", "Cancel", editUserContainer, func(apply bool) {
		if apply {
			// Update the admin status if needed
			if g.getAdminCheck(u.id).Checked && !g.isAdmin(u.id) {
				fyneUI.callbacks.PromoteAdmin(g.id, u.id)
				fyneUI.refreshCurrentAndPendingUsers(g)
			} else if !g.getAdminCheck(u.id).Checked && g.isAdmin(u.id) {
				fyneUI.callbacks.DemoteAdmin(g.id, u.id)
				fyneUI.refreshCurrentAndPendingUsers(g)
			}
		} else {
			// Reset everything
			g.getAdminCheck(u.id).SetChecked(g.isAdmin(u.id))
		}
	}, fyneUI.mainWindow)

	g.editUserDialogs[userID] = userDetailsDialog

	return userDetailsDialog
}

func (fyneUI *Fyne) addAdmin(g *group, userID uuid.UUID) {
	alreadyAdded := false
	for _, id := range g.admins {
		if id == userID {
			alreadyAdded = true
		}
	}
	if !alreadyAdded {
		g.admins = append(g.admins, userID)
	}

	fyneUI.updateEnabledFeatures(g)
}

func (fyneUI *Fyne) removeAdmin(g *group, userID uuid.UUID) {
	adminsWithoutUser := []uuid.UUID{}
	for _, id := range g.admins {
		if id != userID {
			adminsWithoutUser = append(adminsWithoutUser, id)
		}
	}
	g.admins = adminsWithoutUser

	fyneUI.updateEnabledFeatures(g)
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
		return
	}

	group := &group{
		id:                      bounceGroup.ID,
		name:                    binding.NewString(),
		users:                   newUserStore(),
		admins:                  bounceGroup.Admins,
		editThreadNameEntry:     widget.NewEntry(),
		lastAdminAction:         make(map[uuid.UUID]int64),
		restrictUserManagement:  bounceGroup.RestrictUserManagement,
		restrictGroupEdits:      bounceGroup.RestrictGroupEdits,
		restrictPosting:         bounceGroup.RestrictPosting,
		pendingUsers:            newUserStore(),
		scroll:                  container.NewVScroll(container.NewVBox()),
		availableNewUsersScroll: container.NewVScroll(container.NewVBox()),
		currentAdminsContainer:  container.NewMax(),
		currentUsersContainer:   container.NewMax(),
		pendingUsersContainer:   container.NewMax(),
		adminChecks:             make(map[uuid.UUID]*widget.Check),
		removeUserButtons:       make(map[uuid.UUID]*widget.Button),
		editUserDialogs:         make(map[uuid.UUID]dialog.Dialog),
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
		}).Fatal("cannot find muted until for group")
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
	fyneUI.updateEnabledFeatures(group)
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

func (fyneUI *Fyne) AddUser(ugau chat.UpdateGroupAddUser) {
	g, exists := fyneUI.groups[ugau.Thread]
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugau.Thread,
		}).Error("group not found for update group add user")
		return
	}

	ti, err := fyneUI.newUpdateGroupAddUser(ugau)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for adding user to group")
		return
	}

	u := &user{
		id:   ugau.User.ID,
		name: ugau.User.Name,
	}
	fyneUI.users.add(u)
	g.users.add(u)
	g.pendingUsers.remove(u.id)
	fyneUI.refreshUserSelections(g)

	fyneUI.appendThreadItem(g, ti)
}

func (fyneUI *Fyne) RemoveUser(ugru chat.UpdateGroupRemoveUser) {
	g, exists := fyneUI.groups[ugru.Thread]
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugru.Thread,
		}).Error("cannot remove user from unknown group")
		return
	}

	g.users.remove(ugru.User)
	fyneUI.removeAdmin(g, ugru.User)
	fyneUI.refreshUserSelections(g)

	ti, err := fyneUI.newUpdateGroupRemoveUser(ugru)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for removing user to group")
		return
	}

	fyneUI.appendThreadItem(g, ti)
}

func (fyneUI *Fyne) RemovedFromGroup(rfg chat.RemovedFromGroup) {
	if fyneUI.activeThread == rfg.Group {
		fyneUI.chatContainer.Objects = []fyne.CanvasObject{fyneUI.defaultContainer}
		fyneUI.chatContainer.Refresh()
	}
	if rfg.Actor != fyneUI.profile.id {
		var actorName string
		actor, ok := fyneUI.users.get(rfg.Actor)
		if ok {
			actorName = actor.name
		} else {
			log.WithFields(log.Fields{
				"actor": rfg.Actor,
				"group": rfg.Group,
			}).Error("unknown user removed us from a group")
			actorName = "unknown user"
		}
		g, ok := fyneUI.groups[rfg.Group]
		if !ok {
			log.WithFields(log.Fields{
				"group": rfg.Group,
			}).Error("removed from unknown group")
			return
		}
		groupName, err := g.name.Get()
		if err != nil {
			log.Fatal("data bindings are broken")
		}
		dialog.ShowInformation("Removed From Group", actorName+" removed you from "+groupName, fyneUI.mainWindow)
	}
	delete(fyneUI.groups, rfg.Group)
	fyneUI.refreshThreadOrder()
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

	if ugap.Timestamp > g.getLastAdminAction(ugap.UserID) {
		g.setLastAdminAction(ugap.UserID, ugap.Timestamp)
		fyneUI.addAdmin(g, ugap.UserID)
	}
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

	if ugad.Timestamp > g.getLastAdminAction(ugad.UserID) {
		g.setLastAdminAction(ugad.UserID, ugad.Timestamp)
		fyneUI.removeAdmin(g, ugad.UserID)
	}
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
		fyneUI.updateEnabledFeatures(g)
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
		fyneUI.updateEnabledFeatures(g)
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
		fyneUI.updateEnabledFeatures(g)
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
		fyneUI.updateEnabledFeatures(g)
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
		fyneUI.updateEnabledFeatures(g)
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
		fyneUI.updateEnabledFeatures(g)
	}
}

func (fyneUI *Fyne) updateEnabledFeatures(g *group) {
	amAdmin := g.isAdmin(fyneUI.profile.id)

	if amAdmin {
		g.restrictUserManagementCheck.Enable()
		g.restrictGroupEditsCheck.Enable()
		g.restrictPostingCheck.Enable()

		g.adminChecksMutex.Lock()
		for _, check := range g.adminChecks {
			check.Enable()
		}
		g.adminChecksMutex.Unlock()
	} else {
		g.restrictUserManagementCheck.Disable()
		g.restrictGroupEditsCheck.Disable()
		g.restrictPostingCheck.Disable()

		if len(g.admins) > 0 {
			g.adminChecksMutex.Lock()
			for _, check := range g.adminChecks {
				check.Disable()
			}
			g.adminChecksMutex.Unlock()
		}
	}

	if g.restrictUserManagement && !amAdmin {
		g.addUsersButton.Disable()

		g.removeUserButtonsMutex.Lock()
		for _, button := range g.removeUserButtons {
			button.Disable()
		}
		g.removeUserButtonsMutex.Unlock()
	} else {
		g.addUsersButton.Enable()

		g.removeUserButtonsMutex.Lock()
		for _, button := range g.removeUserButtons {
			button.Enable()
		}
		g.removeUserButtonsMutex.Unlock()
	}

	if g.restrictGroupEdits && !amAdmin {
		g.editThreadNameEntry.Disable()
		g.retentionSelection.Disable()
		g.clearHistoryButton.Disable()
	} else {
		g.editThreadNameEntry.Enable()
		g.retentionSelection.Enable()
		g.clearHistoryButton.Enable()
	}

	if g.restrictPosting && !amAdmin {
		g.entry.Disable()
	} else {
		g.entry.Enable()
	}
}
