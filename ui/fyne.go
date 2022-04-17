package ui

import (
	"sync"

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
	newProfileCreator              *fyne.Container
	databaseLoading                *fyne.Container
	editProfile                    *fyne.Container
	displaySyncString              *fyne.Container
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
	mainMenu                       *fyne.MainMenu
	allUsersDMLinksScroll          *container.Scroll
	networkOfflineWarning          *widget.Label
	groups                         map[uuid.UUID]*group
	dms                            map[uuid.UUID]*directMessage
	threadWithMessage              map[uuid.UUID]thread
	threadWithMessageMutex         sync.Mutex
	activeThread                   uuid.UUID
	syncString                     binding.String
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
	log.Debug("initializing types") // TODO: remove after startup bug identified
	fyneUI.groups = make(map[uuid.UUID]*group)
	fyneUI.dms = make(map[uuid.UUID]*directMessage)
	fyneUI.threadWithMessage = make(map[uuid.UUID]thread)
	fyneUI.users = newUserStore()
	fyneUI.focused = true
	fyneUI.syncString = binding.NewString()
	fyneUI.networkOfflineWarning = widget.NewLabelWithStyle("network is starting...", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}) // TODO: red rich text
	fyneUI.networkOfflineWarning.Show()
	fyneUI.newGroupNameEntry = widget.NewEntry()
	fyneUI.newGroupSelectedUsersContainer = container.NewMax()
	fyneUI.newGroupAllAvailableUsers = container.NewVScroll(container.NewVBox())
	fyneUI.newGroupSelectedUsers = newUserStore()

	//
	// Define the app
	//
	log.Debug("defining the app") // TODO: remove after startup bug identified
	fyneUI.app = app.NewWithID("chat.bounce")
	fyneUI.app.SetIcon(newEmbeddedResource("assets/icon.png"))
	fyneUI.app.Lifecycle().SetOnEnteredForeground(func() { fyneUI.focused = true })
	fyneUI.app.Lifecycle().SetOnExitedForeground(func() { fyneUI.focused = false })

	//
	// Define the main window
	//
	log.Debug("defining main window") // TODO: remove after startup bug identified
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
	log.Debug("building containers") // TODO: remove after startup bug identified
	fyneUI.allUsersDMLinksScroll = container.NewVScroll(container.NewVBox())
	log.Debug("buildMenu") // TODO: remove after startup bug identified
	fyneUI.buildMenu()
	log.Debug("buildNewInstall") // TODO: remove after startup bug identified
	fyneUI.buildNewInstall()
	log.Debug("buildNewSyncDevice") // TODO: remove after startup bug identified
	fyneUI.buildNewSyncDevice()
	log.Debug("buildNewProfileCreator") // TODO: remove after startup bug identified
	fyneUI.buildNewProfileCreator()
	log.Debug("buildDatabaseLoading") // TODO: remove after startup bug identified
	fyneUI.buildDatabaseLoading()
	log.Debug("buildEditProfile") // TODO: remove after startup bug identified
	fyneUI.buildEditProfile()
	log.Debug("buildDisplaySyncString") // TODO: remove after startup bug identified
	fyneUI.buildDisplaySyncString()
	log.Debug("buildSettings") // TODO: remove after startup bug identified
	fyneUI.buildSettings()
	log.Debug("buildAbout") // TODO: remove after startup bug identified
	fyneUI.buildAbout()
	log.Debug("buildNewGroup") // TODO: remove after startup bug identified
	fyneUI.buildNewGroup()
	log.Debug("buildNewDM") // TODO: remove after startup bug identified
	fyneUI.buildNewDM()
	log.Debug("buildIntroduceContacts") // TODO: remove after startup bug identified
	fyneUI.buildIntroduceContacts()
	log.Debug("buildImportContact") // TODO: remove after startup bug identified
	fyneUI.buildImportContact()
	log.Debug("buildMainContainer") // TODO: remove after startup bug identified
	fyneUI.buildMainContainer()

	//
	// Hookup callbacks
	//
	log.Debug("hooking up callbacks") // TODO: remove after startup bug identified
	fyneUI.callbacks = callbacks

	//
	// Default to displaying the loading container
	//
	log.Debug("showing main container") // TODO: remove after startup bug identified
	fyneUI.networkState = networkStateStarting
	fyneUI.showMainContainer()
	log.Debug("app built") // TODO: remove after startup bug identified
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
		log.Debug("setting the profile")                                       // TODO: debugging startup issue
		fyneUI.profile = &user{id: state.Profile.ID, name: state.Profile.Name} // TODO: use the exported user concept in the UI?
	}

	for _, u := range state.Users {
		log.Debug("adding a user") // TODO: debugging startup issue
		fyneUI.users.add(&user{id: u.ID, name: u.Name})
	}
	//for _, t := range state.Threads {
	//	fyneUI.NewThread(t)
	//}
	for _, dm := range state.DirectMessages {
		log.Debug("loading a direct message")    // TODO: debugging startup issue
		fyneUI.loadDirectMessage(dm, true, true) // TODO: need to handle what's read / unread
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
