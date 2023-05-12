package chat

import (
	"github.com/google/uuid"
)

type User struct {
	ID    uuid.UUID
	Name  string
	Image []byte
}

type DirectMessage struct {
	ID        uuid.UUID
	Author    uuid.UUID
	Thread    uuid.UUID
	WrittenAt int64
	Text      string
}

type Group struct {
	ID      uuid.UUID
	Name    string
	Image   []byte
	UserIDs []uuid.UUID
}

//type GroupMessage struct {
//	ID        uuid.UUID
//	Author    uuid.UUID
//	Thread    uuid.UUID
//	WrittenAt int64
//	Text      string
//}

type InitialState struct {
	Profile        *User
	Users          []User
	Groups         []Group
	DirectMessages []DirectMessage
	GroupMessages  []GroupMessage
}

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
	NetworkOffline()

	NewSyncDeviceAdded()
	SyncDeviceRequestAccepted(uuid.UUID, string)
	SyncDeviceRequestRejected(peer string)

	// User management
	AddUserRequestRejected(string)
	FriendAdded(User)

	// Chats
	//UserIntroduced(Introduction)
	UserImported(User) // TODO: still needed?
	ReceivedDirectMessage(DirectMessage)
	DeleteMessage(uuid.UUID)
	MarkMessageUndeliverable(uuid.UUID)
	UpdateMessageDeletionTime(uuid.UUID, int64)

	// The notification settings for a DM have been updated
	DMMutedUntilChanged(dm uuid.UUID, mutedUntil int64)

	// The retention settings for a DM have been changed
	DMRetentionChanged(dm uuid.UUID, actor uuid.UUID, retention int64, timestamp int64)

	// Display that a user has deleted all past DMs
	DMChatHistoryCleared(dm, actor uuid.UUID)

	OpenNewGroupChat(Group)
	NewGroupChat(Group)
	ReceivedGroupMessage(GroupMessage)
	RenameGroup(groupID, actorID uuid.UUID, newName string)
	GroupRetentionChanged(groupID uuid.UUID, actorID uuid.UUID, retention int64, timestamp int64)
	GroupChatHistoryCleared(groupID uuid.UUID, actorID uuid.UUID)
	GroupMutedUntilChanged(groupID uuid.UUID, mutedUntil int64)

	ShowTypingIndicatorInHistory(userID, threadID uuid.UUID) // TODO: why did I split these?
	ShowTypingIndicatorInButton(userID, threadID uuid.UUID)
	HideTypingIndicatorInHistory(userID, threadID uuid.UUID)
	HideTypingIndicatorInButton(threadID uuid.UUID)

	// Profile updates from other devices owned by this user
	//UpdateMyName()

	// Chat engine updating the delivery status of a message
}

//
// The chat engine will provide these callbacks to a user interface so that the interface can instruct the chat engine
//
type UICallbacks struct {
	// Get a string that can be scanned by a new device in order to become a sync device of this profile
	GetNewSyncString func() string
	RequestToSync    func(string) error

	// Adding friends
	GetNewAddUserString func() string
	RequestToAddUser    func(string) error

	// The user wants to send a direct message.
	SendDirectMessage func(*DirectMessage)
	// The user wants to send a group  message
	SendGroupMessage func(*GroupMessage) uuid.UUID

	// Called every time a character is entered into an entry to inform the chat engine to send a typing indicator
	TypingInDirectMessage func(userID uuid.UUID)
	TypingInGroup         func(groupID uuid.UUID)

	// Create a new group
	CreateGroup func(groupName string, userIDs []uuid.UUID) error
	// The user wants to add another user to a group
	AddUserToGroup func(groupID, userID uuid.UUID) error
	// The user wants to rename a group
	RenameGroup func(groupID uuid.UUID, newName string) error
	// Set the message retention for a group
	SetGroupRetention func(groupID uuid.UUID, retention int64) error
	// Get the current retention settings for a group
	GetGroupRetention func(groupID uuid.UUID) int64 // TODO: should return an error if the group isn't found?
	// Erase all history on all devices
	ClearGroupChatHistory func(groupID uuid.UUID) error
	// Get the muted until setting for a group
	GetGroupMutedUntil func(groupID uuid.UUID) (int64, error)
	// The user wants to change the notification settings for a group
	SetGroupMutedUntil func(groupID uuid.UUID, mutedUntil int64) error

	// Set a temporary mute on a DM by setting the unix timestamp to pause notifications until
	SetDMMutedUntil func(userID uuid.UUID, mutedUntil int64) error

	// Get the value of a temporary mute on a DM
	GetDMMutedUntil func(userID uuid.UUID) (mutedUntil int64, err error)

	// Set the message retention settings for a DM
	SetDMRetention func(userID uuid.UUID, retention int64) error

	// Get the message retention settings for a DM
	GetDMRetention func(userID uuid.UUID) int64 // TODO: should return an error if the group isn't found?

	// Clear all DM messages on all devices
	ClearDMChatHistory func(userID uuid.UUID) error

	// Setup a new profile on a fresh install
	SetProfile    func(profileName, deviceName string) (uuid.UUID, error)
	ImportUser    func(user []byte) (User, error)
	ExportContact func(name string, expiration int64, oneTime bool) []byte

	// Some user interactions, like opening a DM, indicate that the user might want to send a message to this
	// user soon.  Calling this will cause the chat engine to attempt to dial the user if there isn't already
	// an open connection, reducing the latency in message delivery should the user decide to send a message.
	UserConnectionDesired func(uuid.UUID)

	GroupConnectionDesired func(uuid.UUID)
}
