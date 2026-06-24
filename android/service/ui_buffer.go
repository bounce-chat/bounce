package goservice

import (
	"encoding/base64"
	"sync"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
	log "github.com/sirupsen/logrus"
)

// A struct that holds calls the UI needs to evaluate for the UI to poll
type uiBuffer struct {
	sync.Mutex

	events []map[int][]byte
}

func (u *uiBuffer) getEvents() string {
	u.Lock()
	defer u.Unlock()

	if len(u.events) == 0 {
		return ""
	}

	bytes, err := msgpack.Marshal(u.events)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling event queue")
	}

	u.events = []map[int][]byte{}

	return base64.StdEncoding.EncodeToString(bytes)
}

func (u *uiBuffer) LoadInitialState(chat.InitialState) {}
func (u *uiBuffer) Run()                               {}
func (u *uiBuffer) Quit()                              {}

func (u *uiBuffer) NetworkOnline() {
	u.Lock()
	defer u.Unlock()

	u.events = append(u.events, map[int][]byte{
		0: []byte("NetworkOnline"),
	})
}

func (u *uiBuffer) NetworkOffline()                                          {}
func (u *uiBuffer) NewSyncDeviceAdded()                                      {}
func (u *uiBuffer) SyncDeviceRequestAccepted(chat.User, []chat.Device, bool) {}
func (u *uiBuffer) SyncDeviceRequestRejected(peer string)                    {}

func (u *uiBuffer) ProfileSet(chatUser chat.User, chatDevice chat.Device) {
	u.Lock()
	defer u.Unlock()

	userBytes, err := msgpack.Marshal(&chatUser)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling user")
		return
	}

	deviceBytes, err := msgpack.Marshal(&chatDevice)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling device")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("ProfileSet"),
		1: userBytes,
		2: deviceBytes,
	})
}

func (u *uiBuffer) InitialSyncStarting()                                                  {}
func (u *uiBuffer) InitialSyncProgress(float64)                                           {}
func (u *uiBuffer) InitialSyncComplete()                                                  {}
func (u *uiBuffer) AddUserRequestRejected(string)                                         {}
func (u *uiBuffer) UserAdded(chat.User)                                                   {}
func (u *uiBuffer) DeleteItem(uuid.UUID)                                                  {}
func (u *uiBuffer) MarkMessageUndeliverable(uuid.UUID)                                    {}
func (u *uiBuffer) DisplayDirectMessage(chat.DirectMessage)                               {}
func (u *uiBuffer) DisplaySentDirectMessage(chat.DirectMessage)                           {}
func (u *uiBuffer) SetDMState(uuid.UUID, chat.DMState)                                    {}
func (u *uiBuffer) DMRetentionChanged(chat.UpdateDMRetention)                             {}
func (u *uiBuffer) DMChatHistoryCleared(chat.UpdateDMClearHistory)                        {}
func (u *uiBuffer) UserAliased(chat.UpdateDMSetAlias)                                     {}
func (u *uiBuffer) SetUserName(uuid.UUID, string)                                         {}
func (u *uiBuffer) UserNameUpdated(chat.UpdateUserUpdateName)                             {}
func (u *uiBuffer) OpenNewGroupChat(chat.Group)                                           {}
func (u *uiBuffer) NewGroupChat(chat.Group)                                               {}
func (u *uiBuffer) SetGroupState(chat.Group)                                              {}
func (u *uiBuffer) DisplaySentGroupMessage(chat.GroupMessage)                             {}
func (u *uiBuffer) DisplayGroupMessage(chat.GroupMessage)                                 {}
func (u *uiBuffer) RemoveUser(chat.UpdateGroupRemoveUser)                                 {}
func (u *uiBuffer) RemovedFromGroup(chat.RemovedFromGroup)                                {}
func (u *uiBuffer) GroupDeleted(chat.GroupDeleted)                                        {}
func (u *uiBuffer) UserBlockedGroup(chat.UserBlockedGroup)                                {}
func (u *uiBuffer) RenameGroup(chat.UpdateGroupName)                                      {}
func (u *uiBuffer) GroupRetentionChanged(chat.UpdateGroupRetention)                       {}
func (u *uiBuffer) GroupChatHistoryCleared(chat.UpdateGroupClearHistory)                  {}
func (u *uiBuffer) AdminPromoted(chat.UpdateGroupAdminPromoted)                           {}
func (u *uiBuffer) AdminDemoted(chat.UpdateGroupAdminDemoted)                             {}
func (u *uiBuffer) UserManagementRestricted(chat.UpdateGroupUserManagementRestricted)     {}
func (u *uiBuffer) UserManagementUnrestricted(chat.UpdateGroupUserManagementUnrestricted) {}
func (u *uiBuffer) GroupEditsRestricted(chat.UpdateGroupEditsRestricted)                  {}
func (u *uiBuffer) GroupEditsUnrestricted(chat.UpdateGroupEditsUnrestricted)              {}
func (u *uiBuffer) PostingRestricted(chat.UpdateGroupPostingRestricted)                   {}
func (u *uiBuffer) PostingUnrestricted(chat.UpdateGroupPostingUnrestricted)               {}
func (u *uiBuffer) InviteUser(chat.UpdateGroupInviteUser)                                 {}
func (u *uiBuffer) RollbackGroup(uuid.UUID)                                               {}
func (u *uiBuffer) NotifyAddedToGroup(string)                                             {}
func (u *uiBuffer) NotifyInvitedToGroup(string)                                           {}
func (u *uiBuffer) GroupInviteRevoked(chat.UpdateGroupInviteRevoked)                      {}
func (u *uiBuffer) GroupInviteAccepted(chat.UpdateGroupInviteAccepted)                    {}
func (u *uiBuffer) GroupInviteRejected(chat.UpdateGroupInviteRejected)                    {}
func (u *uiBuffer) ShowTypingIndicator(userID, threadID uuid.UUID)                        {}
func (u *uiBuffer) HideTypingIndicator(userID, threadID uuid.UUID)                        {}
func (u *uiBuffer) UserOnline(userID uuid.UUID)                                           {}
func (u *uiBuffer) UserOffline(userID uuid.UUID)                                          {}
func (u *uiBuffer) DeviceOnline(uuid.UUID)                                                {}
func (u *uiBuffer) DeviceOffline(uuid.UUID)                                               {}
func (u *uiBuffer) DeviceAdded(chat.Device)                                               {}
func (u *uiBuffer) DeviceRevoked(uuid.UUID)                                               {}
func (u *uiBuffer) DeviceRenamed(uuid.UUID, string)                                       {}
func (u *uiBuffer) DeviceLastSeen(uuid.UUID, int64)                                       {}
func (u *uiBuffer) MessageSeen(uuid.UUID)                                                 {}
func (u *uiBuffer) ReceivedReadReceipt(chat.ReadReceipt)                                  {}
func (u *uiBuffer) SetSettings(chat.Settings)                                             {}
func (u *uiBuffer) MessageDelivered(messageID, userID uuid.UUID)                          {}
func (u *uiBuffer) SetDarkMode(value bool)                                                {}
func (u *uiBuffer) SetUserState(chat.User)                                                {}
func (u *uiBuffer) FileCompleted(uuid.UUID)                                               {}
func (u *uiBuffer) UserChangedGroupImage(chat.UpdateGroupUserChangedGroupImage)           {}
func (u *uiBuffer) UserImageUpdated(chat.UpdateUserUpdateImage)                           {}
func (u *uiBuffer) FileDownloadProgress(uuid.UUID, float64)                               {}
func (u *uiBuffer) CatchUpMessages(chat.BulkUpdate, bool)                                 {}
func (u *uiBuffer) AnotherDeviceActive()                                                  {}
func (u *uiBuffer) NoOtherDeviceActive()                                                  {}
func (u *uiBuffer) EncryptedDeviceAdded()                                                 {}
func (u *uiBuffer) EncryptedDeviceRejected()                                              {}
func (u *uiBuffer) EncryptedDeviceManagable(uuid.UUID)                                    {}
func (u *uiBuffer) EncryptedDeviceUnmanagable(uuid.UUID)                                  {}
func (u *uiBuffer) UpdateDraft(chat.Draft)                                                {}
