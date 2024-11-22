package chat

import (
	"github.com/google/uuid"
)

type User struct {
	ID    uuid.UUID
	Name  string
	Image []byte
	State DMState
}

type Device struct {
	ID        uuid.UUID
	Name      string
	Address   string
	CreatedAt int64
	Local     bool
	Online    bool
}

type DMState struct {
	Retention  int64
	MutedUntil int64
}

type DirectMessage struct {
	ID            uuid.UUID
	Author        uuid.UUID
	Thread        uuid.UUID
	WrittenAt     int64
	SavedAt       int64
	Text          string
	Expires       int64
	Read          bool
	Undeliverable bool
	ReadReceipts  []ReadReceipt
}

type UpdateDMRetention struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Read      bool
	Retention int64
}

type UpdateDMClearHistory struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Read      bool
	ClearTime int64
}

type Group struct {
	ID                     uuid.UUID
	Name                   string
	Image                  []byte
	Users                  []User
	Admins                 []uuid.UUID
	BlockedUsers           []uuid.UUID
	Retention              int64
	MutedUntil             int64
	LastActivity           int64
	CreatedBy              uuid.UUID
	CreatedAt              int64
	RestrictUserManagement bool
	RestrictGroupEdits     bool
	RestrictPosting        bool
}

type GroupMessage struct {
	ID            uuid.UUID
	Author        uuid.UUID
	Thread        uuid.UUID
	WrittenAt     int64
	SavedAt       int64
	Text          string
	Expires       int64
	Read          bool
	Undeliverable bool
	ReadReceipts  []ReadReceipt
}

type UpdateGroupRetention struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Read      bool
	Retention int64
}

type UpdateGroupName struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Read      bool
	Name      string
}

type UpdateGroupAddUser struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Read      bool
	User      User
}

type UpdateGroupRemoveUser struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Read      bool
	User      uuid.UUID
}

type UpdateGroupClearHistory struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Read      bool
	ClearTime int64
}

type UpdateGroupAdminPromoted struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Read      bool
	UserID    uuid.UUID
}

type UpdateGroupAdminDemoted struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Read      bool
	UserID    uuid.UUID
}

type UpdateGroupUserManagementRestricted struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Read      bool
}

type UpdateGroupUserManagementUnrestricted struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Read      bool
}

type UpdateGroupEditsRestricted struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Read      bool
}

type UpdateGroupEditsUnrestricted struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Read      bool
}

type UpdateGroupPostingRestricted struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Read      bool
}

type UpdateGroupPostingUnrestricted struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Read      bool
}

type RemovedFromGroup struct {
	Group uuid.UUID
	Actor uuid.UUID
}

type GroupDeleted struct {
	Group uuid.UUID
	Actor uuid.UUID
}

type UserBlockedGroup struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Read      bool
}

type UpdateUserUpdateName struct {
	ID        uuid.UUID
	User      uuid.UUID
	OldName   string
	Name      string
	Timestamp int64
}

type ReadReceipt struct {
	ID     uuid.UUID
	Actor  uuid.UUID
	Target uuid.UUID
	//TargetType string
}

type InitialState struct {
	Profile                                *User
	SyncDevices                            []Device
	Users                                  []User
	Groups                                 []Group
	DirectMessages                         []DirectMessage
	UpdateDMRetentions                     []UpdateDMRetention
	UpdateDMClearHistories                 []UpdateDMClearHistory
	GroupMessages                          []GroupMessage
	UpdateGroupRetentions                  []UpdateGroupRetention
	UpdateGroupNames                       []UpdateGroupName
	UpdateGroupAddUsers                    []UpdateGroupAddUser
	UpdateGroupRemoveUsers                 []UpdateGroupRemoveUser
	UpdateGroupClearHistories              []UpdateGroupClearHistory
	UpdateGroupAdminPromotions             []UpdateGroupAdminPromoted
	UpdateGroupAdminDemotions              []UpdateGroupAdminDemoted
	UpdateGroupUserManagementsRestricted   []UpdateGroupUserManagementRestricted
	UpdateGroupUserManagementsUnrestricted []UpdateGroupUserManagementUnrestricted
	UpdateGroupEditsRestricted             []UpdateGroupEditsRestricted
	UpdateGroupEditsUnrestricted           []UpdateGroupEditsUnrestricted
	UpdateGroupPostingsRestricted          []UpdateGroupPostingRestricted
	UpdateGroupPostingsUnrestricted        []UpdateGroupPostingUnrestricted
	UpdateUserUpdateNames                  []UpdateUserUpdateName
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
	SyncDeviceRequestAccepted(uuid.UUID, string, []Device, bool)
	SyncDeviceRequestRejected(peer string)
	InitialSyncStarting()
	InitialSyncProgress(float64)
	InitialSyncComplete()

	// User management
	AddUserRequestRejected(string)
	FriendAdded(User)

	// Chats
	//UserIntroduced(Introduction)
	UserImported(User) // TODO: still needed?
	DeleteItem(uuid.UUID)
	MarkMessageUndeliverable(uuid.UUID)
	UpdateMessageDeletionTime(uuid.UUID, int64)

	DisplayDirectMessage(DirectMessage)
	SetDMState(uuid.UUID, DMState)
	DMRetentionChanged(UpdateDMRetention)      // The retention settings for a DM have been changed
	DMChatHistoryCleared(UpdateDMClearHistory) // Display that a user has deleted all past DMs

	SetUserName(uuid.UUID, string)
	UserNameUpdated(UpdateUserUpdateName)

	OpenNewGroupChat(Group)
	NewGroupChat(Group)
	SetGroupState(Group)
	DisplayGroupMessage(GroupMessage)
	AddUser(UpdateGroupAddUser)
	RemoveUser(UpdateGroupRemoveUser)
	RemovedFromGroup(RemovedFromGroup)
	GroupDeleted(GroupDeleted)
	UserBlockedGroup(UserBlockedGroup)
	RenameGroup(UpdateGroupName)
	GroupRetentionChanged(UpdateGroupRetention)
	GroupChatHistoryCleared(UpdateGroupClearHistory)
	AdminPromoted(UpdateGroupAdminPromoted)
	AdminDemoted(UpdateGroupAdminDemoted)
	UserManagementRestricted(UpdateGroupUserManagementRestricted)
	UserManagementUnrestricted(UpdateGroupUserManagementUnrestricted)
	GroupEditsRestricted(UpdateGroupEditsRestricted)
	GroupEditsUnrestricted(UpdateGroupEditsUnrestricted)
	PostingRestricted(UpdateGroupPostingRestricted)
	PostingUnrestricted(UpdateGroupPostingUnrestricted)

	ShowTypingIndicatorInHistory(userID, threadID uuid.UUID)
	ShowTypingIndicatorInButton(userID, threadID uuid.UUID)
	HideTypingIndicatorInHistory(userID, threadID uuid.UUID)
	HideTypingIndicatorInButton(threadID uuid.UUID)

	UserIsOnline(userID uuid.UUID)
	UserIsOffline(userID uuid.UUID)

	// Profile updates from other devices owned by this user
	//UpdateMyName()

	// Chat engine updating the delivery status of a message

	// Sync device events
	DeviceOnline(uuid.UUID)
	DeviceOffline(uuid.UUID)
	DeviceAdded(Device)
	DeviceRevoked(uuid.UUID)
	DeviceRenamed(uuid.UUID, string)

	MessageRead(uuid.UUID)           // We read a message on another device
	ReceivedReadReceipt(ReadReceipt) // Someone else read a message of ours
}

// Frames that support being marked as read
const TypeDirectMessage = "DirectMessage"
const TypeUpdateDM = "UpdateDM"
const TypeGroupMessage = "GroupMessage"
const TypeUpdateGroup = "UpdateGroup"
const TypeGroupCreation = "GroupCreation" // TODO: include this?
const TypeUpdateUser = "UpdateUser"

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
	SendDirectMessage func(DirectMessage)
	// The user wants to send a group  message
	SendGroupMessage func(GroupMessage)

	UpdateProfileName func(string) error

	// Called every time a character is entered into an entry to inform the chat engine to send a typing indicator
	TypingInDirectMessage func(userID uuid.UUID)
	TypingInGroup         func(groupID uuid.UUID)

	CreateGroup              func(Group) error                               // Create a new group
	AddUser                  func(groupID, userID uuid.UUID) error           // The user wants to add another user to a group
	RemoveUser               func(groupID, userID uuid.UUID) error           // User wants to remove a user from a group
	RenameGroup              func(groupID uuid.UUID, newName string) error   // The user wants to rename a group
	SetGroupRetention        func(groupID uuid.UUID, retention int64) error  // Set the message retention for a group
	ClearGroupChatHistory    func(groupID uuid.UUID) error                   // Erase all history on all devices
	SetGroupMutedUntil       func(groupID uuid.UUID, mutedUntil int64) error // The user wants to change the notification settings for a group
	PromoteAdmin             func(groupID, userID uuid.UUID) error           // Make a member of a group an admin
	DemoteAdmin              func(groupID, userID uuid.UUID) error           // Remove admin permissions from a member of a group
	RestrictUserManagement   func(groupID uuid.UUID) error                   // Restrict adding and removing users to only admins
	UnrestrictUserManagement func(groupID uuid.UUID) error                   // Allow anyone to add or remove users
	RestrictGroupEdits       func(groupID uuid.UUID) error                   // Restrict editing group properties to admin
	UnrestrictGroupEdits     func(groupID uuid.UUID) error                   // Allow any user to edit group properties
	RestrictPosting          func(groupID uuid.UUID) error                   // Restrict posting to only admins
	UnrestrictPosting        func(groupID uuid.UUID) error                   // Allow any user to post
	DeleteGroup              func(groupID uuid.UUID) error                   // Delete a group
	BlockGroup               func(groupID uuid.UUID) error                   // Block a group

	RenameDevice func(deviceID uuid.UUID, name string) error
	RevokeDevice func(deviceID uuid.UUID) error

	SetDMMutedUntil    func(userID uuid.UUID, mutedUntil int64) error // Set a temporary mute on a DM by setting the unix timestamp to pause notifications until
	SetDMRetention     func(userID uuid.UUID, retention int64) error  // Set the message retention settings for a DM
	ClearDMChatHistory func(userID uuid.UUID) error                   // Clear all DM messages on all devices

	// Setup a new profile on a fresh install
	SetProfile    func(profileName, deviceName string) (uuid.UUID, error)
	ImportUser    func(user []byte) (User, error)
	ExportContact func(name string, expiration int64, oneTime bool) []byte

	// Some user interactions, like opening a DM, indicate that the user might want to send a message to this
	// user soon.  Calling this will cause the chat engine to attempt to dial the user if there isn't already
	// an open connection, reducing the latency in message delivery should the user decide to send a message.
	UserConnectionDesired func(uuid.UUID)

	GroupConnectionDesired func(uuid.UUID)

	MarkRead func(uuid.UUID, string)
}
