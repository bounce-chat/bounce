package ui

import (
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/bounce-chat/bounce/chat"
	"github.com/bounce-chat/bounce/network"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
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

const darkMode = "darkMode"
const sysTray = "sysTray"
const neverAskForBatteryOptimizations = "neverAskForBatteryOptimizations"

type ui struct {
	bounce chat.Engine
	state  *state

	app    fyne.App
	window fyne.Window

	views      *views
	containers *containers
	widgets    *widgets

	users    *userStore
	devices  *deviceStore
	messages *messageStore
	threads  *threadStore
}

type widgets struct {
	networkOfflineWarning *widget.Label
	newInstall            *newInstall
	editProfile           *editProfile
	newGroup              *newGroup
	newSyncDevice         *newSyncDevice
	addSyncDevice         *addSyncDevice
	addUser               *addUser
	settings              *settings
	newDM                 *newDM
	imageViewer           *imageViewer
}

type containers struct {
	profileOptions           *fyne.Container
	defaultContainer         *fyne.Container
	addUserContent           *fyne.Container
	scanUser                 *fyne.Container
	displayAddUserString     *fyne.Container
	addSyncDeviceContent     *fyne.Container
	syncDeviceOptions        *fyne.Container
	inputEncryptedSyncString *fyne.Container
	threads                  *widget.List
	chat                     *fyne.Container
	currentDevices           *container.Scroll
	mainMenu                 *fyne.MainMenu
}

type views struct {
	main              *fyne.Container
	newInstall        *fyne.Container
	newSyncDevice     *fyne.Container
	addSyncDevice     *fyne.Container
	nameNewDevice     *fyne.Container
	addUser           *fyne.Container
	newProfileCreator *fyne.Container
	databaseLoading   *fyne.Container
	editProfile       *fyne.Container
	settings          *fyne.Container
	about             *fyne.Container
	newGroup          *fyne.Container
	newDM             *fyne.Container
	mobileMenu        *fyne.Container
}

type state struct {
	profile               *user
	settings              chat.Settings
	currentView           int
	viewStack             []view
	activeThread          uuid.UUID
	initialStateSet       bool
	initialSyncIncomplete bool
	focused               bool
	active                bool
	anotherDeviceActive   bool
	networkState          int
	setupStep             int
	threadSearch          string
}

func Main() {
	ui := &ui{
		containers: &containers{},
		views:      &views{},
		widgets:    &widgets{},
		users:      newUserStore(),
		devices:    newDeviceStore(),
		messages:   newMessageStore(),
		threads:    newThreadStore(),
		state: &state{
			focused:      true,
			active:       true,
			networkState: networkStateStarting,
			viewStack:    []view{},
		},
	}

	ui.bounce = chat.Open(ui, &network.TorNetwork{}, getConfigDirectory(), ui.postNotification, nil)
	ui.build()
	go func() {
		ui.loadInitialState(ui.bounce.GetInitialState())
	}()

	ui.app.Run()
	ui.bounce.Shutdown()
}

func StartShimmed(shim chat.Engine, bind func(), poll func(chat.UI)) {
	ui := &ui{
		containers: &containers{},
		views:      &views{},
		widgets:    &widgets{},
		users:      newUserStore(),
		devices:    newDeviceStore(),
		messages:   newMessageStore(),
		threads:    newThreadStore(),
		state: &state{
			focused:      true,
			active:       true,
			networkState: networkStateStarting,
			viewStack:    []view{},
		},
	}

	ui.bounce = shim
	ui.build()
	go func() {
		bind()
		ui.loadInitialState(ui.bounce.GetInitialState())
		poll(ui)
	}()

	if notificationDevice, ok := fyne.CurrentDevice().(mobile.NotificationCallback); ok {
		notificationDevice.SetNotificationCallback(func(display string) {
			// Right now, the display string is just a UUID of the thread to open,
			// but in the future it could be expanded to a proper deep link URI
			threadID, err := uuid.Parse(display)
			if err != nil {
				log.WithFields(log.Fields{
					"display": display,
				}).Error("notification callback has invalid thread ID")
				return
			}

			t, ok := ui.threads.get(threadID)
			if !ok {
				log.WithFields(log.Fields{
					"threadID": threadID,
				}).Error("cannot find thread for notification callback")
				return
			}

			// Reset the mobile view stack so that it is just this thread opened from
			// the main view all threads screen
			ui.state.viewStack = []view{
				view{
					viewType: viewTypeAllThreads,
				},
				view{
					viewType: viewTypeThread,
					context:  threadID,
				},
			}
			fyne.Do(func() {
				ui.displayThread(t)
			})
		})
	}

	ui.app.Run()
	ui.bounce.Shutdown()
}

func (ui *ui) build() {
	// Define the app
	ui.app = app.NewWithID("chat.bounce")
	ui.app.SetIcon(newEmbeddedResource("assets/icon.png"))

	// Keep track of the focused state and use focus to report if the device is active
	var activeTicker *time.Ticker
	var activeTickerCleanup chan bool
	firstTime := true
	ui.app.Lifecycle().SetOnEnteredForeground(func() {
		ui.state.focused = true
		ui.state.active = true
		ui.bounce.SetForeground(true)

		activeTicker = time.NewTicker(5 * time.Second)
		activeTickerCleanup = make(chan bool)
		go func() {
			for {
				select {
				case <-activeTickerCleanup:
					return
				case <-activeTicker.C:
					if ui.bounce != nil {
						ui.bounce.CurrentDeviceActive()
					}
				}
			}
		}()

		if !firstTime {
			// TODO: need to restart the event poll here?
			if fyne.CurrentDevice().IsMobile() {
				go func() {
					if ui.app.Preferences().Bool(darkMode) {
						fyne.Do(func() { setStatusBar(true) })
					} else {
						fyne.Do(func() { setStatusBar(false) })
					}
					if ui.bounce != nil {
						if ui.bounce.NetworkOnline() {
							ui.NetworkOnline()
						} else {
							ui.NetworkOffline()
						}
					}
				}()
			}
		}
		firstTime = false
	})
	ui.app.Lifecycle().SetOnExitedForeground(func() {
		ui.state.focused = false
		ui.state.active = false
		ui.bounce.SetForeground(false)

		if activeTicker != nil {
			activeTicker.Stop()
			activeTickerCleanup <- true
		}
	})

	// Set theme mode
	if ui.app.Preferences().Bool(darkMode) {
		ui.app.Settings().SetTheme(&forcedVariant{Theme: theme.DefaultTheme(), variant: theme.VariantDark})
	} else {
		ui.app.Settings().SetTheme(&forcedVariant{Theme: theme.DefaultTheme(), variant: theme.VariantLight})
	}

	// For some reason, these calls don't work until after the UI is entirely up on Android
	if fyne.CurrentDevice().IsMobile() {
		go func() {
			time.Sleep(1 * time.Second)

			if ui.app.Preferences().Bool(darkMode) {
				setStatusBar(true)
			} else {
				setStatusBar(false)
			}
		}()
	}

	// Define the main window
	ui.window = ui.app.NewWindow("Bounce")
	ui.window.SetMaster()
	//ui.window.SetOnClosed(func() {
	//	if !fyne.CurrentDevice().IsMobile() {
	//		ui.app.Preferences().SetBool("size-set", true)
	//		ui.app.Preferences().SetBool("fullscreen", ui.window.FullScreen())
	//		mainSize := ui.window.Canvas().Size()
	//		mainSize.Width -= 2 // The window appears to always be 2px less wide than the canvas size
	//		ui.app.Preferences().SetFloat("main-width", float64(mainSize.Width))
	//		ui.app.Preferences().SetFloat("main-height", float64(mainSize.Height))
	//		log.WithFields(log.Fields{
	//			"w": mainSize.Width,
	//			"h": mainSize.Height,
	//		}).Warn("closing, saving sizes")
	//	}
	//})
	ui.window.SetCloseIntercept(func() {
		// There's some bug in Fyne where the app will hang when the close button
		// is hit unless this is explicitly set https://github.com/fyne-io/fyne/issues/2314
		ui.Quit()
	})
	//if !fyne.CurrentDevice().IsMobile() {
	//	if ui.app.Preferences().Bool("size-set") {
	//		if ui.app.Preferences().Bool("fullscreen") {
	//			ui.window.SetFullScreen(true)
	//		} else {
	//			w := float32(ui.app.Preferences().Float("main-width"))
	//			h := float32(ui.app.Preferences().Float("main-height"))
	//			log.WithFields(log.Fields{
	//				"w": w,
	//				"h": h,
	//			}).Warn("starting up, sizes")
	//			ui.window.Resize(fyne.Size{Height: h, Width: w})
	//		}
	//	} else {
	ui.window.Resize(fyne.Size{Height: defaultHeight, Width: defaultWidth})
	//	}
	//}
	ui.window.Canvas().SetOnTypedKey(func(ev *fyne.KeyEvent) {
		if fyne.CurrentDevice().IsMobile() && ev.Name == mobile.KeyBack {
			ui.mobileBack()
		}
	})

	if ui.app.Preferences().Bool(sysTray) {
		if desk, ok := ui.app.(desktop.App); ok {
			m := fyne.NewMenu("Bounce",
				fyne.NewMenuItem("Show", func() {
					ui.window.Show()
				}),
			)
			desk.SetSystemTrayMenu(m)
			ui.window.SetCloseIntercept(func() {
				ui.window.Hide()
			})
		}
	}

	ui.buildWidgets()

	// Default to displaying the loading container
	ui.showMainContainer()
	ui.window.Show()
}

func (ui *ui) buildWidgets() {
	ui.widgets.networkOfflineWarning = widget.NewLabelWithStyle("network is starting...", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

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
	ui.buildSettings()
	ui.buildAbout()
	ui.buildNewGroup()
	ui.buildNewDM()
	ui.buildMainContainer()
	ui.buildMobileMenu()
	ui.buildImageViewer()
}

func (ui *ui) askToIgnoreBatteryOptimizations() {
	if ui.app.Preferences().Bool(neverAskForBatteryOptimizations) {
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
				ui.app.Preferences().SetBool(neverAskForBatteryOptimizations, true)
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
				ui.window,
			)
			ui.showDialog(batteryDialog, nil)
		}
	}
}

func (ui *ui) Quit() {
	ui.app.Quit()
}

func (ui *ui) loadInitialState(state chat.InitialState) {
	fyne.DoAndWait(func() {
		if ui.state.initialStateSet {
			log.Fatal("the initial state of the UI can only be loaded once")
		}
		// Refresh the state of the main view after the state is loaded
		ui.state.initialStateSet = true
		defer ui.showMainContainer()

		if state.Profile == nil {
			// This is a brand new installation.  Tell the user interface it must have the
			// user create a profile or sync this device to an existing device group before
			// the app can be used.
			// TODO: log fatal if anything else is set
			return
		}

		ui.state.profile = makeUser(*state.Profile)
		ui.users.add(ui.state.profile)
		ui.state.settings = state.Settings
		ui.askToIgnoreBatteryOptimizations()
		if state.Profile.State.Open {
			ui.NewDirectMessage(*state.Profile)
		}

		for i, _ := range state.SyncDevices {
			dev := state.SyncDevices[i]
			ui.devices.add(&dev)
		}
		ui.updateDeviceStatus()

		threadItems := make(map[uuid.UUID]threadItems)

		for _, u := range state.Users {
			uiUser := makeUser(u)
			ui.users.add(uiUser)

			if u.State.Open {
				ui.NewDirectMessage(u)
			}
		}

		for _, g := range state.Groups {
			member := false // TODO: just use SetGroupState for this?
			for _, bu := range g.Users {
				if ui.state.profile.id == bu.ID {
					member = true
					break
				}
			}
			invited := false
			for _, inviteUser := range g.Invites {
				if ui.state.profile.id == inviteUser.ID {
					invited = true
					break
				}
			}
			if invited && !member {
				ui.buildNewGroupChatInvite(g)
			} else if !invited && member {
				ui.buildNewGroupChat(g)
				t, ok := ui.threads.getGroup(g.ID)
				if !ok {
					log.Fatal("group thread doesn't exist immediately after creation")
				}
				t.buttonData.setLastMessageTime(time.Unix(g.LastActivity, 0))
				t.setLastMessageTime(g.LastActivity)

				gcTi, err := ui.newGroupCreated(g.ID, g.ID, g.CreatedBy, g.CreatedAt)
				if err != nil {
					log.WithFields(log.Fields{
						"error":      err.Error(),
						"group":      g.ID,
						"created_by": g.CreatedBy,
					}).Warn("error creating thread item for group creation while loading initial state")
				}
				threadItems[g.ID] = append(threadItems[g.ID], gcTi)
			} else {
				log.WithFields(log.Fields{
					"group_id": g.ID,
				}).Error("group is neither invited nor member")
			}
		}

		for _, dm := range state.DirectMessages {
			_, ok := ui.threads.getDM(dm.Thread)
			if !ok {
				continue
			}
			dmti, err := ui.newDirectMessage(dm)
			if err != nil {
				log.Error(err.Error())
			}
			threadItems[dm.Thread] = append(threadItems[dm.Thread], dmti)
		}

		for _, udmr := range state.UpdateDMRetentions {
			if _, ok := ui.threads.getDM(udmr.Thread); !ok {
				continue
			}
			udmrItem, err := ui.newUpdateDMRetention(udmr)
			if err != nil {
				log.Error(err.Error())
			}
			threadItems[udmr.Thread] = append(threadItems[udmr.Thread], udmrItem)
		}

		for _, udmch := range state.UpdateDMClearHistories {
			if _, ok := ui.threads.getDM(udmch.Thread); !ok {
				continue
			}
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

		for _, ugiu := range state.UpdateGroupInvitedUsers {
			ugiuItem, err := ui.newUpdateGroupInviteUser(ugiu)
			if err != nil {
				log.Error(err.Error())
			}
			threadItems[ugiu.Thread] = append(threadItems[ugiu.Thread], ugiuItem)
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

		for _, ugir := range state.UpdateGroupRevokedInvites {
			ugirItem, err := ui.newUpdateGroupInviteRevoked(ugir)
			if err != nil {
				log.Error(err.Error())
			}
			threadItems[ugir.Thread] = append(threadItems[ugir.Thread], ugirItem)
		}

		for _, ugia := range state.UpdateGroupAcceptedInvites {
			ugiaItem, err := ui.newUpdateGroupInviteAccepted(ugia)
			if err != nil {
				log.Error(err.Error())
			}
			threadItems[ugia.Thread] = append(threadItems[ugia.Thread], ugiaItem)
		}

		for _, ugir := range state.UpdateGroupRejectedInvites {
			ugirItem, err := ui.newUpdateGroupInviteRejected(ugir)
			if err != nil {
				log.Error(err.Error())
			}
			threadItems[ugir.Thread] = append(threadItems[ugir.Thread], ugirItem)
		}

		for _, uuun := range state.UpdateUserUpdateNames {
			if uuun.User == ui.state.profile.id {
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
						if uuun.Timestamp < t.user.introductionTime {
							return
						}
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
			if uuui.User == ui.state.profile.id {
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
						if uuui.Timestamp < t.user.introductionTime {
							return
						}
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
				if _, ok := t.(*invite); ok {
					return
				}
				ui.populateInitialItems(t, items)
			}
		})

		for _, fp := range state.FileProgress {
			go ui.FileDownloadProgress(fp.ID, fp.Progress)
		}

		for _, d := range state.Drafts {
			t, ok := ui.threads.get(d.Thread)
			if ok {
				t.getEntry().Text = d.Text
				t.getEntry().Refresh()

				t.getButtonData().setDraft(d.Text)
			}
		}

		ui.containers.threads.Refresh()

		if state.DeviceRevoked {
			ui.widgets.networkOfflineWarning.SetText("This device has been revoked")
			ui.widgets.networkOfflineWarning.Importance = widget.DangerImportance
			ui.widgets.networkOfflineWarning.Refresh()
		}
	})
	go ui.messages.writeCache()
}

func (ui *ui) postNotification(_, title, content, _ string, _ []byte) {
	ui.app.SendNotification(fyne.NewNotification(title, content))
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

func (ui *ui) NetworkOnline() {
	if ui.state.activeThread != uuid.Nil {
		t, ok := ui.threads.get(ui.state.activeThread)
		if !ok {
			log.WithFields(log.Fields{
				"thread": ui.state.activeThread,
			}).Error("active thread is not a known user or group")
		}
		_, isGroup := t.(*group)
		_, isInvite := t.(*invite)
		if isGroup || isInvite {
			ui.bounce.GroupConnectionDesired(ui.state.activeThread)
		} else {
			ui.bounce.UserConnectionDesired(ui.state.activeThread)
		}
	}
	fyne.DoAndWait(func() { ui.widgets.networkOfflineWarning.Hide() })
	ui.state.networkState = networkStateOnline
}

func (ui *ui) NetworkOffline() {
	ui.state.networkState = networkStateOffline
	fyne.DoAndWait(func() {
		ui.widgets.networkOfflineWarning.SetText("network connection lost, reconnecting...")
		ui.widgets.networkOfflineWarning.Show()
	})
}

func (ui *ui) UserImported(u chat.User) {
	ui.users.add(makeUser(u))
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
		fyne.Do(func() {
			ei := t.getEditIcon()
			if ei != nil {
				ei.setBackground()
				ei.Refresh()
			}
			hi := t.getHeaderIcon()
			if hi != nil {
				hi.setBackground()
				hi.Refresh()
			}
		})
	})
	fyne.Do(func() {
		ui.containers.threads.Refresh()
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

func (ui *ui) AnotherDeviceActive() {
	ui.state.anotherDeviceActive = true
}

func (ui *ui) NoOtherDeviceActive() {
	ui.state.anotherDeviceActive = false
}

func getConfigDirectory() string {
	var configDirectory string
	if testing.Testing() {
		configDirectory = os.TempDir() + "/bounce-test-" + uuid.New().String()
	} else if runtime.GOOS == "android" {
		configDirectory = "/data/data/chat.bounce/bounce"
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
