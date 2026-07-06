package chat

import (
	"io"

	"github.com/google/uuid"
)

type Engine interface {
	AcceptInvite(groupID uuid.UUID) error
	AliasUser(userID uuid.UUID, alias string) error
	BlockGroup(groupID uuid.UUID) error
	BlockUser(userID uuid.UUID) error
	CancelDownload(fileID uuid.UUID)
	ClearDMChatHistory(userID uuid.UUID) error
	ClearGroupChatHistory(groupID uuid.UUID) error
	CreateGroup(ng NewGroup) error
	CurrentDeviceActive()
	CurrentUserID() uuid.UUID
	DeleteGroup(groupID uuid.UUID) error
	DemoteGroupAdmin(groupID uuid.UUID, userID uuid.UUID) error
	DownloadFileToDisk(fileID uuid.UUID, destination string)
	FileDownloaded(fileID uuid.UUID) bool
	FileEmbedded(fileID uuid.UUID) bool
	FileWanted(fileID uuid.UUID) bool
	GetDMHistory(userID uuid.UUID) InitialState
	GetFileData(fileID uuid.UUID) ([]byte, error)
	GetInitialState() InitialState
	GetNewAddUserString() string
	GetNewSyncString() string
	GroupConnectionDesired(id uuid.UUID)
	InviteUserToGroup(groupID uuid.UUID, userID uuid.UUID) error
	MarkAllDirectMessagesAsRead(userID uuid.UUID)
	MarkAllGroupMessagesAsRead(groupID uuid.UUID)
	MarkAsRead(id uuid.UUID, frameType string)
	NetworkOnline() bool
	PromoteGroupAdmin(groupID uuid.UUID, userID uuid.UUID) error
	RejectInvite(groupID uuid.UUID) error
	RemoveUserFromGroup(groupID uuid.UUID, userID uuid.UUID) error
	RenameDevice(deviceID uuid.UUID, name string) error
	RenameGroup(groupID uuid.UUID, newName string) error
	RequestToAddUser(offer string) error
	RequestToManageEncryptedDevice(data string) error
	RequestToSync(data string) error
	RestrictGroupEdits(groupID uuid.UUID) error
	RestrictPosting(groupID uuid.UUID) error
	RestrictUserManagement(groupID uuid.UUID) error
	RevokeDevice(deviceID uuid.UUID) error
	RevokeInvite(groupID uuid.UUID, userID uuid.UUID) error
	SendDirectMessage(DirectMessage, map[uuid.UUID]io.ReadCloser, map[uuid.UUID]string)
	SendGroupMessage(GroupMessage, map[uuid.UUID]io.ReadCloser, map[uuid.UUID]string)
	SetActiveThread(id uuid.UUID)
	SetAutoJoinGroups(value int)
	SetDMLastOpened(userID uuid.UUID, timestamp int64)
	SetDMMutedUntil(userID uuid.UUID, mutedUntil int64) error
	SetDMReadReceiptSettings(userID uuid.UUID, override bool, enabled bool) error
	SetDMRetention(userID uuid.UUID, retention int64) error
	SetDMTypingIndicatorSettings(userID uuid.UUID, override bool, enabled bool) error
	SetForeground(bool)
	SetGroupImage(groupID uuid.UUID, image []byte) error
	SetGroupLastOpened(groupID uuid.UUID, timestamp int64)
	SetGroupMutedUntil(groupID uuid.UUID, mutedUntil int64) error
	SetGroupReadReceiptSettings(groupID uuid.UUID, override bool, enabled bool) error
	SetGroupRetention(groupID uuid.UUID, retention int64) error
	SetGroupTypingIndicatorSettings(groupID uuid.UUID, override bool, enabled bool) error
	SetNewDMRetention(value int64)
	SetNewGroupRestrictGroupEdits(value bool)
	SetNewGroupRestrictPosting(value bool)
	SetNewGroupRestrictUserManagement(value bool)
	SetNewGroupRetention(value int64)
	SetNotificationIcon(thread string, icon []byte)
	SetOpenDM(userID uuid.UUID, open bool) error
	SetProfile(profileName string, image []byte, deviceName string) error
	SetReadReceiptsByDefault(value bool)
	SetScrolledDown(id uuid.UUID, value bool)
	SetTypingIndicatorsByDefault(value bool)
	SetUserNotes(userID uuid.UUID, notes string) error
	Shutdown()
	TypingInDirectMessage(userID uuid.UUID)
	TypingInGroup(groupID uuid.UUID)
	UnblockUser(userID uuid.UUID) error
	UnrestrictGroupEdits(groupID uuid.UUID) error
	UnrestrictPosting(groupID uuid.UUID) error
	UnrestrictUserManagement(groupID uuid.UUID) error
	UpdateDraft(threadID uuid.UUID, text string)
	UpdateProfileImage(newImage []byte) error
	UpdateProfileName(newName string) error
	UserConnectionDesired(id uuid.UUID)
}
