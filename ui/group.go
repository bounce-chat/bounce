package ui

import (
	"errors"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/hkparker/bounce/chat"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

type group struct {
	id                          uuid.UUID
	name                        binding.String
	initial                     binding.String
	users                       *userStore
	admins                      []uuid.UUID
	blockedUsers                []uuid.UUID
	retention                   int64
	restrictUserManagement      bool
	restrictGroupEdits          bool
	restrictPosting             bool
	lastAdminActionMutex        sync.Mutex
	pendingUsers                *userStore
	notificationsMutedUntil     int64
	editContainer               *fyne.Container
	editThreadNameEntry         *widget.Entry
	retentionSelection          *widget.Select
	clearHistoryButton          *widget.Button
	leaveGroupButton            *widget.Button
	deleteGroupButton           *widget.Button
	blockGroupButton            *widget.Button
	addUsersButton              *widget.Button
	view                        *fyne.Container
	header                      *fyne.Container
	button                      *threadButton
	notificationsEnabledCheck   *widget.Check
	restrictUserManagementCheck *widget.Check
	restrictGroupEditsCheck     *widget.Check
	restrictPostingCheck        *widget.Check
	scroll                      *container.Scroll
	newUserSearchEntry          *widget.Entry
	availableNewUsersScroll     *container.Scroll
	currentUsersContainer       *container.Scroll
	pendingUsersContainer       *container.Scroll
	adminChecks                 map[uuid.UUID]*widget.Check
	adminChecksMutex            sync.Mutex
	removeUserButtons           map[uuid.UUID]*widget.Button
	removeUserButtonsMutex      sync.Mutex
	editUserDialogs             map[uuid.UUID]dialog.Dialog
	editUserDialogsMutex        sync.Mutex
	entry                       *threadEntry
	lastMessage                 int64
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

func (g *group) setInitial() {
	name, err := g.name.Get()
	if err != nil {
		log.Fatal(err.Error())
	}
	r, n := utf8.DecodeRuneInString(name)
	if r == utf8.RuneError {
		log.WithFields(log.Fields{
			"rune_error": r,
			"size":       n,
		}).Error("error setting group inital")
		return
	}
	g.initial.Set(string(r))
}

func (fyneUI *Fyne) getRemoveUserButton(g *group, userID uuid.UUID) *widget.Button {
	g.removeUserButtonsMutex.Lock()
	defer g.removeUserButtonsMutex.Unlock()

	buttonText := "Remove From Group"
	dialogHeader := "Remove User?"
	dialogPrompt := "Are you sure you want to remove this user from the group?"
	if userID == fyneUI.profile.id {
		buttonText = "Leave Group"
		dialogHeader = "Leave Group?"
		dialogPrompt = "Are you sure you want to leave this group?"
	}

	button, ok := g.removeUserButtons[userID]
	if ok {
		return button
	}

	confirmRemoveUser := dialog.NewConfirm(
		dialogHeader,
		dialogPrompt,
		func(confirmed bool) {
			if confirmed {
				err := fyneUI.callbacks.RemoveUser(g.id, userID)
				if err != nil {
					dialog.ShowError(errors.New("error removing user: "+err.Error()), fyneUI.mainWindow)
				} else {
					fyneUI.getEditUserDialog(g, userID).Hide()
					if fyneUI.profile.id == userID {
						fyneUI.showMainContainer()
					}
				}
			}
			fyneUI.activeDialog = nil
		},
		fyneUI.mainWindow,
	)

	button = widget.NewButton(buttonText, func() {
		fyneUI.activeDialog = confirmRemoveUser
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
		container.NewCenter(newDefaultImage(u.id, u.initials, 64, nil)), // TODO: get size from theme
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
					Name: u.getName(),
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
	userDetailsDialog = dialog.NewCustomConfirm(u.getName(), "Apply", "Cancel", editUserContainer, func(apply bool) { // TODO: add listener to update name when it changes
		if apply {
			// Update the admin status if needed
			if g.getAdminCheck(u.id).Checked && !g.isAdmin(u.id) {
				err := fyneUI.callbacks.PromoteAdmin(g.id, u.id)
				if err != nil {
					dialog.ShowError(errors.New("error promoting admin: "+err.Error()), fyneUI.mainWindow)
					g.getAdminCheck(u.id).SetChecked(false)
				}
			} else if !g.getAdminCheck(u.id).Checked && g.isAdmin(u.id) {
				err := fyneUI.callbacks.DemoteAdmin(g.id, u.id)
				if err != nil {
					dialog.ShowError(errors.New("error demoting admin: "+err.Error()), fyneUI.mainWindow)
					g.getAdminCheck(u.id).SetChecked(true)
				}
			}
		} else {
			// Reset everything
			g.getAdminCheck(u.id).SetChecked(g.isAdmin(u.id))
		}
		fyneUI.activeDialog = nil
		fyneUI.activeDialogCleanup = nil
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
		initial:                 binding.NewString(),
		users:                   newUserStore(),
		admins:                  bounceGroup.Admins,
		blockedUsers:            bounceGroup.BlockedUsers,
		retention:               bounceGroup.Retention,
		editThreadNameEntry:     widget.NewEntry(),
		restrictUserManagement:  bounceGroup.RestrictUserManagement,
		restrictGroupEdits:      bounceGroup.RestrictGroupEdits,
		restrictPosting:         bounceGroup.RestrictPosting,
		notificationsMutedUntil: bounceGroup.MutedUntil,
		pendingUsers:            newUserStore(),
		scroll:                  container.NewVScroll(container.NewVBox()),
		availableNewUsersScroll: container.NewVScroll(container.NewVBox()),
		currentUsersContainer:   container.NewVScroll(container.NewVBox()),
		pendingUsersContainer:   container.NewVScroll(container.NewVBox()),
		adminChecks:             make(map[uuid.UUID]*widget.Check),
		removeUserButtons:       make(map[uuid.UUID]*widget.Button),
		editUserDialogs:         make(map[uuid.UUID]dialog.Dialog),
		lastMessage:             time.Now().Unix(),
	}
	// TODO: if mobile, wrap the scroll

	for _, bu := range bounceGroup.Users {
		u, exists := fyneUI.users.get(bu.ID)
		if !exists {
			u := makeUser(bu.ID, bu.Name)
			fyneUI.users.add(u)
			group.users.add(u)
		} else {
			group.users.add(u)
		}
	}

	err := group.name.Set(bounceGroup.Name)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("data bindings are broken")
	}
	group.setInitial()
	group.name.AddListener(binding.NewDataListener(func() {
		group.setInitial()
	}))

	group.editThreadNameEntry.OnChanged = func(str string) {
		// Remove any leading whitespace
		str, trimmed := trimLeadingSpace(str)
		group.editThreadNameEntry.Text = str
		if trimmed != 0 {
			group.editThreadNameEntry.CursorRow = 0
			group.editThreadNameEntry.CursorColumn = 0
		}

		// Enforce length limit
		if utf8.RuneCountInString(str) > chat.MaximumNameLength {
			runes := []rune(str)
			truncated := runes[0:chat.MaximumNameLength]
			group.editThreadNameEntry.Text = string(truncated)

		}
		group.editThreadNameEntry.Refresh()
	}

	group.notificationsEnabledCheck = widget.NewCheck("Enable notifications", func(_ bool) {})
	enabled := group.notificationsMutedUntil != chat.MutedForever
	group.notificationsEnabledCheck.SetChecked(enabled)

	group.restrictUserManagementCheck = widget.NewCheck("Restrict User Management", func(_ bool) {})
	group.restrictGroupEditsCheck = widget.NewCheck("Restrict Group Edits", func(_ bool) {})
	group.restrictPostingCheck = widget.NewCheck("Restrict Posting", func(_ bool) {})
	group.restrictUserManagementCheck.SetChecked(group.restrictUserManagement)
	group.restrictGroupEditsCheck.SetChecked(group.restrictGroupEdits)
	group.restrictPostingCheck.SetChecked(group.restrictPosting)

	fyneUI.buildEditThreadContainer(group)
	editButton := widget.NewButtonWithIcon("", theme.MoreVerticalIcon(), func() {
		fyneUI.refreshUserSelections(group)
		fyneUI.showEditThreadContainer(group)
	})
	editButton.Importance = widget.LowImportance

	groupIconCanvas := newDefaultImage(group.id, group.initial, 32, nil) // TODO: get size from theme
	groupLabelText := widget.NewLabel(bounceGroup.Name)

	var groupLabel *fyne.Container
	if fyne.CurrentDevice().IsMobile() {
		backButton := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() {
			fyneUI.mobileBack()
		})
		backButton.Importance = widget.LowImportance
		groupLabel = container.NewHBox(
			backButton,
			groupIconCanvas,
			groupLabelText,
		)
	} else {
		groupLabel = container.NewHBox(
			groupIconCanvas,
			groupLabelText,
		)
	}

	groupLabelText.Bind(group.name)
	groupLabelText.TextStyle = fyne.TextStyle{Bold: true}
	group.header = container.New(
		layout.NewBorderLayout(nil, nil, groupLabel, editButton),
		groupLabel,
		editButton,
	)

	entry := newThreadEntry(5)
	group.entry = entry
	entry.OnChanged = func(_ string) {
		go fyneUI.callbacks.TypingInGroup(group.id)
	}
	entry.customOnSubmitted = func() {
		fyneUI.callbacks.SendGroupMessage(chat.GroupMessage{
			Thread: group.id,
			Text:   entry.Text,
		})

		entry.Text = ""
		entry.Refresh()

		group.chatHistoryScroll().ScrollToBottom()
		group.chatHistoryScroll().Refresh()
	}

	openThread := func() {
		fyneUI.displayThread(group)
		fyneUI.callbacks.GroupConnectionDesired(group.id)
		if fyne.CurrentDevice().IsMobile() {
			fyneUI.viewStack = append(fyneUI.viewStack, view{viewType: viewTypeThread, context: group.id})
		}
	}
	group.button = newThreadButton(newDefaultImage(group.id, group.initial, 64, openThread), group.name, openThread)

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
		layout.NewBorderLayout(group.header, group.entry, nil, nil),
		group.header,
		group.entry,
		container.New(&autoscollLayout{}, group.scroll),
	)
	fyneUI.groups[group.id] = group
	fyneUI.refreshThreadOrder()
	fyneUI.updateEnabledFeatures(group)

	ti, err := fyneUI.newGroupCreated(group.id, group.id, bounceGroup.CreatedBy, bounceGroup.CreatedAt)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
			"group": group.id,
		}).Warn("error created thread item for group creation")
	}
	fyneUI.appendThreadItem(group, ti)
}

func (fyneUI *Fyne) SetGroupState(bounceGroup chat.Group) {
	g, exists := fyneUI.groups[bounceGroup.ID]
	if !exists {
		log.WithFields(log.Fields{
			"group_id": bounceGroup.ID,
		}).Warn("cannot set state on unknown group")
		return
	}

	err := g.name.Set(bounceGroup.Name)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("data bindings are broken")
	}

	// TODO: set the image

	g.users.empty()
	for _, bu := range bounceGroup.Users {
		u, ok := fyneUI.users.get(bu.ID)
		if !ok {
			u := makeUser(bu.ID, bu.Name)
			fyneUI.users.add(u)
			g.users.add(u)
		}
		func(thisUser *user) {
			g.users.add(thisUser)
			g.pendingUsers.remove(u.id)
		}(u)
	}

	g.admins = bounceGroup.Admins

	for _, u := range g.users.userList {
		g.getAdminCheck(u.id).SetChecked(g.isAdmin(u.id))
		g.getAdminCheck(u.id).Refresh()
	}

	g.blockedUsers = bounceGroup.BlockedUsers

	g.retention = bounceGroup.Retention
	g.retentionSelection.Selected = getRetentionName(bounceGroup.Retention)
	g.retentionSelection.Refresh()

	g.notificationsMutedUntil = bounceGroup.MutedUntil
	enabled := g.notificationsMutedUntil != chat.MutedForever
	g.notificationsEnabledCheck.SetChecked(enabled)
	g.notificationsEnabledCheck.Refresh()

	g.restrictUserManagementCheck.SetChecked(bounceGroup.RestrictUserManagement)
	g.restrictUserManagement = bounceGroup.RestrictUserManagement

	g.restrictGroupEditsCheck.SetChecked(bounceGroup.RestrictGroupEdits)
	g.restrictGroupEdits = bounceGroup.RestrictGroupEdits

	g.restrictPostingCheck.SetChecked(bounceGroup.RestrictPosting)
	g.restrictPosting = bounceGroup.RestrictPosting

	fyneUI.updateEnabledFeatures(g)
	fyneUI.refreshUserSelections(g)
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

	u := makeUser(ugau.User.ID, ugau.User.Name)
	fyneUI.users.add(u) // TODO: this is needed since it isn't in the group state, should it be?

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
		fyneUI.activeThread = uuid.Nil
	}
	if rfg.Actor != fyneUI.profile.id {
		var actorName string
		actor, ok := fyneUI.users.get(rfg.Actor)
		if ok {
			actorName = actor.getName()
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

func (fyneUI *Fyne) GroupDeleted(gd chat.GroupDeleted) {
	if fyneUI.activeThread == gd.Group {
		fyneUI.chatContainer.Objects = []fyne.CanvasObject{fyneUI.defaultContainer}
		fyneUI.chatContainer.Refresh()
		fyneUI.activeThread = uuid.Nil
	}

	if gd.Actor != fyneUI.profile.id {
		var actorName string
		if gd.Actor == fyneUI.profile.id {
			actorName = "You"
		} else {
			actor, ok := fyneUI.users.get(gd.Actor)
			if ok {
				actorName = actor.getName()
			} else {
				log.WithFields(log.Fields{
					"actor": gd.Actor,
					"group": gd.Group,
				}).Error("unknown user deleted group")
				actorName = "unknown user"
			}
		}

		g, ok := fyneUI.groups[gd.Group]
		if !ok {
			log.WithFields(log.Fields{
				"group": gd.Group,
			}).Error("unknown group deleted")
			return
		}
		groupName, err := g.name.Get()
		if err != nil {
			log.Fatal("data bindings are broken")
		}
		dialog.ShowInformation("Group Deleted", actorName+" deleted the group \""+groupName+"\"", fyneUI.mainWindow)
	}

	delete(fyneUI.groups, gd.Group)
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
}

func (fyneUI *Fyne) UserBlockedGroup(ubg chat.UserBlockedGroup) {
	g, exists := fyneUI.groups[ubg.Thread]
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ubg.Thread,
		}).Error("cannot display block for group that doesn't exist")
		return
	}

	ti, err := fyneUI.newUpdateGroupUserBlocked(ubg)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for user blocked group")
	}
	fyneUI.appendThreadItem(g, ti)
}

func (fyneUI *Fyne) updateEnabledFeatures(g *group) {
	amAdmin := g.isAdmin(fyneUI.profile.id)

	if amAdmin {
		g.restrictUserManagementCheck.Enable()
		g.restrictGroupEditsCheck.Enable()
		g.restrictPostingCheck.Enable()
		g.deleteGroupButton.Enable()

		g.adminChecksMutex.Lock()
		for _, check := range g.adminChecks {
			check.Enable()
		}
		g.adminChecksMutex.Unlock()
	} else {
		g.restrictUserManagementCheck.Disable()
		g.restrictGroupEditsCheck.Disable()
		g.restrictPostingCheck.Disable()
		g.deleteGroupButton.Disable()

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
