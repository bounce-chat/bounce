package ui

import (
	"github.com/hkparker/bounce/chat"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
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
	app                   fyne.App
	mainWindow            fyne.Window
	mainContainer         *fyne.Container
	newInstall            *fyne.Container
	newProfileCreator     *fyne.Container
	networkLoading        *fyne.Container
	databaseLoading       *fyne.Container
	editProfile           *fyne.Container
	settings              *fyne.Container
	about                 *fyne.Container
	newGroup              *fyne.Container
	newDM                 *fyne.Container
	introduceContacts     *fyne.Container
	importContact         *fyne.Container
	threadVBox            *fyne.Container
	chatContainer         *fyne.Container
	mainMenu              *fyne.MainMenu
	allUsersDMLinksScroll *container.Scroll
	networkOfflineWarning *widget.Label
	groups                map[uuid.UUID]*group
	dms                   map[uuid.UUID]*directMessage
	activeThread          uuid.UUID
	profile               *user
	users                 *userStore
	initialStateSet       bool
	networkState          int
	setupStep             int
	callbacks             chat.UICallbacks
}

func (fyneUI *Fyne) Build(configDirectory string, callbacks chat.UICallbacks) {
	//
	// Initialize types that require it
	//
	fyneUI.groups = make(map[uuid.UUID]*group)
	fyneUI.dms = make(map[uuid.UUID]*directMessage)
	fyneUI.users = newUserStore()
	fyneUI.networkOfflineWarning = widget.NewLabelWithStyle("network is offline, reconnecting...", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}) // TODO: red rich text

	//
	// Define the app
	//
	fyneUI.app = app.NewWithID("chat.bounce")
	fyneUI.app.SetIcon(newEmbeddedResource("assets/icon.png"))

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
	fyneUI.allUsersDMLinksScroll = container.NewVScroll(container.NewVBox())
	fyneUI.buildMenu()
	fyneUI.buildNewInstall()
	fyneUI.buildNewProfileCreator()
	fyneUI.buildNetworkLoading()
	fyneUI.buildDatabaseLoading()
	fyneUI.buildEditProfile()
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
	// Default to displaying the "network loading" container
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
	fyneUI.initialStateSet = true
	// Refresh the state of the main view after the state is loaded
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
	//for _, t := range state.Threads {
	//	fyneUI.NewThread(t)
	//}
	for _, dm := range state.DirectMessages {
		fyneUI.loadDirectMessage(dm, true, true) // TODO: need to handle what's read / unread
	}
}

func (fyneUI *Fyne) NetworkOnline() {
	fyneUI.networkOfflineWarning.Hide()
	fyneUI.networkState = networkStateOnline
	fyneUI.showMainContainer()
}

func (fyneUI *Fyne) NetworkDisconnected() {
	fyneUI.networkOfflineWarning.Show()
	fyneUI.networkState = networkStateOffline
	fyneUI.showMainContainer()
}

func (fyneUI *Fyne) UserImported(u chat.User) {
	fyneUI.users.add(&user{id: u.ID, name: u.Name})
}
