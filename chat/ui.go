package chat

import (
	"github.com/google/uuid"
)

//
// User interfaces for bounce are achieved by implementing the BounceUI interface.
//

type BounceUI interface {
	// Create user interface objects
	Build(configPath string, callbacks UICallbacks)

	// Load the initial state
	LoadInitialState(InitialState)

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
	NetworkOffline() // TODO: when internet connection is lost, still let the user browse messages

	// Chats
	//UserIntroduced(Introduction)
	UserImported(User)
	ReceivedDirectMessage(DirectMessage)

	NewGroupChat(Group)
	ReceivedGroupMessage(GroupMessage)
	//RenameGroup()

	// Profile updates from other devices owned by this user
	//UpdateMyName()

	// Chat engine updating the delivery status of a message
}

type InitialState struct {
	//ProfileSet bool // *User
	Profile *User
	//Devices  []Device
	Users          []User
	Groups         []Group
	DirectMessages []DirectMessage
	//GroupMessages []Message
}

type User struct { // TODO: replace with model?
	ID    uuid.UUID
	Name  string
	Image []byte
}

//type Introduction struct {
//	Introducer string
//	User       User
//}

type Group struct { // TODO: replace with model?
	ID      uuid.UUID
	Name    string
	Image   []byte
	UserIDs []uuid.UUID
}

//
// The chat engine will provide these callbacks to a user interface
//
type UICallbacks struct {
	// The user wants to send a direct message.
	SendDirectMessage func(*DirectMessage) uuid.UUID
	// The user wants to send a group  message
	SendGroupMessage func(GroupMessage) // TODO: return the UUID?
	// The user wants to add another user to a group
	AddUserToGroup func(groupID, userID uuid.UUID)
	// The user wants to rename a group
	RenameGroup func(groupID uuid.UUID, newName string)
	// The user wants to change the notification settings for a group on all their devices
	ChangeNotificationSettings func(groupID uuid.UUID, notificationEnabled bool)
	// Setup a new profile on a fresh install
	SetProfile    func(profileName, deviceName string) (uuid.UUID, error)
	ImportUser    func(user []byte) error
	ExportContact func(name string, expiration int64, oneTime bool) []byte
	// Tell the chat engine that some user interaction makes indicates a message might soon be sent to this user
	UserConnectionDesired func(uuid.UUID)
	//Unimplemented:
	//MessageRead()
}
