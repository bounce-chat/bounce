package chat

import (
	"github.com/google/uuid"
)

//
// User interfaces for bounce are achieved by implementing the UI interface.
//

type UI interface {
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

	NewSyncDeviceAdded()
	SyncDeviceRequestAccepted(uuid.UUID, string) // TODO: better name for these?
	SyncDeviceRequestRejected()

	// Chats
	//UserIntroduced(Introduction)
	UserImported(User)
	ReceivedDirectMessage(DirectMessage)
	DeleteMessage(uuid.UUID)
	MarkMessageUndeliverable(uuid.UUID)
	UpdateMessageDeletionTime(uuid.UUID, int64)

	// The notification settings for a DM have been updated
	DMNotificationsChanged(dm uuid.UUID, enabled bool)

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
// The chat engine will provide these callbacks to a user interface so that the interface can instruct the chat engine
//
type UICallbacks struct {
	// Get a string that can be scanned by a new device in order to become a sync device of this profile
	GetNewSyncString func() string
	RequestToSync    func(string) error

	// The user wants to send a direct message.
	SendDirectMessage func(*DirectMessage) uuid.UUID
	// The user wants to send a group  message
	SendGroupMessage func(GroupMessage) uuid.UUID
	// The user wants to add another user to a group
	AddUserToGroup func(groupID, userID uuid.UUID)
	// The user wants to rename a group
	RenameGroup func(groupID uuid.UUID, newName string)

	// Set if notifications should be enabled for a DM.  Broadcasts to all sync devices.
	SetDMNotificationEnabled func(userID uuid.UUID, notificationEnabled bool)

	// Get the current notification setting for a DM
	GetDMNotificationEnabled func(userID uuid.UUID) (enabled bool, err error)

	// Set a temporary mute on a DM by setting the unix timestamp to pause notifications until
	SetDMNotificationMutedUntil func(userID uuid.UUID, mutedUntil int64)

	// Get the value of a temporary mute on a DM
	GetDMNotificationMutedUntil func(userID uuid.UUID) (mutedUntil int64, err error)

	// The user wants to change the notification settings for a group
	ChangeGroupNotificationSettings func(groupID uuid.UUID, notificationEnabled bool)
	// Setup a new profile on a fresh install
	SetProfile    func(profileName, deviceName string) (uuid.UUID, error)
	ImportUser    func(user []byte) (User, error)
	ExportContact func(name string, expiration int64, oneTime bool) []byte

	// Some user interaction, like opening a DM, indicates that the user might want to send a message to this
	// user soon.  Calling this will cause the chat engine to attempt to dial the user if there isn't already
	// an open connection, reducing the latency in message delivery should the user decide to send a message.
	UserConnectionDesired func(uuid.UUID)
}
