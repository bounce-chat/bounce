package ui

import (
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/hkparker/bounce/chat"
	"github.com/hkparker/bounce/network"

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

const defaultHeight = float32(700)
const defaultWidth = float32(900)
const defaultChatHistoryHeight = float32(585.84375)
const defaultChatHistoryWidth = float32(566)

type ui struct {
	bounce                        *chat.Bounce
	app                           fyne.App
	mainWindow                    fyne.Window
	newGroupWidgets               *newGroupWidgets
	newSyncDeviceWidgets          *newSyncDeviceWidgets
	settingsWidgets               *settingsWidgets
	mainContainer                 *fyne.Container
	defaultContainer              *fyne.Container
	newInstall                    *fyne.Container
	newSyncDevice                 *fyne.Container
	nameNewDevice                 *fyne.Container
	addUser                       *fyne.Container
	newProfileCreator             *fyne.Container
	databaseLoading               *fyne.Container
	editProfile                   *fyne.Container
	displaySyncString             *fyne.Container
	displayAddUserString          *fyne.Container
	settingsContainer             *fyne.Container
	about                         *fyne.Container
	newGroup                      *fyne.Container
	newDM                         *fyne.Container
	introduceContacts             *fyne.Container
	importContact                 *fyne.Container
	threadVBox                    *fyne.Container
	chatContainer                 *fyne.Container
	mobileMenu                    *fyne.Container
	newProfileImage               *defaultImage
	newProfileImageData           []byte
	profileNameEntry              *widget.Entry
	profileIcon                   *defaultImage
	profileOptions                *fyne.Container
	newDMUserSearchEntry          *widget.Entry
	mainMenu                      *fyne.MainMenu
	allUsersDMLinksScroll         *container.Scroll
	networkOfflineWarning         *widget.Label
	groups                        map[uuid.UUID]*group
	dms                           map[uuid.UUID]*directMessage
	threadWithItem                map[uuid.UUID]thread
	threadWithItemMutex           sync.Mutex
	pausedGroupNotifications      map[uuid.UUID]bool
	pausedGroupNotificationsMutex sync.Mutex
	activeThread                  uuid.UUID
	syncString                    binding.String
	addUserString                 binding.String
	profile                       *user
	settings                      chat.Settings
	localSettings                 chat.LocalSettings
	users                         *userStore
	devices                       *deviceStore
	messages                      *messageStore
	currentDevices                *container.Scroll
	deletedUser                   *user
	initialStateSet               bool
	initialSyncIncomplete         bool
	focused                       bool
	networkState                  int
	setupStep                     int
	viewStack                     []view
}

func Main() {
	ui := &ui{}
	ui.bounce = chat.Open(ui, &network.TorNetwork{}, getConfigDirectory())
	ui.build()

	ui.app.Lifecycle().SetOnStarted(func() {
		go ui.loadInitialState(ui.bounce.GetInitialState())
	})

	ui.app.Lifecycle().SetOnStopped(func() {
		ui.bounce.Shutdown()
	})

	ui.app.Run()
}

func (ui *ui) build() {
	//
	// Define the app
	//
	ui.app = app.NewWithID("chat.bounce")
	ui.app.SetIcon(newEmbeddedResource("assets/icon.png"))
	ui.app.Lifecycle().SetOnEnteredForeground(func() { ui.focused = true })
	ui.app.Lifecycle().SetOnExitedForeground(func() { ui.focused = false })
	if ui.bounce.DarkMode() {
		ui.app.Settings().SetTheme(&forcedVariant{Theme: theme.DefaultTheme(), variant: theme.VariantDark})
	} else {
		ui.app.Settings().SetTheme(&forcedVariant{Theme: theme.DefaultTheme(), variant: theme.VariantLight})
	}

	//
	// Initialize types that require it
	//
	ui.groups = make(map[uuid.UUID]*group)
	ui.dms = make(map[uuid.UUID]*directMessage)
	ui.threadWithItem = make(map[uuid.UUID]thread)
	ui.pausedGroupNotifications = make(map[uuid.UUID]bool)
	ui.users = newUserStore()
	ui.devices = newDeviceStore()
	ui.messages = newMessageStore(getConfigDirectory())
	ui.focused = true
	ui.syncString = binding.NewString()
	ui.addUserString = binding.NewString()
	ui.networkOfflineWarning = widget.NewLabelWithStyle("network is starting...", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	ui.networkOfflineWarning.Show()
	ui.deletedUser = makeUser(uuid.Nil, "-deleted-")
	ui.viewStack = []view{}

	//
	// Define the main window
	//
	ui.mainWindow = ui.app.NewWindow("Bounce")
	ui.mainWindow.SetMaster()
	//ui.mainWindow.SetOnClosed(func() {
	//	if !fyne.CurrentDevice().IsMobile() {
	//		ui.app.Preferences().SetBool("size-set", true)
	//		ui.app.Preferences().SetBool("fullscreen", ui.mainWindow.FullScreen())
	//		mainSize := ui.mainWindow.Canvas().Size()
	//		mainSize.Width -= 2 // The window appears to always be 2px less wide than the canvas size
	//		ui.app.Preferences().SetFloat("main-width", float64(mainSize.Width))
	//		ui.app.Preferences().SetFloat("main-height", float64(mainSize.Height))
	//		log.WithFields(log.Fields{
	//			"w": mainSize.Width,
	//			"h": mainSize.Height,
	//		}).Warn("closing, saving sizes")
	//	}
	//})
	ui.mainWindow.SetCloseIntercept(func() {
		// There's some bug in Fyne where the app will hang when the close button
		// is hit unless this is explicitly set https://github.com/fyne-io/fyne/issues/2314
		ui.Quit()
	})
	//if !fyne.CurrentDevice().IsMobile() {
	//	if ui.app.Preferences().Bool("size-set") {
	//		if ui.app.Preferences().Bool("fullscreen") {
	//			ui.mainWindow.SetFullScreen(true)
	//		} else {
	//			w := float32(ui.app.Preferences().Float("main-width"))
	//			h := float32(ui.app.Preferences().Float("main-height"))
	//			log.WithFields(log.Fields{
	//				"w": w,
	//				"h": h,
	//			}).Warn("starting up, sizes")
	//			ui.mainWindow.Resize(fyne.Size{Height: h, Width: w})
	//		}
	//	} else {
	ui.mainWindow.Resize(fyne.Size{Height: defaultHeight, Width: defaultWidth})
	//	}
	//}
	ui.mainWindow.Canvas().SetOnTypedKey(func(ev *fyne.KeyEvent) {
		if fyne.CurrentDevice().IsMobile() && ev.Name == mobile.KeyBack {
			ui.mobileBack()
		}
	})
	//if desk, ok := ui.app.(desktop.App); ok {
	//	m := fyne.NewMenu("Bounce",
	//		fyne.NewMenuItem("Show", func() {
	//			ui.mainWindow.Show()
	//		}),
	//	)
	//	desk.SetSystemTrayMenu(m)
	//	ui.mainWindow.SetCloseIntercept(func() {
	//		ui.mainWindow.Hide()
	//	})
	//}

	//
	// Build all the containers
	//
	ui.buildMenu()
	ui.buildNewSyncDeviceWidgets()
	ui.buildNewInstall()
	ui.buildNewSyncDevice()
	ui.buildNameNewDevice()
	ui.buildAddUser()
	ui.buildNewProfileCreator()
	ui.buildDatabaseLoading()
	ui.buildEditProfile()
	ui.buildDisplaySyncString()
	ui.buildDisplayAddUserString()
	ui.buildSettings()
	ui.buildAbout()
	ui.buildNewGroup()
	ui.buildNewDM()
	ui.buildImportContact()
	ui.buildMainContainer()
	ui.buildMobileMenu()

	//
	// Default to displaying the loading container
	//
	ui.networkState = networkStateStarting
	ui.showMainContainer()
	ui.mainWindow.Show()
}

func (ui *ui) askToIgnoreBatteryOptimizations() {
	if ui.localSettings.NeverAskForBatteryOptimizations {
		return
	}

	if runtime.GOOS == "android" {
		if !batteryOptimizationsIgnored() {
			var batteryDialog dialog.Dialog

			batteryText := widget.NewRichTextWithText("Bounce can only notify you of new messages if it's running in the background.  Click \"Allow\" to be prompted to add Bounce to Android's list of allowed background apps.")
			batteryText.Wrapping = fyne.TextWrapWord
			batteryAllow := widget.NewButton("Allow", func() {
				batteryDialog.Hide()
				requestIgnoreBatteryOptimizations()
			})
			batteryAllow.Importance = widget.HighImportance
			batteryLater := widget.NewButton("Later", func() {
				batteryDialog.Hide()
			})
			batteryLater.Importance = widget.LowImportance
			batteryNever := widget.NewButton("Never", func() {
				batteryDialog.Hide()
				ui.bounce.NeverAskForBatteryOptimizations()
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
				ui.mainWindow,
			)
			ui.showDialog(batteryDialog, nil)
		}
	}
}

func (ui *ui) Run() {
	ui.app.Run()
}

func (ui *ui) Quit() {
	ui.app.Quit()
}

func (ui *ui) loadInitialState(state chat.InitialState) {
	fyne.DoAndWait(func() {
		if ui.initialStateSet {
			log.Fatal("the initial state of the UI can only be loaded once")
		}
		// Refresh the state of the main view after the state is loaded
		ui.initialStateSet = true
		defer ui.showMainContainer()

		if state.Profile == nil {
			// This is a brand new installation.  Tell the user interface it must have the
			// user create a profile or sync this device to an existing device group before
			// the app can be used.
			// TODO: log fatal if anything else is set
			return
		}

		ui.profile = makeUser(state.Profile.ID, state.Profile.Name)
		ui.profile.images = state.Profile.Images
		ui.users.add(ui.profile)
		ui.settings = state.Settings
		ui.localSettings = state.LocalSettings
		ui.askToIgnoreBatteryOptimizations()

		for i, _ := range state.SyncDevices {
			dev := state.SyncDevices[i]
			ui.devices.add(&dev)
		}
		ui.updateDeviceStatus()

		dmItems := make(map[uuid.UUID]threadItems)
		groupItems := make(map[uuid.UUID]threadItems)

		initialDMStates := map[uuid.UUID]chat.DMState{}
		for _, u := range state.Users {
			uiUser := makeUser(u.ID, u.Name)
			uiUser.images = u.Images
			ui.users.add(uiUser)
			initialDMStates[u.ID] = u.State
		}

		for _, g := range state.Groups {
			ui.buildNewGroupChat(g)
			group, exists := ui.groups[g.ID]
			if !exists {
				log.Fatal("group doesn't exist immediately after creation")
			}
			group.button.setLastMessageTime(time.Unix(g.LastActivity, 0))
			group.setLastMessageTime(g.LastActivity)

			gcTi, err := ui.newGroupCreated(g.ID, g.ID, g.CreatedBy, g.CreatedAt)
			if err != nil {
				log.WithFields(log.Fields{
					"error":      err.Error(),
					"group":      g.ID,
					"created_by": g.CreatedBy,
				}).Warn("error creating thread item for group creation while loading initial state")
			}
			groupItems[g.ID] = append(groupItems[g.ID], gcTi)
		}

		for _, dm := range state.DirectMessages {
			ui.createDMIfNeeded(dm.Thread, initialDMStates)
			dmti, err := ui.newDirectMessage(dm)
			if err != nil {
				log.Error(err.Error())
			}
			dmItems[dm.Thread] = append(dmItems[dm.Thread], dmti)
		}

		for _, udmr := range state.UpdateDMRetentions {
			ui.createDMIfNeeded(udmr.Thread, initialDMStates)
			udmrItem, err := ui.newUpdateDMRetention(udmr)
			if err != nil {
				log.Error(err.Error())
			}
			dmItems[udmr.Thread] = append(dmItems[udmr.Thread], udmrItem)
		}

		for _, udmch := range state.UpdateDMClearHistories {
			ui.createDMIfNeeded(udmch.Thread, initialDMStates)
			udmchItem, err := ui.newUpdateDMClearHistory(udmch)
			if err != nil {
				log.Error(err.Error())
			}
			dmItems[udmch.Thread] = append(dmItems[udmch.Thread], udmchItem)
		}

		for _, gm := range state.GroupMessages {
			mti, err := ui.newGroupMessage(gm)
			if err != nil {
				log.Error(err.Error())
			}
			groupItems[gm.Thread] = append(groupItems[gm.Thread], mti)
		}

		for _, ugr := range state.UpdateGroupRetentions {
			ugrItem, err := ui.newUpdateGroupRetention(ugr)
			if err != nil {
				log.Error(err.Error())
			}
			groupItems[ugr.Thread] = append(groupItems[ugr.Thread], ugrItem)
		}

		for _, ugn := range state.UpdateGroupNames {
			ugnItem, err := ui.newUpdateGroupName(ugn)
			if err != nil {
				log.Error(err.Error())
			}
			groupItems[ugn.Thread] = append(groupItems[ugn.Thread], ugnItem)
		}

		for _, ugau := range state.UpdateGroupAddUsers {
			ugauItem, err := ui.newUpdateGroupAddUser(ugau)
			if err != nil {
				log.Error(err.Error())
			}
			groupItems[ugau.Thread] = append(groupItems[ugau.Thread], ugauItem)
		}

		for _, ugru := range state.UpdateGroupRemoveUsers {
			ugruItem, err := ui.newUpdateGroupRemoveUser(ugru)
			if err != nil {
				log.Error(err.Error())
			}
			groupItems[ugru.Thread] = append(groupItems[ugru.Thread], ugruItem)
		}

		for _, ugch := range state.UpdateGroupClearHistories {
			ugchItem, err := ui.newUpdateGroupClearHistory(ugch)
			if err != nil {
				log.Error(err.Error())
			}
			groupItems[ugch.Thread] = append(groupItems[ugch.Thread], ugchItem)
		}

		for _, ugap := range state.UpdateGroupAdminPromotions {
			ugapItem, err := ui.newUpdateGroupAdminPromoted(ugap)
			if err != nil {
				log.Error(err.Error())
			}
			groupItems[ugap.Thread] = append(groupItems[ugap.Thread], ugapItem)
		}

		for _, ugad := range state.UpdateGroupAdminDemotions {
			ugadItem, err := ui.newUpdateGroupAdminDemoted(ugad)
			if err != nil {
				log.Error(err.Error())
			}
			groupItems[ugad.Thread] = append(groupItems[ugad.Thread], ugadItem)
		}

		for _, ugumr := range state.UpdateGroupUserManagementsRestricted {
			ugumrItem, err := ui.newUpdateGroupUserManagementRestricted(ugumr)
			if err != nil {
				log.Error(err.Error())
			}
			groupItems[ugumr.Thread] = append(groupItems[ugumr.Thread], ugumrItem)
		}

		for _, ugumu := range state.UpdateGroupUserManagementsUnrestricted {
			ugumuItem, err := ui.newUpdateGroupUserManagementUnrestricted(ugumu)
			if err != nil {
				log.Error(err.Error())
			}
			groupItems[ugumu.Thread] = append(groupItems[ugumu.Thread], ugumuItem)
		}

		for _, uger := range state.UpdateGroupEditsRestricted {
			ugerItem, err := ui.newUpdateGroupEditsRestricted(uger)
			if err != nil {
				log.Error(err.Error())
			}
			groupItems[uger.Thread] = append(groupItems[uger.Thread], ugerItem)
		}

		for _, ugeu := range state.UpdateGroupEditsUnrestricted {
			ugeuItem, err := ui.newUpdateGroupEditsUnrestricted(ugeu)
			if err != nil {
				log.Error(err.Error())
			}
			groupItems[ugeu.Thread] = append(groupItems[ugeu.Thread], ugeuItem)
		}

		for _, ugpr := range state.UpdateGroupPostingsRestricted {
			ugprItem, err := ui.newUpdateGroupPostingRestricted(ugpr)
			if err != nil {
				log.Error(err.Error())
			}
			groupItems[ugpr.Thread] = append(groupItems[ugpr.Thread], ugprItem)
		}

		for _, ugpu := range state.UpdateGroupPostingsUnrestricted {
			ugpuItem, err := ui.newUpdateGroupPostingUnrestricted(ugpu)
			if err != nil {
				log.Error(err.Error())
			}
			groupItems[ugpu.Thread] = append(groupItems[ugpu.Thread], ugpuItem)
		}

		for _, ubg := range state.UpdateGroupUserBlockedGroups {
			ubgItem, err := ui.newUpdateGroupUserBlocked(ubg)
			if err != nil {
				log.Error(err.Error())
			}
			groupItems[ubg.Thread] = append(groupItems[ubg.Thread], ubgItem)
		}

		for _, ugci := range state.UpdateGroupUserChangedGroupImages {
			ugciItem, err := ui.newUpdateGroupUserChangedGroupImage(ugci)
			if err != nil {
				log.Error(err.Error())
			}
			groupItems[ugci.Thread] = append(groupItems[ugci.Thread], ugciItem)
		}

		for _, uuun := range state.UpdateUserUpdateNames {
			if uuun.User == ui.profile.id {
				for dmID, _ := range ui.dms { // TODO: don't add to DMs before the DMs should exist
					uuunItem, err := ui.userChangedName(uuun.ID, uuun.User, uuun.OldName, uuun.Name, uuun.Timestamp)
					if err != nil {
						log.Error(err.Error())
					} else {
						dmItems[dmID] = append(dmItems[dmID], uuunItem)
					}
				}
				for _, g := range ui.groups {
					if uuun.Timestamp < g.createdAt {
						continue
					}
					uuunItem, err := ui.userChangedName(uuun.ID, uuun.User, uuun.OldName, uuun.Name, uuun.Timestamp)
					if err != nil {
						log.Error(err.Error())
					} else {
						groupItems[g.id] = append(groupItems[g.id], uuunItem)
					}
				}
			} else {
				uuunItem, err := ui.userChangedName(uuun.ID, uuun.User, uuun.OldName, uuun.Name, uuun.Timestamp)
				if err != nil {
					log.Error(err.Error())
				}
				dmItems[uuun.User] = append(dmItems[uuun.User], uuunItem)

				for _, g := range ui.groups {
					if uuun.Timestamp < g.createdAt {
						continue
					}
					if g.users.contains(uuun.User) {
						uuunGroupItem, err := ui.userChangedName(uuun.ID, uuun.User, uuun.OldName, uuun.Name, uuun.Timestamp)
						if err != nil {
							log.Error(err.Error())
						}
						groupItems[g.id] = append(groupItems[g.id], uuunGroupItem)
					}
				}
			}
		}
		for _, uuui := range state.UpdateUserUpdateImages {
			if uuui.User == ui.profile.id {
				for dmID, _ := range ui.dms { // TODO: don't add to DMs before the DMs should exist
					uuuiItem, err := ui.userChangedImage(uuui.ID, uuui.User, uuui.Timestamp)
					if err != nil {
						log.Error(err.Error())
					} else {
						dmItems[dmID] = append(dmItems[dmID], uuuiItem)
					}
				}
				for _, g := range ui.groups {
					if uuui.Timestamp < g.createdAt {
						continue
					}
					uuuiItem, err := ui.userChangedImage(uuui.ID, uuui.User, uuui.Timestamp)
					if err != nil {
						log.Error(err.Error())
					} else {
						groupItems[g.id] = append(groupItems[g.id], uuuiItem)
					}
				}
			} else {
				uuuiItem, err := ui.userChangedImage(uuui.ID, uuui.User, uuui.Timestamp)
				if err != nil {
					log.Error(err.Error())
				}
				dmItems[uuui.User] = append(dmItems[uuui.User], uuuiItem)

				for _, g := range ui.groups {
					if uuui.Timestamp < g.createdAt {
						continue
					}
					if g.users.contains(uuui.User) {
						uuuiGroupItem, err := ui.userChangedImage(uuui.ID, uuui.User, uuui.Timestamp)
						if err != nil {
							log.Error(err.Error())
						}
						groupItems[g.id] = append(groupItems[g.id], uuuiGroupItem)
					}
				}
			}
		}

		// Create widgets for all the thread items we added
		for _, g := range ui.groups { // TODO: store groups and DMs in a shared threads slice?
			if items, ok := groupItems[g.id]; ok {
				ui.populateInitialItems(g, items)
			}
		}
		for _, u := range ui.dms { // TODO: store groups and DMs in a shared threads slice?
			if items, ok := dmItems[u.user.id]; ok {
				ui.populateInitialItems(u, items)
			}
		}
		ui.refreshThreadOrder()
	})
	go ui.messages.writeCache()
}

func chatContainerSizeAtStartup() fyne.Size {
	if fyne.CurrentDevice().IsMobile() {
		return fyne.CurrentApp().Driver().AllWindows()[0].Canvas().Size()
	}

	//if fyne.CurrentApp().Preferences().Bool("size-set") {
	//	log.WithFields(log.Fields{
	//		"chat-width":  float32(fyne.CurrentApp().Preferences().Float("chat-width")),
	//		"chat-height": float32(fyne.CurrentApp().Preferences().Float("chat-height")),
	//	}).Warn("default height pulled from store")
	//	return fyne.Size{
	//		Width:  float32(fyne.CurrentApp().Preferences().Float("chat-width")),
	//		Height: float32(fyne.CurrentApp().Preferences().Float("chat-height")),
	//	}
	//}

	return fyne.Size{
		Width:  defaultChatHistoryWidth,
		Height: defaultChatHistoryHeight,
	}
}

func (ui *ui) createDMIfNeeded(id uuid.UUID, initialStates map[uuid.UUID]chat.DMState) {
	u, exists := ui.users.get(id)
	if !exists {
		log.Fatal("dm user not known to UI")
	}

	initialState, _ := initialStates[id]

	_, exists = ui.dms[id]
	if !exists {
		ui.buildNewDirectMessage(chat.User{
			ID:     id,
			Name:   u.getName(),
			Images: u.images,
			State:  initialState,
		})
		_, exists = ui.dms[id]
		if !exists {
			log.Fatal("dm doesn't exist after creation")
		}
	}
}

func (ui *ui) NetworkOnline() {
	if ui.activeThread != uuid.Nil {
		if _, ok := ui.dms[ui.activeThread]; ok { // TODO: group and dm maps are not concurrency safe
			ui.bounce.UserConnectionDesired(ui.activeThread)
		} else if _, ok = ui.groups[ui.activeThread]; ok {
			ui.bounce.GroupConnectionDesired(ui.activeThread)
		} else {
			log.WithFields(log.Fields{
				"thread": ui.activeThread,
			}).Error("active thread is not a known user or group")
		}
	}
	fyne.DoAndWait(func() { ui.networkOfflineWarning.Hide() })
	ui.networkState = networkStateOnline
}

func (ui *ui) NetworkOffline() {
	ui.networkState = networkStateOffline
	fyne.DoAndWait(func() {
		ui.networkOfflineWarning.SetText("network connection lost, reconnecting...")
		ui.networkOfflineWarning.Show()
	})
}

func (ui *ui) UserImported(u chat.User) {
	newUser := makeUser(u.ID, u.Name)
	ui.users.add(newUser)
	fyne.DoAndWait(func() { ui.NewDirectMessage(u) })
}

func (ui *ui) FileCompleted(fileID uuid.UUID) {
	// TODO: check if anything is waiting for this file and refresh it
	for _, g := range ui.groups {
		fyne.DoAndWait(func() {
			g.editIcon.Refresh()
			g.headerIcon.Refresh()
			g.button.threadImage.Refresh()
		})
	}

	for _, dm := range ui.dms {
		fyne.DoAndWait(func() {
			dm.editIcon.Refresh()
			dm.headerIcon.Refresh()
			dm.button.threadImage.Refresh()
		})
	}
}

func getConfigDirectory() string {
	var configDirectory string
	if testing.Testing() {
		configDirectory = os.TempDir() + "/bounce-test-" + uuid.New().String()
	} else if runtime.GOOS == "android" {
		// TODO: use /data/data/chat.bounce/ ?
		configDirectory = "/sdcard/Android/data/chat.bounce"
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			log.WithFields(log.Fields{
				"at":    "configDirectory",
				"error": err.Error(),
			}).Fatal("error getting home directory")
		}
		configDirectory = home + "/.bounce"
	}

	err := os.MkdirAll(configDirectory, 0700)
	if err != nil {
		log.WithFields(log.Fields{
			"path":  configDirectory,
			"error": err.Error(),
		}).Fatal("error creating config directory")
	}

	return configDirectory
}
