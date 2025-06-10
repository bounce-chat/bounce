package chat

import (
	"github.com/google/uuid"
)

type User struct {
	ID     uuid.UUID
	Name   string
	Images []uuid.UUID
	State  DMState
}

type Settings struct {
	DefaultGroupRetention          int64
	DefaultSendReadReceipts        bool
	DefaultSendTypingIndicators    bool
	NewGroupRestrictUserManagement bool
	NewGroupRestrictGroupEdits     bool
	NewGroupRestrictPosting        bool
}

type LocalSettings struct {
	NeverAskForBatteryOptimizations bool
	DarkMode                        bool
}

type Device struct {
	ID        uuid.UUID
	Name      string
	Address   string
	LastSeen  int64
	CreatedAt int64
	Local     bool
	Online    bool
}

type DMState struct {
	Retention                      int64
	MutedUntil                     int64
	OverrideReadReceiptSetting     bool
	ReadReceiptsEnabled            bool
	OverrideTypingIndicatorSetting bool
	TypingIndicatorsEnabled        bool
}

type FileAttachment struct {
	ID   uuid.UUID
	Name string
	Size int64
}

type ImageAttachment struct {
	ID       uuid.UUID
	Name     string
	Size     int64
	Width    int
	Height   int
	BlurHash string
}

type DirectMessage struct {
	ID               uuid.UUID
	Author           uuid.UUID
	Thread           uuid.UUID
	WrittenAt        int64
	SavedAt          int64
	ExpiresAt        int64
	Text             string
	Seen             bool
	Undeliverable    bool
	ImageAttachments []ImageAttachment
	FileAttachments  []FileAttachment
	ReadReceipts     []ReadReceipt
	DeliveredTo      []uuid.UUID
}

type UpdateDMRetention struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Seen      bool
	Retention int64
}

type UpdateDMClearHistory struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Seen      bool
	ClearTime int64
}

type Group struct {
	ID                             uuid.UUID
	Name                           string
	Images                         []uuid.UUID
	Users                          []User
	Admins                         []uuid.UUID
	BlockedUsers                   []uuid.UUID
	Retention                      int64
	MutedUntil                     int64
	LastActivity                   int64
	CreatedBy                      uuid.UUID
	CreatedAt                      int64
	RestrictUserManagement         bool
	RestrictGroupEdits             bool
	RestrictPosting                bool
	OverrideReadReceiptSetting     bool
	ReadReceiptsEnabled            bool
	OverrideTypingIndicatorSetting bool
	TypingIndicatorsEnabled        bool
}

type GroupMessage struct {
	ID               uuid.UUID
	Author           uuid.UUID
	Thread           uuid.UUID
	WrittenAt        int64
	SavedAt          int64
	ExpiresAt        int64
	Text             string
	Seen             bool
	Undeliverable    bool
	ImageAttachments []ImageAttachment
	FileAttachments  []FileAttachment
	ReadReceipts     []ReadReceipt
	DeliveredTo      []uuid.UUID
}

type UpdateGroupRetention struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Seen      bool
	Retention int64
}

type UpdateGroupName struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Seen      bool
	Name      string
}

type UpdateGroupAddUser struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Seen      bool
	User      User
}

type UpdateGroupRemoveUser struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Seen      bool
	User      uuid.UUID
}

type UpdateGroupClearHistory struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Seen      bool
	ClearTime int64
}

type UpdateGroupAdminPromoted struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Seen      bool
	UserID    uuid.UUID
}

type UpdateGroupAdminDemoted struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Seen      bool
	UserID    uuid.UUID
}

type UpdateGroupUserManagementRestricted struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Seen      bool
}

type UpdateGroupUserManagementUnrestricted struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Seen      bool
}

type UpdateGroupEditsRestricted struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Seen      bool
}

type UpdateGroupEditsUnrestricted struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Seen      bool
}

type UpdateGroupPostingRestricted struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Seen      bool
}

type UpdateGroupPostingUnrestricted struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Seen      bool
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
	Seen      bool
}

type UpdateGroupUserChangedGroupImage struct {
	ID        uuid.UUID
	Thread    uuid.UUID
	Actor     uuid.UUID
	Timestamp int64
	Seen      bool
}

type UpdateUserUpdateName struct {
	ID        uuid.UUID
	User      uuid.UUID
	OldName   string
	Name      string
	Timestamp int64
}

type UpdateUserUpdateImage struct {
	ID        uuid.UUID
	User      uuid.UUID
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
	Settings                               Settings
	LocalSettings                          LocalSettings
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
	UpdateGroupUserBlockedGroups           []UserBlockedGroup // TODO: make name consistent?
	UpdateGroupUserChangedGroupImages      []UpdateGroupUserChangedGroupImage
	UpdateUserUpdateNames                  []UpdateUserUpdateName
	UpdateUserUpdateImages                 []UpdateUserUpdateImage
}

//
// Functions that are passed to bounce that can be used to inform and update the UI
//
type UI interface {
	// App lifecycle
	Quit()

	// Network state
	NetworkOnline()
	NetworkOffline()

	// Device pairing
	NewSyncDeviceAdded()
	SyncDeviceRequestAccepted(uuid.UUID, string, []Device, bool)
	SyncDeviceRequestRejected(peer string)
	InitialSyncStarting()
	InitialSyncProgress(float64)
	InitialSyncComplete()

	// Device management
	DeviceOnline(uuid.UUID)
	DeviceOffline(uuid.UUID)
	DeviceAdded(Device)
	DeviceRevoked(uuid.UUID)
	DeviceRenamed(uuid.UUID, string)
	DeviceLastSeen(uuid.UUID, int64)

	// User management
	AddUserRequestRejected(string)
	UserAdded(User)
	UserImported(User) // TODO: replace with async add user?

	// Direct messages
	DisplayDirectMessage(DirectMessage)
	DisplaySentDirectMessage(DirectMessage)
	SetDMState(uuid.UUID, DMState)
	DMRetentionChanged(UpdateDMRetention)      // The retention settings for a DM have been changed
	DMChatHistoryCleared(UpdateDMClearHistory) // Display that a user has deleted all past DMs

	// Group chats
	OpenNewGroupChat(Group)
	NewGroupChat(Group)
	SetGroupState(Group)
	DisplayGroupMessage(GroupMessage)
	DisplaySentGroupMessage(GroupMessage)
	AddUser(UpdateGroupAddUser)
	RemoveUser(UpdateGroupRemoveUser)
	RemovedFromGroup(RemovedFromGroup)
	GroupDeleted(GroupDeleted)
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
	UserBlockedGroup(UserBlockedGroup)
	UserChangedGroupImage(UpdateGroupUserChangedGroupImage)
	PauseGroupNotifications(uuid.UUID)
	ResumeGroupNotifications(uuid.UUID)

	// Generic thread items
	DeleteItem(uuid.UUID)
	MessageDelivered(messageID, userID uuid.UUID)
	MarkMessageUndeliverable(uuid.UUID)
	MessageSeen(uuid.UUID)           // We read a message on another device
	ReceivedReadReceipt(ReadReceipt) // Someone else read a message of ours
	ShowTypingIndicator(userID, threadID uuid.UUID)
	HideTypingIndicator(userID, threadID uuid.UUID)

	// User settings and status
	SetUserState(User)
	UserNameUpdated(UpdateUserUpdateName)
	UserImageUpdated(UpdateUserUpdateImage)
	UserOnline(userID uuid.UUID)
	UserOffline(userID uuid.UUID)

	// Settings
	SetSettings(Settings)
	SetDarkMode(value bool)

	// File management
	FileCompleted(uuid.UUID)
}

// Frames that support being marked as read
const TypeDirectMessage = "DirectMessage"
const TypeUpdateDM = "UpdateDM"
const TypeGroupMessage = "GroupMessage"
const TypeUpdateGroup = "UpdateGroup"
const TypeGroupCreation = "GroupCreation" // TODO: include this?
const TypeUpdateUser = "UpdateUser"
