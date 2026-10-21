package chat

type User struct {
	ID               ID
	Name             string
	Images           []ID
	State            DMState
	IntroductionTime int64
	Alias            string
	Notes            string
	Blocked          bool
}

type Settings struct {
	DefaultGroupRetention          int64
	DefaultSendReadReceipts        bool
	DefaultSendTypingIndicators    bool
	NewGroupRestrictUserManagement bool
	NewGroupRestrictGroupEdits     bool
	NewGroupRestrictPosting        bool
	AutoJoinGroups                 int
	DefaultDMRetention             int64
}

type Device struct {
	ID        ID
	Name      string
	Address   string
	LastSeen  int64
	CreatedAt int64
	Local     bool
	Encrypted bool
	Online    bool
}

type DMState struct {
	Open                           bool
	Retention                      int64
	MutedUntil                     int64
	LastActivity                   int64
	OverrideReadReceiptSetting     bool
	ReadReceiptsEnabled            bool
	OverrideTypingIndicatorSetting bool
	TypingIndicatorsEnabled        bool
	LastOpened                     int64
}

type FileAttachment struct {
	ID       ID
	Name     string
	Size     int64
	Progress float64
}

type ImageAttachment struct {
	ID       ID
	Name     string
	Size     int64
	Width    int
	Height   int
	BlurHash string
}

type DirectMessage struct {
	ID               ID
	Author           ID
	Thread           ID
	WrittenAt        int64
	SavedAt          int64
	ExpiresAt        int64
	Text             string
	Seen             bool
	Undeliverable    bool
	ImageAttachments []ImageAttachment
	FileAttachments  []FileAttachment
	ReadReceipts     []ReadReceipt
	DeliveredTo      []ID
}

type UpdateDMRetention struct {
	ID        ID
	Thread    ID
	Actor     ID
	Timestamp int64
	Seen      bool
	Retention int64
}

type UpdateDMClearHistory struct {
	ID        ID
	Thread    ID
	Actor     ID
	Timestamp int64
	Seen      bool
	ClearTime int64
}

type UpdateDMSetAlias struct {
	ID        ID
	User      ID
	Timestamp int64
	Alias     string
}

type Group struct {
	ID                             ID
	Name                           string
	Images                         []ID
	Users                          []User
	Admins                         []ID
	Invites                        []User
	InvitedBy                      ID
	InvitedAt                      int64
	AcceptedAt                     int64
	BlockedUsers                   []ID
	Retention                      int64
	MutedUntil                     int64
	LastActivity                   int64
	CreatedBy                      ID
	CreatedAt                      int64
	RestrictUserManagement         bool
	RestrictGroupEdits             bool
	RestrictPosting                bool
	OverrideReadReceiptSetting     bool
	ReadReceiptsEnabled            bool
	OverrideTypingIndicatorSetting bool
	TypingIndicatorsEnabled        bool
	LastOpened                     int64
}

type NewGroup struct {
	Name                   string
	Image                  []byte
	InitialInvites         []ID
	Retention              int64
	RestrictUserManagement bool
	RestrictGroupEdits     bool
	RestrictPosting        bool
}

type GroupMessage struct {
	ID               ID
	Author           ID
	Thread           ID
	WrittenAt        int64
	SavedAt          int64
	ExpiresAt        int64
	Text             string
	Seen             bool
	Undeliverable    bool
	ImageAttachments []ImageAttachment
	FileAttachments  []FileAttachment
	ReadReceipts     []ReadReceipt
	DeliveredTo      []ID
}

type UpdateGroupRetention struct {
	ID        ID
	Thread    ID
	Actor     ID
	Timestamp int64
	Seen      bool
	Retention int64
}

type UpdateGroupName struct {
	ID        ID
	Thread    ID
	Actor     ID
	Timestamp int64
	Seen      bool
	Name      string
}

type UpdateGroupInviteUser struct {
	ID        ID
	Thread    ID
	Actor     ID
	Timestamp int64
	Seen      bool
	User      User
}

type UpdateGroupRemoveUser struct {
	ID        ID
	Thread    ID
	Actor     ID
	Timestamp int64
	Seen      bool
	User      ID
}

type UpdateGroupClearHistory struct {
	ID        ID
	Thread    ID
	Actor     ID
	Timestamp int64
	Seen      bool
	ClearTime int64
}

type UpdateGroupAdminPromoted struct {
	ID        ID
	Thread    ID
	Actor     ID
	Timestamp int64
	Seen      bool
	UserID    ID
}

type UpdateGroupAdminDemoted struct {
	ID        ID
	Thread    ID
	Actor     ID
	Timestamp int64
	Seen      bool
	UserID    ID
}

type UpdateGroupUserManagementRestricted struct {
	ID        ID
	Thread    ID
	Actor     ID
	Timestamp int64
	Seen      bool
}

type UpdateGroupUserManagementUnrestricted struct {
	ID        ID
	Thread    ID
	Actor     ID
	Timestamp int64
	Seen      bool
}

type UpdateGroupEditsRestricted struct {
	ID        ID
	Thread    ID
	Actor     ID
	Timestamp int64
	Seen      bool
}

type UpdateGroupEditsUnrestricted struct {
	ID        ID
	Thread    ID
	Actor     ID
	Timestamp int64
	Seen      bool
}

type UpdateGroupPostingRestricted struct {
	ID        ID
	Thread    ID
	Actor     ID
	Timestamp int64
	Seen      bool
}

type UpdateGroupPostingUnrestricted struct {
	ID        ID
	Thread    ID
	Actor     ID
	Timestamp int64
	Seen      bool
}

type RemovedFromGroup struct {
	Group ID
	Actor ID
}

type GroupDeleted struct {
	Group ID
	Actor ID
}

type UserBlockedGroup struct {
	ID        ID
	Thread    ID
	Actor     ID
	Timestamp int64
	Seen      bool
}

type UpdateGroupUserChangedGroupImage struct {
	ID        ID
	Thread    ID
	Actor     ID
	Timestamp int64
	Seen      bool
}

type UpdateGroupInviteRevoked struct {
	ID        ID
	Thread    ID
	Actor     ID
	Timestamp int64
	UserID    ID
	Seen      bool
}

type UpdateGroupInviteAccepted struct {
	ID        ID
	Thread    ID
	Actor     ID
	Timestamp int64
	Seen      bool
}

type UpdateGroupInviteRejected struct {
	ID        ID
	Thread    ID
	Actor     ID
	Timestamp int64
	Seen      bool
}

type UpdateUserUpdateName struct {
	ID        ID
	User      ID
	OldName   string
	Name      string
	Timestamp int64
}

type UpdateUserUpdateImage struct {
	ID        ID
	User      ID
	Timestamp int64
}

type ReadReceipt struct {
	ID     ID
	Actor  ID
	Target ID
}

type FileProgress struct {
	ID       ID
	Progress float64
}

type Draft struct {
	Thread ID
	Text   string
}

type BulkUpdate struct {
	Source         ID
	Seen           []ID
	GroupMessages  map[ID][]GroupMessage
	DirectMessages map[ID][]DirectMessage
	ReadReceipts   map[ID][]ReadReceipt
}

type InitialState struct {
	DeviceRevoked                          bool
	NetworkOnline                          bool
	Profile                                *User
	Settings                               Settings
	SyncDevices                            []Device
	Users                                  []User
	Groups                                 []Group
	DirectMessages                         []DirectMessage
	UpdateDMRetentions                     []UpdateDMRetention
	UpdateDMClearHistories                 []UpdateDMClearHistory
	GroupMessages                          []GroupMessage
	UpdateGroupRetentions                  []UpdateGroupRetention
	UpdateGroupNames                       []UpdateGroupName
	UpdateGroupInvitedUsers                []UpdateGroupInviteUser
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
	UpdateGroupRevokedInvites              []UpdateGroupInviteRevoked
	UpdateGroupAcceptedInvites             []UpdateGroupInviteAccepted
	UpdateGroupRejectedInvites             []UpdateGroupInviteRejected
	UpdateUserUpdateNames                  []UpdateUserUpdateName
	UpdateUserUpdateImages                 []UpdateUserUpdateImage
	FileProgress                           []FileProgress
	Drafts                                 []Draft
}

// Functions that are passed to bounce that can be used to inform and update the UI
type UI interface {
	// App lifecycle
	AnotherDeviceActive()
	NoOtherDeviceActive()
	Quit()

	// Network state
	NetworkOnline()
	NetworkOffline()

	// Profile creation and device pairing
	ProfileSet(User, Device)
	NewSyncDeviceAdded()
	SyncDeviceRequestAccepted(User, []Device, bool)
	SyncDeviceRequestRejected(peer string)
	InitialSyncStarting()
	InitialSyncProgress(float64)
	InitialSyncPreparing()
	InitialSyncComplete()

	// Device management
	DeviceOnline(ID)
	DeviceOffline(ID)
	DeviceAdded(Device)
	DeviceRevoked(ID)
	DeviceRenamed(ID, string)
	DeviceLastSeen(ID, int64)
	EncryptedDeviceAdded()
	EncryptedDeviceRejected()
	EncryptedDeviceUnmanagable(ID)
	EncryptedDeviceManagable(ID)

	// User management
	AddUserRequestRejected(string)
	UserAdded(User)

	// Direct messages
	DisplayDirectMessage(DirectMessage)
	DisplaySentDirectMessage(DirectMessage)
	SetDMState(ID, DMState)
	DMRetentionChanged(UpdateDMRetention)      // The retention settings for a DM have been changed
	DMChatHistoryCleared(UpdateDMClearHistory) // Display that a user has deleted all past DMs
	UserAliased(UpdateDMSetAlias)

	// Group chats
	OpenNewGroupChat(Group)
	SetGroupState(Group)
	DisplayGroupMessage(GroupMessage)
	DisplaySentGroupMessage(GroupMessage)
	InviteUser(UpdateGroupInviteUser)
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
	GroupInviteRevoked(UpdateGroupInviteRevoked)
	GroupInviteAccepted(UpdateGroupInviteAccepted)
	GroupInviteRejected(UpdateGroupInviteRejected)
	RollbackGroup(ID)

	// Generic thread items
	DeleteItem(ID)
	MessageDelivered(messageID, userID ID)
	MarkMessageUndeliverable(ID)
	MessageSeen(ID)                  // We read a message on another device
	ReceivedReadReceipt(ReadReceipt) // Someone else read a message of ours
	ShowTypingIndicator(userID, threadID ID)
	HideTypingIndicator(userID, threadID ID)
	CatchUpMessages(BulkUpdate, bool)
	UpdateDraft(Draft)

	// User settings and status
	SetUserState(User)
	UserNameUpdated(UpdateUserUpdateName)
	UserImageUpdated(UpdateUserUpdateImage)
	UserOnline(userID ID)
	UserOffline(userID ID)

	// Settings
	SetSettings(Settings)

	// File management
	FileCompleted(ID)
	FileDownloadProgress(ID, float64)
}

// Frames that support being marked as read
const TypeDirectMessage = "DirectMessage"
const TypeUpdateDM = "UpdateDM"
const TypeGroupMessage = "GroupMessage"
const TypeUpdateGroup = "UpdateGroup"
const TypeGroupCreation = "GroupCreation" // TODO: include this?
const TypeUpdateUser = "UpdateUser"
