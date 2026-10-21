package chat

import (
	"io"
)

type Engine interface {
	AcceptInvite(groupID ID) error
	AliasUser(userID ID, alias string) error
	BlockGroup(groupID ID) error
	BlockUser(userID ID) error
	CancelDownload(fileID ID)
	ClearDMChatHistory(userID ID) error
	ClearGroupChatHistory(groupID ID) error
	CreateGroup(ng NewGroup) error
	CurrentDeviceActive()
	CurrentUserID() ID
	DeleteGroup(groupID ID) error
	DemoteGroupAdmin(groupID ID, userID ID) error
	DownloadFileToDisk(fileID ID, destination string)
	FileDownloaded(fileID ID) bool
	FileEmbedded(fileID ID) bool
	FileWanted(fileID ID) bool
	GetDMHistory(userID ID) InitialState
	GetFileData(fileID ID) ([]byte, error)
	GetFilePath(fileID ID) (string, error)
	GetInitialState() InitialState
	GetNewAddUserString() string
	GetNewSyncString() string
	GroupConnectionDesired(id ID)
	InviteUserToGroup(groupID ID, userID ID) error
	MarkAllDirectMessagesAsRead(userID ID)
	MarkAllGroupMessagesAsRead(groupID ID)
	MarkAsRead(id ID, frameType string)
	NetworkOnline() bool
	PromoteGroupAdmin(groupID ID, userID ID) error
	RejectInvite(groupID ID) error
	RemoveUserFromGroup(groupID ID, userID ID) error
	RenameDevice(deviceID ID, name string) error
	RenameGroup(groupID ID, newName string) error
	RequestToAddUser(offer string) error
	RequestToManageEncryptedDevice(data string) error
	RequestToSync(data string) error
	RestrictGroupEdits(groupID ID) error
	RestrictPosting(groupID ID) error
	RestrictUserManagement(groupID ID) error
	RevokeDevice(deviceID ID) error
	RevokeInvite(groupID ID, userID ID) error
	SendDirectMessage(DirectMessage, map[ID]io.ReadCloser, map[ID]string)
	SendGroupMessage(GroupMessage, map[ID]io.ReadCloser, map[ID]string)
	SetActiveThread(id ID)
	SetAutoJoinGroups(value int)
	SetDMLastOpened(userID ID, timestamp int64)
	SetDMMutedUntil(userID ID, mutedUntil int64) error
	SetDMReadReceiptSettings(userID ID, override bool, enabled bool) error
	SetDMRetention(userID ID, retention int64) error
	SetDMTypingIndicatorSettings(userID ID, override bool, enabled bool) error
	SetForeground(bool)
	SetGroupImage(groupID ID, image []byte) error
	SetGroupLastOpened(groupID ID, timestamp int64)
	SetGroupMutedUntil(groupID ID, mutedUntil int64) error
	SetGroupReadReceiptSettings(groupID ID, override bool, enabled bool) error
	SetGroupRetention(groupID ID, retention int64) error
	SetGroupTypingIndicatorSettings(groupID ID, override bool, enabled bool) error
	SetNewDMRetention(value int64)
	SetNewGroupRestrictGroupEdits(value bool)
	SetNewGroupRestrictPosting(value bool)
	SetNewGroupRestrictUserManagement(value bool)
	SetNewGroupRetention(value int64)
	SetNotificationIcon(thread string, icon []byte)
	SetOpenDM(userID ID, open bool) error
	SetProfile(profileName string, image []byte, deviceName string) error
	SetReadReceiptsByDefault(value bool)
	SetScrolledDown(id ID, value bool)
	SetTypingIndicatorsByDefault(value bool)
	SetUserNotes(userID ID, notes string) error
	Shutdown()
	TypingInDirectMessage(userID ID)
	TypingInGroup(groupID ID)
	UnblockUser(userID ID) error
	UnrestrictGroupEdits(groupID ID) error
	UnrestrictPosting(groupID ID) error
	UnrestrictUserManagement(groupID ID) error
	UpdateDraft(threadID ID, text string)
	UpdateProfileImage(newImage []byte) error
	UpdateProfileName(newName string) error
	UserConnectionDesired(id ID)
	VersionString() string
}
