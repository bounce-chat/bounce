package ui

import (
	"sync"
	"time"

	"github.com/hkparker/bounce/chat"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

const networkStateStarting = 0
const networkStateOnline = 1
const networkStateOffline = 2

const setupStepInit = 0
const setupStepProfile = 1

//
// An implementation of the Bounce chat.UI interface using Fyne
//
type Fyne struct {
	app                            fyne.App
	mainWindow                     fyne.Window
	mainContainer                  *fyne.Container
	newInstall                     *fyne.Container
	newSyncDevice                  *fyne.Container
	addUser                        *fyne.Container
	newProfileCreator              *fyne.Container
	databaseLoading                *fyne.Container
	editProfile                    *fyne.Container
	displaySyncString              *fyne.Container
	displayAddUserString           *fyne.Container
	settings                       *fyne.Container
	about                          *fyne.Container
	newGroup                       *fyne.Container
	newDM                          *fyne.Container
	introduceContacts              *fyne.Container
	importContact                  *fyne.Container
	threadVBox                     *fyne.Container
	chatContainer                  *fyne.Container
	newGroupSelectedUsersContainer *fyne.Container
	newGroupNameEntry              *widget.Entry
	newGroupAllAvailableUsers      *container.Scroll
	newGroupSelectedUsers          *userStore
	newGroupCreateButton           *widget.Button
	mainMenu                       *fyne.MainMenu
	allUsersDMLinksScroll          *container.Scroll
	networkOfflineWarning          *widget.Label
	groups                         map[uuid.UUID]*group
	dms                            map[uuid.UUID]*directMessage
	threadWithMessage              map[uuid.UUID]thread
	threadWithMessageMutex         sync.Mutex
	activeThread                   uuid.UUID
	syncString                     binding.String
	addUserString                  binding.String
	profile                        *user
	users                          *userStore
	initialStateSet                bool
	focused                        bool
	networkState                   int
	setupStep                      int
	callbacks                      chat.UICallbacks
}

func (fyneUI *Fyne) Build(configDirectory string, callbacks chat.UICallbacks) {
	//
	// Initialize types that require it
	//
	fyneUI.groups = make(map[uuid.UUID]*group)
	fyneUI.dms = make(map[uuid.UUID]*directMessage)
	fyneUI.threadWithMessage = make(map[uuid.UUID]thread)
	fyneUI.users = newUserStore()
	fyneUI.focused = true
	fyneUI.syncString = binding.NewString()
	fyneUI.addUserString = binding.NewString()
	fyneUI.networkOfflineWarning = widget.NewLabelWithStyle("network is starting...", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}) // TODO: red rich text
	fyneUI.networkOfflineWarning.Show()

	//
	// Define the app
	//
	fyneUI.app = app.NewWithID("chat.bounce")
	fyneUI.app.SetIcon(newEmbeddedResource("assets/icon.png"))
	fyneUI.app.Lifecycle().SetOnEnteredForeground(func() { fyneUI.focused = true })
	fyneUI.app.Lifecycle().SetOnExitedForeground(func() { fyneUI.focused = false })

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
	fyneUI.mainWindow.Resize(fyne.Size{Height: 600, Width: 800})
	fyneUI.mainWindow.Show()

	//
	// Build all the containers
	//
	fyneUI.newGroupNameEntry = widget.NewEntry()
	fyneUI.newGroupSelectedUsersContainer = container.NewMax()
	fyneUI.newGroupAllAvailableUsers = container.NewVScroll(container.NewVBox())
	fyneUI.newGroupSelectedUsers = newUserStore()
	fyneUI.allUsersDMLinksScroll = container.NewVScroll(container.NewVBox())
	fyneUI.buildMenu()
	fyneUI.buildNewInstall()
	fyneUI.buildNewSyncDevice()
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
	fyneUI.buildIntroduceContacts()
	fyneUI.buildImportContact()
	fyneUI.buildMainContainer()

	//
	// Hookup callbacks
	//
	fyneUI.callbacks = callbacks

	//
	// Default to displaying the loading container
	//
	fyneUI.networkState = networkStateStarting
	fyneUI.showMainContainer()
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
		fyneUI.profile = &user{id: state.Profile.ID, name: state.Profile.Name} // TODO: use the exported user concept in the UI?
	}

	for _, u := range state.Users {
		fyneUI.users.add(&user{id: u.ID, name: u.Name})
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

	for _, dm := range state.DirectMessages {
		u, exists := fyneUI.users.get(dm.Thread)
		if !exists {
			log.Fatal("dm author not known to UI")
		}
		dmThread, exists := fyneUI.dms[dm.Thread]
		if !exists {
			fyneUI.NewDirectMessage(chat.User{
				ID:   dm.Thread,
				Name: u.name,
			})
			dmThread, exists = fyneUI.dms[dm.Thread]
			if !exists {
				log.Fatal("dm doesn't exist after creation")
			}
		}
		dmti, err := fyneUI.newDirectMessage(dm)
		if err != nil {
			log.Fatal(err.Error())
		}
		dmThread.items = append(dmThread.items, dmti)
	}

	for _, gm := range state.GroupMessages {
		group, exists := fyneUI.groups[gm.Thread]
		if !exists {
			log.Fatal("group doesn't exist for update group retention")
		}
		mti, err := fyneUI.newGroupMessage(gm)
		if err != nil {
			log.Fatal(err.Error())
		}
		group.items = append(group.items, mti)
	}

	for _, ugr := range state.UpdateGroupRetentions {
		group, exists := fyneUI.groups[ugr.GroupID]
		if !exists {
			log.Fatal("group doesn't exist for update group retention")
		}
		ugrItem, err := fyneUI.newUpdateGroupRetention(ugr)
		if err != nil {
			log.Fatal(err.Error())
		}
		group.items = append(group.items, ugrItem)
	}

	for _, ugn := range state.UpdateGroupNames {
		group, exists := fyneUI.groups[ugn.GroupID]
		if !exists {
			log.Fatal("group doesn't exist for update group name")
		}
		ugnItem, err := fyneUI.newUpdateGroupName(ugn)
		if err != nil {
			log.Fatal(err.Error())
		}
		group.items = append(group.items, ugnItem)
	}

	// Create widgets for all the thread items we added
	for _, g := range fyneUI.groups { // TODO: store groups and DMs in a shared threads slice?
		fyneUI.populateGroupItems(g)
		g.chatHistoryScroll().ScrollToBottom() // TODO: only scroll to the first unread message
	}
	for _, u := range fyneUI.dms { // TODO: store groups and DMs in a shared threads slice?
		fyneUI.populateUserItems(u)
		u.chatHistoryScroll().ScrollToBottom() // TODO: only scroll to the first unread message
	}
}

func (fyneUI *Fyne) NetworkOnline() {
	if fyneUI.activeThread != uuid.Nil {
		if _, ok := fyneUI.dms[fyneUI.activeThread]; ok {
			fyneUI.callbacks.UserConnectionDesired(fyneUI.activeThread)
		} else if _, ok = fyneUI.groups[fyneUI.activeThread]; ok {
			//fyneUI.callbacks.GroupConnectionDesired(fyneUI.activeThread)
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
	fyneUI.users.add(&user{id: u.ID, name: u.Name})
	fyneUI.NewDirectMessage(u)
}
