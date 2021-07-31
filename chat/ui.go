package chat

//
// User interfaces for bounce are achieved by implementing the BounceUI interface.
//

type BounceUI interface {
	//
	// During initial startup, the following functions are called in order to build the interface
	//

	// Create user interface objects
	Build(configPath string)
	// Define callbacks the interface will use to communicate with the chat ending
	RegisterCallbacks(UICallbacks)
	// Load the initial state
	LoadInitialState(InitialState)

	//
	// These functions control the user interface lifecycle
	//

	// Run displays the user interface and blocks.  A network loading message should be displayed first until NetworkOnline() is called.
	Run()
	// Application is closing due to a fatal error, show down the user interface
	Quit()

	//
	// The following functions can be called at any time
	//

	// The network is ready
	NetworkOnline()
	// Network connection has been lost, go back to displaying a loading message, blocking user interaction
	NetworkDisconnected() // TODO: when internet connection is lost, still let the user browse messages

	// Chats
	//UserIntroduced(Introduction)
	UserImported(User)
	ReceivedDirectMessage(Message)

	NewGroupChat(Group)
	ReceivedGroupMessage(Message)
	//RenameGroup()

	// Profile updates from other devices owned by this user
	//UpdateMyName()
}

type InitialState struct {
	ProfileSet bool // *User
	//Devices  []Device
	Users         []User
	Groups        []Group
	DirecMessages []Message
	GroupMessages []Message
}

type User struct {
	ID    string
	Name  string
	Image []byte
}

type Introduction struct {
	Introducer string
	User       User
}

type Group struct {
	ID      string
	Name    string
	Image   []byte
	UserIDs []string
}

//
// The chat engine will provide these callbacks to a user interface
//
type UICallbacks struct {
	SendMessage                SendMessageCallback
	AddUserToGroup             AddUserToGroupCallback
	RenameGroup                RenameGroupCallback
	ChangeNotificationSettings ChangeNotificationSettingsCallback
	SetProfile                 SetProfileCallback

	//Unimplemented:
	//MessageRead()
}

// The user wants to send a message
type SendMessageCallback func(Message) // TODO: return an error?

// The user wants to add another user to a group
type AddUserToGroupCallback func(groupID, userID string)

// The user wants to rename a group
type RenameGroupCallback func(groupID, newName string)

// The user wants to change the notification settings for a group on all their devices
type ChangeNotificationSettingsCallback func(groupID string, notificationEnabled bool)

// Setup a new profile on a fresh install
type SetProfileCallback func(profileName, deviceName string) error

//
// TODO: to be deleted once I figure out loading messages in order during initial state loading.  Or maybe not?
//

type Message struct {
	ID          string
	CreatedAt   int64
	Read        bool
	Source      string // Always a user's UUID
	Destination string // a user UUID or a group UUID
	Text        string
}
