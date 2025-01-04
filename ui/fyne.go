package ui

import (
	"runtime"
	"sync"
	"time"

	"github.com/hkparker/bounce/chat"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/mobile"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

const networkStateStarting = 0
const networkStateOnline = 1
const networkStateOffline = 2

const setupStepInit = 0
const setupStepProfile = 1

const defaultHeight = float32(600)
const defaultWidth = float32(800)

//
// An implementation of the Bounce chat.UI interface using Fyne
//
type Fyne struct {
	app                                   fyne.App
	mainWindow                            fyne.Window
	mainContainer                         *fyne.Container
	defaultContainer                      *fyne.Container
	newInstall                            *fyne.Container
	newSyncDevice                         *fyne.Container
	nameNewDevice                         *fyne.Container
	addUser                               *fyne.Container
	newProfileCreator                     *fyne.Container
	databaseLoading                       *fyne.Container
	editProfile                           *fyne.Container
	displaySyncString                     *fyne.Container
	displayAddUserString                  *fyne.Container
	settingsContainer                     *fyne.Container
	about                                 *fyne.Container
	newGroup                              *fyne.Container
	newDM                                 *fyne.Container
	introduceContacts                     *fyne.Container
	importContact                         *fyne.Container
	threadVBox                            *fyne.Container
	chatContainer                         *fyne.Container
	mobileMenu                            *fyne.Container
	newSyncDeviceWidgets                  *newSyncDeviceWidgets
	profileNameEntry                      *widget.Entry
	profileIcon                           *fyne.Container
	profileOptions                        *fyne.Container
	newDMUserSearchEntry                  *widget.Entry
	newGroupNameEntry                     *widget.Entry
	newGroupSelectedUsersContainer        *container.Scroll
	newGroupPendingUsersList              *container.Scroll
	newGroupAllAvailableUsers             *container.Scroll
	newGroupUserSearchEntry               *widget.Entry
	newGroupSelectedUsers                 *userStore
	newGroupPendingUsers                  *userStore
	newGroupPendingAdmins                 map[uuid.UUID]bool
	newGroupCreateButton                  *widget.Button
	newGroupAddAdminsButton               *widget.Button
	newGroupAddUsersButton                *widget.Button
	newGroupRetentionSelection            *widget.Select
	newGroupUserManagementRestrictedCheck *widget.Check
	newGroupGroupEditsRestrictedCheck     *widget.Check
	newGroupPostingRestrictedCheck        *widget.Check
	mainMenu                              *fyne.MainMenu
	allUsersDMLinksScroll                 *container.Scroll
	networkOfflineWarning                 *widget.Label
	groups                                map[uuid.UUID]*group
	dms                                   map[uuid.UUID]*directMessage
	threadWithItem                        map[uuid.UUID]thread
	threadWithItemMutex                   sync.Mutex
	activeThread                          uuid.UUID
	syncString                            binding.String
	addUserString                         binding.String
	profile                               *user
	settings                              chat.Settings
	settingsWidgets                       *settingsWidgets
	users                                 *userStore
	devices                               *deviceStore
	currentDevices                        *container.Scroll
	deletedUser                           *user
	initialStateSet                       bool
	initialSyncIncomplete                 bool
	focused                               bool
	networkState                          int
	setupStep                             int
	viewStack                             []view
	activeDialog                          dialog.Dialog
	activeDialogCleanup                   func()
	callbacks                             chat.UICallbacks
}

func (fyneUI *Fyne) Build(configDirectory string, callbacks chat.UICallbacks) {
	//
	// Hookup callbacks
	//
	fyneUI.callbacks = callbacks

	//
	// Initialize types that require it
	//
	fyneUI.groups = make(map[uuid.UUID]*group)
	fyneUI.dms = make(map[uuid.UUID]*directMessage)
	fyneUI.threadWithItem = make(map[uuid.UUID]thread)
	fyneUI.users = newUserStore()
	fyneUI.devices = newDeviceStore()
	fyneUI.focused = true
	fyneUI.syncString = binding.NewString()
	fyneUI.addUserString = binding.NewString()
	fyneUI.networkOfflineWarning = widget.NewLabelWithStyle("network is starting...", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	fyneUI.networkOfflineWarning.Show()
	fyneUI.deletedUser = makeUser(uuid.Nil, "-deleted-")
	fyneUI.viewStack = []view{}

	//
	// Define the app
	//
	fyneUI.app = app.NewWithID("chat.bounce")
	fyneUI.app.SetIcon(newEmbeddedResource("assets/icon.png"))
	fyneUI.app.Lifecycle().SetOnEnteredForeground(func() { fyneUI.focused = true })
	fyneUI.app.Lifecycle().SetOnExitedForeground(func() { fyneUI.focused = false })
	fyneUI.app.Settings().SetTheme(&forcedVariant{Theme: theme.DefaultTheme(), variant: theme.VariantDark})

	//
	// Define the main window
	//
	fyneUI.mainWindow = fyneUI.app.NewWindow("Bounce")
	fyneUI.mainWindow.SetMaster()
	fyneUI.mainWindow.SetCloseIntercept(func() {
		// There's some bug in Fyne where the app will hang when the close button
		// is hit unless this is explicitly set https://github.com/fyne-io/fyne/issues/2314
		// TODO: maybe we actually want to use this to display a closing message and wait for the network to go offline
		fyneUI.Quit()
	})
	fyneUI.mainWindow.Resize(fyne.Size{Height: defaultHeight, Width: defaultWidth})
	fyneUI.mainWindow.Canvas().SetOnTypedKey(func(ev *fyne.KeyEvent) {
		if fyne.CurrentDevice().IsMobile() && ev.Name == mobile.KeyBack {
			fyneUI.mobileBack()
		}
	})

	fyneUI.mainWindow.Show()

	//
	// Build all the containers
	//
	fyneUI.buildMenu()
	fyneUI.buildNewSyncDeviceWidgets()
	fyneUI.buildNewInstall()
	fyneUI.buildNewSyncDevice()
	fyneUI.buildNameNewDevice()
	fyneUI.buildAddUser()
	fyneUI.buildNewProfileCreator()
	fyneUI.buildDatabaseLoading()
	fyneUI.buildEditProfile()
	fyneUI.buildDisplaySyncString()
	fyneUI.buildDisplayAddUserString()
	fyneUI.buildSettings()
	fyneUI.buildAbout()
	fyneUI.buildNewGroup()
	fyneUI.buildNewDM()
	fyneUI.buildImportContact()
	fyneUI.buildMainContainer()
	fyneUI.buildMobileMenu()

	//
	// Default to displaying the loading container
	//
	fyneUI.networkState = networkStateStarting
	fyneUI.showMainContainer()
	fyneUI.askToIgnoreBatteryOptimizations()
}

func (fyneUI *Fyne) askToIgnoreBatteryOptimizations() {
	if fyneUI.settings.NeverAskForBatteryOptimizations {
		return
	}

	if runtime.GOOS == "android" {
		if !batteryOptimizationsIgnored() {
			var batteryDialog dialog.Dialog

			batteryText := widget.NewRichTextWithText("Bounce can only notify you of new messages if it's running in the background.  Click \"Allow\" to be prompted to add Bounce to Android's list of allowed background apps.")
			batteryText.Wrapping = fyne.TextWrapWord
			batteryAllow := widget.NewButton("Allow", func() {
				batteryDialog.Hide()
				fyneUI.activeDialog = nil
				fyneUI.activeDialogCleanup = nil
				requestIgnoreBatteryOptimizations()
			})
			batteryAllow.Importance = widget.HighImportance
			batteryLater := widget.NewButton("Later", func() {
				batteryDialog.Hide()
				fyneUI.activeDialog = nil
				fyneUI.activeDialogCleanup = nil
			})
			batteryLater.Importance = widget.LowImportance
			batteryNever := widget.NewButton("Never", func() {
				batteryDialog.Hide()
				fyneUI.activeDialog = nil
				fyneUI.activeDialogCleanup = nil
				fyneUI.callbacks.NeverAskForBatteryOptimizations()
			})
			batteryNever.Importance = widget.LowImportance

			batteryContent := container.NewVBox(
				batteryText,
				batteryAllow,
				batteryLater,
				batteryNever,
			)

			batteryDialog = dialog.NewCustomWithoutButtons(
				"Allow Backgrounding",
				batteryContent,
				fyneUI.mainWindow,
			)
			fyneUI.activeDialog = batteryDialog
			fyneUI.activeDialogCleanup = nil
			batteryDialog.Show()
		}
	}
}

func (fyneUI *Fyne) Run() {
	fyneUI.app.Run()
}

func (fyneUI *Fyne) Quit() {
	fyneUI.app.Quit()
}

func (fyneUI *Fyne) LoadInitialState(state chat.InitialState) {
	if fyneUI.initialStateSet {
		log.Fatal("the initial state of the UI can only be loaded once")
	}
	// Refresh the state of the main view after the state is loaded
	fyneUI.initialStateSet = true
	defer fyneUI.showMainContainer()

	if state.Profile == nil {
		// This is a brand new installation.  Tell the user interface it must have the
		// user create a profile or sync this device to an existing device group before
		// the app can be used.
		// TODO: log fatal if anything else is set
		return
	} else {
		fyneUI.profile = makeUser(state.Profile.ID, state.Profile.Name)
		fyneUI.users.add(fyneUI.profile)
	}
	fyneUI.settings = state.Settings

	for _, dev := range state.SyncDevices {
		fyneUI.devices.add(&chat.Device{
			ID:        dev.ID,
			Name:      dev.Name,
			Address:   dev.Address,
			CreatedAt: dev.CreatedAt,
			Local:     dev.Local,
			Online:    dev.Online,
		})
	}
	fyneUI.updateDeviceStatus()

	initialDMStates := map[uuid.UUID]chat.DMState{}
	for _, u := range state.Users {
		uiUser := makeUser(u.ID, u.Name)
		fyneUI.users.add(uiUser)
		initialDMStates[u.ID] = u.State
	}

	for _, g := range state.Groups {
		fyneUI.NewGroupChat(g)
		group, exists := fyneUI.groups[g.ID]
		if !exists {
			log.Fatal("group doesn't exist immediately after creation")
		}
		group.button.setLastMessageTime(time.Unix(g.LastActivity, 0))
		group.setLastMessageTime(g.LastActivity)
	}

	dmItems := make(map[uuid.UUID]threadItems)
	groupItems := make(map[uuid.UUID]threadItems)

	for _, dm := range state.DirectMessages {
		fyneUI.creatDMIfNeeded(dm.Thread, initialDMStates)
		dmti, err := fyneUI.newDirectMessage(dm)
		if err != nil {
			log.Fatal(err.Error())
		}
		dmItems[dm.Thread] = append(dmItems[dm.Thread], dmti)
	}

	for _, udmr := range state.UpdateDMRetentions {
		fyneUI.creatDMIfNeeded(udmr.Thread, initialDMStates)
		udmrItem, err := fyneUI.newUpdateDMRetention(udmr)
		if err != nil {
			log.Fatal(err.Error())
		}
		dmItems[udmr.Thread] = append(dmItems[udmr.Thread], udmrItem)
	}

	for _, udmch := range state.UpdateDMClearHistories {
		fyneUI.creatDMIfNeeded(udmch.Thread, initialDMStates)
		udmchItem, err := fyneUI.newUpdateDMClearHistory(udmch)
		if err != nil {
			log.Fatal(err.Error())
		}
		dmItems[udmch.Thread] = append(dmItems[udmch.Thread], udmchItem)
	}

	for _, gm := range state.GroupMessages {
		mti, err := fyneUI.newGroupMessage(gm)
		if err != nil {
			log.Fatal(err.Error())
		}
		groupItems[gm.Thread] = append(groupItems[gm.Thread], mti)
	}

	for _, ugr := range state.UpdateGroupRetentions {
		ugrItem, err := fyneUI.newUpdateGroupRetention(ugr)
		if err != nil {
			log.Fatal(err.Error())
		}
		groupItems[ugr.Thread] = append(groupItems[ugr.Thread], ugrItem)
	}

	for _, ugn := range state.UpdateGroupNames {
		ugnItem, err := fyneUI.newUpdateGroupName(ugn)
		if err != nil {
			log.Fatal(err.Error())
		}
		groupItems[ugn.Thread] = append(groupItems[ugn.Thread], ugnItem)
	}

	for _, ugau := range state.UpdateGroupAddUsers {
		ugauItem, err := fyneUI.newUpdateGroupAddUser(ugau)
		if err != nil {
			log.Fatal(err.Error())
		}
		groupItems[ugau.Thread] = append(groupItems[ugau.Thread], ugauItem)
	}

	for _, ugru := range state.UpdateGroupRemoveUsers {
		ugruItem, err := fyneUI.newUpdateGroupRemoveUser(ugru)
		if err != nil {
			log.Fatal(err.Error())
		}
		groupItems[ugru.Thread] = append(groupItems[ugru.Thread], ugruItem)
	}

	for _, ugch := range state.UpdateGroupClearHistories {
		ugchItem, err := fyneUI.newUpdateGroupClearHistory(ugch)
		if err != nil {
			log.Fatal(err.Error())
		}
		groupItems[ugch.Thread] = append(groupItems[ugch.Thread], ugchItem)
	}

	for _, ugap := range state.UpdateGroupAdminPromotions {
		ugapItem, err := fyneUI.newUpdateGroupAdminPromoted(ugap)
		if err != nil {
			log.Fatal(err.Error())
		}
		groupItems[ugap.Thread] = append(groupItems[ugap.Thread], ugapItem)
	}

	for _, ugad := range state.UpdateGroupAdminDemotions {
		ugadItem, err := fyneUI.newUpdateGroupAdminDemoted(ugad)
		if err != nil {
			log.Fatal(err.Error())
		}
		groupItems[ugad.Thread] = append(groupItems[ugad.Thread], ugadItem)
	}

	for _, ugumr := range state.UpdateGroupUserManagementsRestricted {
		ugumrItem, err := fyneUI.newUpdateGroupUserManagementRestricted(ugumr)
		if err != nil {
			log.Fatal(err.Error())
		}
		groupItems[ugumr.Thread] = append(groupItems[ugumr.Thread], ugumrItem)
	}

	for _, ugumu := range state.UpdateGroupUserManagementsUnrestricted {
		ugumuItem, err := fyneUI.newUpdateGroupUserManagementUnrestricted(ugumu)
		if err != nil {
			log.Fatal(err.Error())
		}
		groupItems[ugumu.Thread] = append(groupItems[ugumu.Thread], ugumuItem)
	}

	for _, uger := range state.UpdateGroupEditsRestricted {
		ugerItem, err := fyneUI.newUpdateGroupEditsRestricted(uger)
		if err != nil {
			log.Fatal(err.Error())
		}
		groupItems[uger.Thread] = append(groupItems[uger.Thread], ugerItem)
	}

	for _, ugeu := range state.UpdateGroupEditsUnrestricted {
		ugeuItem, err := fyneUI.newUpdateGroupEditsUnrestricted(ugeu)
		if err != nil {
			log.Fatal(err.Error())
		}
		groupItems[ugeu.Thread] = append(groupItems[ugeu.Thread], ugeuItem)
	}

	for _, ugpr := range state.UpdateGroupPostingsRestricted {
		ugprItem, err := fyneUI.newUpdateGroupPostingRestricted(ugpr)
		if err != nil {
			log.Fatal(err.Error())
		}
		groupItems[ugpr.Thread] = append(groupItems[ugpr.Thread], ugprItem)
	}

	for _, ugpu := range state.UpdateGroupPostingsUnrestricted {
		ugpuItem, err := fyneUI.newUpdateGroupPostingUnrestricted(ugpu)
		if err != nil {
			log.Fatal(err.Error())
		}
		groupItems[ugpu.Thread] = append(groupItems[ugpu.Thread], ugpuItem)
	}

	for _, uuun := range state.UpdateUserUpdateNames {
		if uuun.User == fyneUI.profile.id {
			for dmID, _ := range fyneUI.dms { // TODO: don't add to DMs before the DMs should exist
				uuunItem, err := fyneUI.userChangedName(uuun.ID, uuun.User, uuun.OldName, uuun.Name, uuun.Timestamp)
				if err != nil {
					log.Fatal(err.Error())
				} else {
					dmItems[dmID] = append(dmItems[dmID], uuunItem)
				}
			}
			for _, g := range fyneUI.groups {
				if uuun.Timestamp < g.createdAt {
					continue
				}
				uuunItem, err := fyneUI.userChangedName(uuun.ID, uuun.User, uuun.OldName, uuun.Name, uuun.Timestamp)
				if err != nil {
					log.Fatal(err.Error())
				} else {
					groupItems[g.id] = append(groupItems[g.id], uuunItem)
				}
			}
		} else {
			uuunItem, err := fyneUI.userChangedName(uuun.ID, uuun.User, uuun.OldName, uuun.Name, uuun.Timestamp)
			if err != nil {
				log.Fatal(err.Error())
			}
			dmItems[uuun.User] = append(dmItems[uuun.User], uuunItem)

			for _, g := range fyneUI.groups {
				if uuun.Timestamp < g.createdAt {
					continue
				}
				if g.users.contains(uuun.User) {
					uuunGroupItem, err := fyneUI.userChangedName(uuun.ID, uuun.User, uuun.OldName, uuun.Name, uuun.Timestamp)
					if err != nil {
						log.Fatal(err.Error())
					}
					groupItems[g.id] = append(groupItems[g.id], uuunGroupItem)
				}
			}
		}
	}

	// Create widgets for all the thread items we added
	for _, g := range fyneUI.groups { // TODO: store groups and DMs in a shared threads slice?
		if items, ok := groupItems[g.id]; ok {
			fyneUI.populateInitialItems(g, items)
		}
	}
	for _, u := range fyneUI.dms { // TODO: store groups and DMs in a shared threads slice?
		if items, ok := dmItems[u.user.id]; ok {
			fyneUI.populateInitialItems(u, items)
		}
	}
	fyneUI.refreshThreadOrder()
}

func (fyneUI *Fyne) chatContainerSizeAtStartup() fyne.Size {
	if fyne.CurrentDevice().IsMobile() {
		fyneUI.mainWindow.Canvas().Size()
	}

	// Exclude thread buttons from width
	width := defaultWidth - threadButtonHeight*5

	return fyne.Size{
		Width:  width,
		Height: defaultHeight,
	}
}

func (fyneUI *Fyne) creatDMIfNeeded(id uuid.UUID, initialStates map[uuid.UUID]chat.DMState) {
	u, exists := fyneUI.users.get(id)
	if !exists {
		log.Fatal("dm user not known to UI")
	}

	initialState, _ := initialStates[id]

	_, exists = fyneUI.dms[id]
	if !exists {
		fyneUI.NewDirectMessage(chat.User{
			ID:    id,
			Name:  u.getName(),
			State: initialState,
		})
		_, exists = fyneUI.dms[id]
		if !exists {
			log.Fatal("dm doesn't exist after creation")
		}
	}
}

func (fyneUI *Fyne) NetworkOnline() {
	if fyneUI.activeThread != uuid.Nil {
		if _, ok := fyneUI.dms[fyneUI.activeThread]; ok {
			fyneUI.callbacks.UserConnectionDesired(fyneUI.activeThread)
		} else if _, ok = fyneUI.groups[fyneUI.activeThread]; ok {
			fyneUI.callbacks.GroupConnectionDesired(fyneUI.activeThread)
		} else {
			log.WithFields(log.Fields{
				"thread": fyneUI.activeThread,
			}).Error("active thread is not a known user or group")
		}
	}
	fyneUI.networkOfflineWarning.Hide()
	fyneUI.networkState = networkStateOnline
}

func (fyneUI *Fyne) NetworkOffline() {
	fyneUI.networkState = networkStateOffline
	fyneUI.networkOfflineWarning.SetText("network connection lost, reconnecting...")
	fyneUI.networkOfflineWarning.Show()
}

func (fyneUI *Fyne) UserImported(u chat.User) {
	newUser := makeUser(u.ID, u.Name)
	fyneUI.users.add(newUser)
	fyneUI.NewDirectMessage(u)
}
