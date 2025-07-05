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
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
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
	users                         *userStore
	devices                       *deviceStore
	messages                      *messageStore
	threads                       *threadStore
	app                           fyne.App
	mainWindow                    fyne.Window
	newGroupWidgets               *newGroupWidgets
	newSyncDeviceWidgets          *newSyncDeviceWidgets
	settingsWidgets               *settingsWidgets
	imageViewer                   *imageViewer
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
	pausedGroupNotifications      map[uuid.UUID]bool
	pausedGroupNotificationsMutex sync.Mutex
	activeThread                  uuid.UUID
	syncStringEntry               *widget.Entry
	addUserStringEntry            *widget.Entry
	addUserQRCode                 *canvas.Image
	addDeviceQRCode               *canvas.Image
	profile                       *user
	settings                      chat.Settings
	localSettings                 chat.LocalSettings
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
	ui := &ui{
		users:    newUserStore(),
		devices:  newDeviceStore(),
		messages: newMessageStore(),
		threads:  newThreadStore(),
	}
	ui.bounce = chat.Open(ui, &network.TorNetwork{}, getConfigDirectory())
	ui.build()

	go func() {
		ui.loadInitialState(ui.bounce.GetInitialState())
	}()

	ui.app.Run()
	ui.bounce.Shutdown()
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
	ui.pausedGroupNotifications = make(map[uuid.UUID]bool)
	ui.focused = true
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
	ui.buildImageViewer()

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

		threadItems := make(map[uuid.UUID]threadItems)

		initialDMStates := map[uuid.UUID]chat.DMState{}
		for _, u := range state.Users {
			uiUser := makeUser(u.ID, u.Name)
			uiUser.images = u.Images
			ui.users.add(uiUser)
			initialDMStates[u.ID] = u.State
		}

		for _, g := range state.Groups {
			ui.buildNewGroupChat(g)
			group, ok := ui.threads.getGroup(g.ID)
			if !ok {
				log.Fatal("group thread doesn't exist immediately after creation")
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
			threadItems[g.ID] = append(threadItems[g.ID], gcTi)
		}

		for _, dm := range state.DirectMessages {
			ui.createDMIfNeeded(dm.Thread, initialDMStates)
			dmti, err := ui.newDirectMessage(dm)
			if err != nil {
				log.Error(err.Error())
			}
			threadItems[dm.Thread] = append(threadItems[dm.Thread], dmti)
		}

		for _, udmr := range state.UpdateDMRetentions {
			ui.createDMIfNeeded(udmr.Thread, initialDMStates)
			udmrItem, err := ui.newUpdateDMRetention(udmr)
			if err != nil {
				log.Error(err.Error())
			}
			threadItems[udmr.Thread] = append(threadItems[udmr.Thread], udmrItem)
		}

		for _, udmch := range state.UpdateDMClearHistories {
			ui.createDMIfNeeded(udmch.Thread, initialDMStates)
			udmchItem, err := ui.newUpdateDMClearHistory(udmch)
			if err != nil {
				log.Error(err.Error())
			}
			threadItems[udmch.Thread] = append(threadItems[udmch.Thread], udmchItem)
		}

		for _, gm := range state.GroupMessages {
			mti, err := ui.newGroupMessage(gm)
			if err != nil {
				log.Error(err.Error())
			}
			threadItems[gm.Thread] = append(threadItems[gm.Thread], mti)
		}

		for _, ugr := range state.UpdateGroupRetentions {
			ugrItem, err := ui.newUpdateGroupRetention(ugr)
			if err != nil {
				log.Error(err.Error())
			}
			threadItems[ugr.Thread] = append(threadItems[ugr.Thread], ugrItem)
		}

		for _, ugn := range state.UpdateGroupNames {
			ugnItem, err := ui.newUpdateGroupName(ugn)
			if err != nil {
				log.Error(err.Error())
			}
			threadItems[ugn.Thread] = append(threadItems[ugn.Thread], ugnItem)
		}

		for _, ugau := range state.UpdateGroupAddUsers {
			ugauItem, err := ui.newUpdateGroupAddUser(ugau)
			if err != nil {
				log.Error(err.Error())
			}
			threadItems[ugau.Thread] = append(threadItems[ugau.Thread], ugauItem)
		}

		for _, ugru := range state.UpdateGroupRemoveUsers {
			ugruItem, err := ui.newUpdateGroupRemoveUser(ugru)
			if err != nil {
				log.Error(err.Error())
			}
			threadItems[ugru.Thread] = append(threadItems[ugru.Thread], ugruItem)
		}

		for _, ugch := range state.UpdateGroupClearHistories {
			ugchItem, err := ui.newUpdateGroupClearHistory(ugch)
			if err != nil {
				log.Error(err.Error())
			}
			threadItems[ugch.Thread] = append(threadItems[ugch.Thread], ugchItem)
		}

		for _, ugap := range state.UpdateGroupAdminPromotions {
			ugapItem, err := ui.newUpdateGroupAdminPromoted(ugap)
			if err != nil {
				log.Error(err.Error())
			}
			threadItems[ugap.Thread] = append(threadItems[ugap.Thread], ugapItem)
		}

		for _, ugad := range state.UpdateGroupAdminDemotions {
			ugadItem, err := ui.newUpdateGroupAdminDemoted(ugad)
			if err != nil {
				log.Error(err.Error())
			}
			threadItems[ugad.Thread] = append(threadItems[ugad.Thread], ugadItem)
		}

		for _, ugumr := range state.UpdateGroupUserManagementsRestricted {
			ugumrItem, err := ui.newUpdateGroupUserManagementRestricted(ugumr)
			if err != nil {
				log.Error(err.Error())
			}
			threadItems[ugumr.Thread] = append(threadItems[ugumr.Thread], ugumrItem)
		}

		for _, ugumu := range state.UpdateGroupUserManagementsUnrestricted {
			ugumuItem, err := ui.newUpdateGroupUserManagementUnrestricted(ugumu)
			if err != nil {
				log.Error(err.Error())
			}
			threadItems[ugumu.Thread] = append(threadItems[ugumu.Thread], ugumuItem)
		}

		for _, uger := range state.UpdateGroupEditsRestricted {
			ugerItem, err := ui.newUpdateGroupEditsRestricted(uger)
			if err != nil {
				log.Error(err.Error())
			}
			threadItems[uger.Thread] = append(threadItems[uger.Thread], ugerItem)
		}

		for _, ugeu := range state.UpdateGroupEditsUnrestricted {
			ugeuItem, err := ui.newUpdateGroupEditsUnrestricted(ugeu)
			if err != nil {
				log.Error(err.Error())
			}
			threadItems[ugeu.Thread] = append(threadItems[ugeu.Thread], ugeuItem)
		}

		for _, ugpr := range state.UpdateGroupPostingsRestricted {
			ugprItem, err := ui.newUpdateGroupPostingRestricted(ugpr)
			if err != nil {
				log.Error(err.Error())
			}
			threadItems[ugpr.Thread] = append(threadItems[ugpr.Thread], ugprItem)
		}

		for _, ugpu := range state.UpdateGroupPostingsUnrestricted {
			ugpuItem, err := ui.newUpdateGroupPostingUnrestricted(ugpu)
			if err != nil {
				log.Error(err.Error())
			}
			threadItems[ugpu.Thread] = append(threadItems[ugpu.Thread], ugpuItem)
		}

		for _, ubg := range state.UpdateGroupUserBlockedGroups {
			ubgItem, err := ui.newUpdateGroupUserBlocked(ubg)
			if err != nil {
				log.Error(err.Error())
			}
			threadItems[ubg.Thread] = append(threadItems[ubg.Thread], ubgItem)
		}

		for _, ugci := range state.UpdateGroupUserChangedGroupImages {
			ugciItem, err := ui.newUpdateGroupUserChangedGroupImage(ugci)
			if err != nil {
				log.Error(err.Error())
			}
			threadItems[ugci.Thread] = append(threadItems[ugci.Thread], ugciItem)
		}

		for _, uuun := range state.UpdateUserUpdateNames {
			if uuun.User == ui.profile.id {
				ui.threads.rangeFunc(func(th thread) {
					switch t := th.(type) {
					case *group:
						if uuun.Timestamp < t.createdAt {
							return
						}
						uuunItem, err := ui.userChangedName(uuun.ID, uuun.User, uuun.OldName, uuun.Name, uuun.Timestamp)
						if err != nil {
							log.Error(err.Error())
						} else {
							threadItems[t.id] = append(threadItems[t.id], uuunItem)
						}
					case *directMessage:
						uuunItem, err := ui.userChangedName(uuun.ID, uuun.User, uuun.OldName, uuun.Name, uuun.Timestamp)
						if err != nil {
							log.Error(err.Error())
						} else {
							threadItems[t.user.id] = append(threadItems[t.user.id], uuunItem)
						}
					}
				})
			} else {
				uuunItem, err := ui.userChangedName(uuun.ID, uuun.User, uuun.OldName, uuun.Name, uuun.Timestamp)
				if err != nil {
					log.Error(err.Error())
				}
				threadItems[uuun.User] = append(threadItems[uuun.User], uuunItem)

				ui.threads.rangeFunc(func(t thread) {
					if g, ok := t.(*group); ok {
						if uuun.Timestamp < g.createdAt {
							return
						}
						if g.users.contains(uuun.User) {
							uuunGroupItem, err := ui.userChangedName(uuun.ID, uuun.User, uuun.OldName, uuun.Name, uuun.Timestamp)
							if err != nil {
								log.Error(err.Error())
							}
							threadItems[g.id] = append(threadItems[g.id], uuunGroupItem)
						}
					}
				})
			}
		}
		for _, uuui := range state.UpdateUserUpdateImages {
			if uuui.User == ui.profile.id {
				ui.threads.rangeFunc(func(th thread) {
					switch t := th.(type) {
					case *group:
						if uuui.Timestamp < t.createdAt {
							return
						}
						uuuiItem, err := ui.userChangedImage(uuui.ID, uuui.User, uuui.Timestamp)
						if err != nil {
							log.Error(err.Error())
						} else {
							threadItems[t.id] = append(threadItems[t.id], uuuiItem)
						}
					case *directMessage:
						uuuiItem, err := ui.userChangedImage(uuui.ID, uuui.User, uuui.Timestamp)
						if err != nil {
							log.Error(err.Error())
						} else {
							threadItems[t.user.id] = append(threadItems[t.user.id], uuuiItem)
						}
					}
				})
			} else {
				uuuiItem, err := ui.userChangedImage(uuui.ID, uuui.User, uuui.Timestamp)
				if err != nil {
					log.Error(err.Error())
				}
				threadItems[uuui.User] = append(threadItems[uuui.User], uuuiItem)

				ui.threads.rangeFunc(func(t thread) {
					if g, ok := t.(*group); ok {
						if uuui.Timestamp < g.createdAt {
							return
						}
						if g.users.contains(uuui.User) {
							uuuiGroupItem, err := ui.userChangedImage(uuui.ID, uuui.User, uuui.Timestamp)
							if err != nil {
								log.Error(err.Error())
							}
							threadItems[g.id] = append(threadItems[g.id], uuuiGroupItem)
						}
					}
				})
			}
		}

		// Create widgets for all the thread items we added
		ui.threads.rangeFunc(func(t thread) {
			if items, ok := threadItems[t.getID()]; ok {
				ui.populateInitialItems(t, items)
			}
		})
		ui.refreshThreadOrder()

		for _, fp := range state.FileProgress {
			ui.FileDownloadProgress(fp.ID, fp.Progress)
		}
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
	initialState, _ := initialStates[id]

	_, ok := ui.threads.getDM(id)
	if !ok {
		u, userExists := ui.users.get(id)
		if !userExists {
			log.Fatal("dm user not known to UI")
		}

		ui.NewDirectMessage(chat.User{
			ID:     u.id,
			Name:   u.getName(),
			Images: u.images,
			State:  initialState,
		})

		_, ok = ui.threads.getDM(id)
		if !ok {
			log.Fatal("DM doesn't exist immediately after creation")
		}
	}
}

func (ui *ui) NetworkOnline() {
	if ui.activeThread != uuid.Nil {
		t, ok := ui.threads.get(ui.activeThread)
		if !ok {
			log.WithFields(log.Fields{
				"thread": ui.activeThread,
			}).Error("active thread is not a known user or group")
		}
		if _, ok = t.(*group); ok {
			ui.bounce.GroupConnectionDesired(ui.activeThread)
		} else {
			ui.bounce.UserConnectionDesired(ui.activeThread)
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
	messageID, ok := ui.messages.getMessageWithFile(fileID)
	if ok {
		t, ok := ui.threads.withItem(messageID)
		if ok {
			// TODO hide any progress bars?
			fyne.DoAndWait(func() { t.chatHistoryScroll().Refresh() })
		}
		return
	}

	// TODO: check if anything is waiting for this file and refresh it
	ui.threads.rangeFunc(func(t thread) {
		fyne.DoAndWait(func() {
			t.getEditIcon().Refresh()
			t.getHeaderIcon().Refresh()
			t.getButton().threadImage.Refresh()
		})
	})
}

func (ui *ui) FileDownloadProgress(fileID uuid.UUID, progress float64) {
	messageID, ok := ui.messages.getMessageWithFile(fileID)
	if !ok {
		log.WithFields(log.Fields{
			"file_id": fileID,
		}).Warn("message not found with file while trying to update progress")
		return
	}
	m, ok := ui.messages.get(messageID)
	if !ok {
		log.WithFields(log.Fields{
			"message_id": messageID,
		}).Warn("message not found in the message store")
		return
	}

	t, ok := ui.threads.withItem(messageID)
	if !ok {
		log.WithFields(log.Fields{
			"message_id": messageID,
		}).Warn("thread not found with message")
		return
	}

	fyne.Do(func() {
		cbd, ok := m.(*chatBubbleData)
		if !ok {
			log.WithFields(log.Fields{
				"message_id": messageID,
			}).Warn("message with file attachment is not a chat bubble")
			return
		}
		for _, attachment := range cbd.fileAttachments {
			if attachment.ID == fileID {
				attachment.Progress = progress
			}
		}
		t.chatHistoryScroll().Refresh()
	})
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

	err := os.MkdirAll(configDirectory+"/blobs/", 0700)
	if err != nil {
		log.WithFields(log.Fields{
			"path":  configDirectory,
			"error": err.Error(),
		}).Fatal("error creating config directory")
	}

	return configDirectory
}
