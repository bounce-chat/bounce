package ui

import (
	"github.com/hkparker/bounce/chat"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	log "github.com/sirupsen/logrus"
)

//
// An implementation of the Bounce chat.UI interface using Fyne
//
type Fyne struct {
	app                          fyne.App
	mainWindow                   fyne.Window
	mainContainer                *fyne.Container
	newInstall                   *fyne.Container
	newProfileCreator            *fyne.Container
	networkLoading               *fyne.Container
	databaseLoading              *fyne.Container
	editProfile                  *fyne.Container
	settings                     *fyne.Container
	about                        *fyne.Container
	newGroup                     *fyne.Container
	newDM                        *fyne.Container
	introduceContacts            *fyne.Container
	importContact                *fyne.Container
	threadVBox                   *fyne.Container
	chatContainer                *fyne.Container
	mainMenu                     *fyne.MainMenu
	groups                       map[string]*group
	dms                          map[string]*directMessage
	activeThread                 string
	users                        *userStore
	profileSet                   bool
	initialStateSet              bool
	networkOnline                bool
	onSendMessage                chat.SendMessageCallback
	onAddUserToGroup             chat.AddUserToGroupCallback
	onRenameGroup                chat.RenameGroupCallback
	onChangeNotificationSettings chat.ChangeNotificationSettingsCallback
	onSetProfile                 chat.SetProfileCallback
}

func (fyneUI *Fyne) Build(configDirectory string) {
	//
	// Initialize types that require it
	//
	fyneUI.groups = make(map[string]*group)
	fyneUI.dms = make(map[string]*directMessage)
	fyneUI.users = newUserStore()

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
		fyneUI.Quit()
	})
	fyneUI.mainWindow.Resize(fyne.Size{Height: 600, Width: 800})
	fyneUI.mainWindow.Show()

	//
	// Build all the containers
	//
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
	// Default to displaying the "network loading" container
	//
	fyneUI.showMainContainer()
}

func (fyneUI *Fyne) RegisterCallbacks(callbacks chat.UICallbacks) {
	fyneUI.onSendMessage = callbacks.SendMessage
	fyneUI.onAddUserToGroup = callbacks.AddUserToGroup
	fyneUI.onRenameGroup = callbacks.RenameGroup
	fyneUI.onChangeNotificationSettings = callbacks.ChangeNotificationSettings
	fyneUI.onSetProfile = callbacks.SetProfile
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

	if !state.ProfileSet {
		// This is a brand new installation.  Tell the user interface it must have the
		// user create a profile or sync this device to an existing device group before
		// the app can be used.
		fyneUI.profileSet = false
		// TODO: log fatal if anything else is set
		return
	} else {
		fyneUI.profileSet = true
	}

	for _, u := range state.Users {
		fyneUI.users.add(&user{id: u.ID, name: u.Name})
	}
	//for _, t := range state.Threads {
	//	fyneUI.NewThread(t)
	//}
	//for _, m := range state.Messages {
	//	fyneUI.ReceivedMessage(m) // TODO: except we don't want to mark them as unread (unless they are, I guess)
	//}
}

func (fyneUI *Fyne) NetworkOnline() {
	fyneUI.networkOnline = true
	fyneUI.showMainContainer()
}

func (fyneUI *Fyne) NetworkDisconnected() {
	fyneUI.networkOnline = false
	fyneUI.showMainContainer()
}

func (fyneUI *Fyne) NewUser(id, name string) {
	fyneUI.users.add(&user{id: id, name: name})
}
