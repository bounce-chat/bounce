package main

import (
	"io"

	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
)

// A shim that takes all calls and serializes them to the chat engine over AIDL
type aidlBounce struct{}

func (b *aidlBounce) AcceptInvite(groupID uuid.UUID) error {
	return nil
}

func (b *aidlBounce) AliasUser(userID uuid.UUID, alias string) error {
	return nil
}

func (b *aidlBounce) BlockGroup(groupID uuid.UUID) error {
	return nil
}

func (b *aidlBounce) BlockUser(userID uuid.UUID) error                              { return nil }
func (b *aidlBounce) CancelDownload(fileID uuid.UUID)                               {}
func (b *aidlBounce) ClearDMChatHistory(userID uuid.UUID) error                     { return nil }
func (b *aidlBounce) ClearGroupChatHistory(groupID uuid.UUID) error                 { return nil }
func (b *aidlBounce) CreateGroup(ng chat.NewGroup) error                            { return nil }
func (b *aidlBounce) CurrentDeviceActive()                                          {}
func (b *aidlBounce) DeleteGroup(groupID uuid.UUID) error                           { return nil }
func (b *aidlBounce) DemoteGroupAdmin(groupID uuid.UUID, userID uuid.UUID) error    { return nil }
func (b *aidlBounce) DownloadFileToDisk(fileID uuid.UUID, destination string)       {}
func (b *aidlBounce) FileDownloaded(fileID uuid.UUID) bool                          { return false }
func (b *aidlBounce) FileEmbedded(fileID uuid.UUID) bool                            { return false }
func (b *aidlBounce) FileWanted(fileID uuid.UUID) bool                              { return false }
func (b *aidlBounce) GetDMHistory(userID uuid.UUID) chat.InitialState               { return chat.InitialState{} }
func (b *aidlBounce) GetFileData(fileID uuid.UUID) ([]byte, error)                  { return []byte{}, nil }
func (b *aidlBounce) GetInitialState() chat.InitialState                            { return chat.InitialState{} }
func (b *aidlBounce) GetNewAddUserString() string                                   { return "" }
func (b *aidlBounce) GetNewSyncString() string                                      { return "" }
func (b *aidlBounce) GroupConnectionDesired(id uuid.UUID)                           {}
func (b *aidlBounce) InviteUserToGroup(groupID uuid.UUID, userID uuid.UUID) error   { return nil }
func (b *aidlBounce) MarkAllDirectMessagesAsRead(userID uuid.UUID)                  {}
func (b *aidlBounce) MarkAllGroupMessagesAsRead(groupID uuid.UUID)                  {}
func (b *aidlBounce) MarkAsRead(id uuid.UUID, frameType string)                     {}
func (b *aidlBounce) PromoteGroupAdmin(groupID uuid.UUID, userID uuid.UUID) error   { return nil }
func (b *aidlBounce) RejectInvite(groupID uuid.UUID) error                          { return nil }
func (b *aidlBounce) RemoveUserFromGroup(groupID uuid.UUID, userID uuid.UUID) error { return nil }
func (b *aidlBounce) RenameDevice(deviceID uuid.UUID, name string) error            { return nil }
func (b *aidlBounce) RenameGroup(groupID uuid.UUID, newName string) error           { return nil }
func (b *aidlBounce) RequestToAddUser(offer string) error                           { return nil }
func (b *aidlBounce) RequestToManageEncryptedDevice(data string) error              { return nil }
func (b *aidlBounce) RequestToSync(data string) error                               { return nil }
func (b *aidlBounce) RestrictGroupEdits(groupID uuid.UUID) error                    { return nil }
func (b *aidlBounce) RestrictPosting(groupID uuid.UUID) error                       { return nil }
func (b *aidlBounce) RestrictUserManagement(groupID uuid.UUID) error                { return nil }
func (b *aidlBounce) RevokeDevice(deviceID uuid.UUID) error                         { return nil }
func (b *aidlBounce) RevokeInvite(groupID uuid.UUID, userID uuid.UUID) error        { return nil }
func (b *aidlBounce) SendDirectMessage(chat.DirectMessage, map[uuid.UUID]io.ReadCloser, map[uuid.UUID]string) {
}
func (b *aidlBounce) SendGroupMessage(chat.GroupMessage, map[uuid.UUID]io.ReadCloser, map[uuid.UUID]string) {
}
func (b *aidlBounce) SetAutoJoinGroups(value int)                              {}
func (b *aidlBounce) SetDMLastOpened(userID uuid.UUID, timestamp int64)        {}
func (b *aidlBounce) SetDMMutedUntil(userID uuid.UUID, mutedUntil int64) error { return nil }
func (b *aidlBounce) SetDMReadReceiptSettings(userID uuid.UUID, override bool, enabled bool) error {
	return nil
}
func (b *aidlBounce) SetDMRetention(userID uuid.UUID, retention int64) error { return nil }
func (b *aidlBounce) SetDMTypingIndicatorSettings(userID uuid.UUID, override bool, enabled bool) error {
	return nil
}
func (b *aidlBounce) SetGroupImage(groupID uuid.UUID, image []byte) error          { return nil }
func (b *aidlBounce) SetGroupLastOpened(groupID uuid.UUID, timestamp int64)        {}
func (b *aidlBounce) SetGroupMutedUntil(groupID uuid.UUID, mutedUntil int64) error { return nil }
func (b *aidlBounce) SetGroupReadReceiptSettings(groupID uuid.UUID, override bool, enabled bool) error {
	return nil
}
func (b *aidlBounce) SetGroupRetention(groupID uuid.UUID, retention int64) error { return nil }
func (b *aidlBounce) SetGroupTypingIndicatorSettings(groupID uuid.UUID, override bool, enabled bool) error {
	return nil
}
func (b *aidlBounce) SetNewDMRetention(value int64)               {}
func (b *aidlBounce) SetNewGroupRestrictGroupEdits(value bool)    {}
func (b *aidlBounce) SetNewGroupRestrictPosting(value bool)       {}
func (b *aidlBounce) SetNewGroupRestrictUserManagement(bool)      {}
func (b *aidlBounce) SetNewGroupRetention(value int64)            {}
func (b *aidlBounce) SetOpenDM(userID uuid.UUID, open bool) error { return nil }
func (b *aidlBounce) SetProfile(profileName string, image []byte, deviceName string) error {
	return nil
}
func (b *aidlBounce) SetReadReceiptsByDefault(value bool)               {}
func (b *aidlBounce) SetTypingIndicatorsByDefault(value bool)           {}
func (b *aidlBounce) SetUserNotes(userID uuid.UUID, notes string) error { return nil }
func (b *aidlBounce) Shutdown()                                         {}
func (b *aidlBounce) TypingInDirectMessage(userID uuid.UUID)            {}
func (b *aidlBounce) TypingInGroup(groupID uuid.UUID)                   {}
func (b *aidlBounce) UnblockUser(userID uuid.UUID) error                { return nil }
func (b *aidlBounce) UnrestrictGroupEdits(groupID uuid.UUID) error      { return nil }
func (b *aidlBounce) UnrestrictPosting(groupID uuid.UUID) error         { return nil }
func (b *aidlBounce) UnrestrictUserManagement(groupID uuid.UUID) error  { return nil }
func (b *aidlBounce) UpdateDraft(threadID uuid.UUID, text string)       {}
func (b *aidlBounce) UpdateProfileImage(newImage []byte) error          { return nil }
func (b *aidlBounce) UpdateProfileName(newName string) error            { return nil }
func (b *aidlBounce) UserConnectionDesired(id uuid.UUID)                {}
