package goservice

import (
	"encoding/base64"
	"encoding/binary"
	"math"
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

func (u *uiBuffer) NetworkOffline() {
	u.Lock()
	defer u.Unlock()

	u.events = append(u.events, map[int][]byte{
		0: []byte("NetworkOffline"),
	})
}

func (u *uiBuffer) NewSyncDeviceAdded() {
	u.Lock()
	defer u.Unlock()

	u.events = append(u.events, map[int][]byte{
		0: []byte("NewSyncDeviceAdded"),
	})
}

func (u *uiBuffer) SyncDeviceRequestAccepted(chatUser chat.User, chatDevices []chat.Device, references bool) {
	u.Lock()
	defer u.Unlock()

	userBytes, err := msgpack.Marshal(&chatUser)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling user")
		return
	}

	devicesBytes, err := msgpack.Marshal(&chatDevices)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling devices")
		return
	}

	referencesByte := []byte{0x00}
	if references {
		referencesByte = []byte{0x01}
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("SyncDeviceRequestAccepted"),
		1: userBytes,
		2: devicesBytes,
		3: referencesByte,
	})
}

func (u *uiBuffer) SyncDeviceRequestRejected(peer string) {
	u.Lock()
	defer u.Unlock()

	u.events = append(u.events, map[int][]byte{
		0: []byte("SyncDeviceRequestRejected"),
		1: []byte(peer),
	})
}

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

func (u *uiBuffer) InitialSyncStarting() {
	u.Lock()
	defer u.Unlock()

	u.events = append(u.events, map[int][]byte{
		0: []byte("InitialSyncStarting"),
	})
}

func (u *uiBuffer) InitialSyncProgress(f float64) {
	u.Lock()
	defer u.Unlock()

	bits := math.Float64bits(f)
	bytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(bytes, bits)

	u.events = append(u.events, map[int][]byte{
		0: []byte("InitialSyncProgress"),
		1: bytes,
	})
}

func (u *uiBuffer) InitialSyncComplete() {
	u.Lock()
	defer u.Unlock()

	u.events = append(u.events, map[int][]byte{
		0: []byte("InitialSyncComplete"),
	})
}

func (u *uiBuffer) AddUserRequestRejected(peer string) {
	u.Lock()
	defer u.Unlock()

	u.events = append(u.events, map[int][]byte{
		0: []byte("AddUserRequestRejected"),
		1: []byte(peer),
	})
}

func (u *uiBuffer) UserAdded(chatUser chat.User) {
	u.Lock()
	defer u.Unlock()

	userBytes, err := msgpack.Marshal(&chatUser)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling user")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("UserAdded"),
		1: userBytes,
	})
}

func (u *uiBuffer) DeleteItem(id uuid.UUID) {
	u.Lock()
	defer u.Unlock()

	u.events = append(u.events, map[int][]byte{
		0: []byte("AddUserRequestRejected"),
		1: id[:],
	})
}

func (u *uiBuffer) MarkMessageUndeliverable(id uuid.UUID) {
	u.Lock()
	defer u.Unlock()

	u.events = append(u.events, map[int][]byte{
		0: []byte("MarkMessageUndeliverable"),
		1: id[:],
	})
}

func (u *uiBuffer) DisplayDirectMessage(dm chat.DirectMessage) {
	u.Lock()
	defer u.Unlock()

	dmBytes, err := msgpack.Marshal(&dm)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling dm")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("DisplayDirectMessage"),
		1: dmBytes,
	})
}

func (u *uiBuffer) DisplaySentDirectMessage(dm chat.DirectMessage) {
	u.Lock()
	defer u.Unlock()

	dmBytes, err := msgpack.Marshal(&dm)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling dm")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("DisplaySentDirectMessage"),
		1: dmBytes,
	})
}

func (u *uiBuffer) SetDMState(id uuid.UUID, state chat.DMState) {
	u.Lock()
	defer u.Unlock()

	stateBytes, err := msgpack.Marshal(&state)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling DM state")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("SetDMState"),
		1: id[:],
		2: stateBytes,
	})
}

func (u *uiBuffer) DMRetentionChanged(udmr chat.UpdateDMRetention) {
	u.Lock()
	defer u.Unlock()

	udmrBytes, err := msgpack.Marshal(&udmr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling udmr")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("DMRetentionChanged"),
		1: udmrBytes,
	})
}

func (u *uiBuffer) DMChatHistoryCleared(udch chat.UpdateDMClearHistory) {
	u.Lock()
	defer u.Unlock()

	udchBytes, err := msgpack.Marshal(&udch)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling udch")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("DMChatHistoryCleared"),
		1: udchBytes,
	})
}

func (u *uiBuffer) UserAliased(udsa chat.UpdateDMSetAlias) {
	u.Lock()
	defer u.Unlock()

	udsaBytes, err := msgpack.Marshal(&udsa)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling udsa")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("UserAliased"),
		1: udsaBytes,
	})
}

func (u *uiBuffer) UserNameUpdated(uuun chat.UpdateUserUpdateName) {
	u.Lock()
	defer u.Unlock()

	uuunBytes, err := msgpack.Marshal(&uuun)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling uuun")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("UserNameUpdated"),
		1: uuunBytes,
	})
}

func (u *uiBuffer) OpenNewGroupChat(g chat.Group) {
	u.Lock()
	defer u.Unlock()

	gBytes, err := msgpack.Marshal(&g)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling group")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("OpenNewGroupChat"),
		1: gBytes,
	})
}

func (u *uiBuffer) SetGroupState(g chat.Group) {
	u.Lock()
	defer u.Unlock()

	gBytes, err := msgpack.Marshal(&g)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling group")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("SetGroupState"),
		1: gBytes,
	})
}

func (u *uiBuffer) DisplaySentGroupMessage(gm chat.GroupMessage) {
	u.Lock()
	defer u.Unlock()

	gmBytes, err := msgpack.Marshal(&gm)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling group message")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("DisplaySentGroupMessage"),
		1: gmBytes,
	})
}

func (u *uiBuffer) DisplayGroupMessage(gm chat.GroupMessage) {
	u.Lock()
	defer u.Unlock()

	gmBytes, err := msgpack.Marshal(&gm)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling group message")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("DisplayGroupMessage"),
		1: gmBytes,
	})
}

func (u *uiBuffer) RemoveUser(ugru chat.UpdateGroupRemoveUser) {
	u.Lock()
	defer u.Unlock()

	ugruBytes, err := msgpack.Marshal(&ugru)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling ugru")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("RemoveUser"),
		1: ugruBytes,
	})
}

func (u *uiBuffer) RemovedFromGroup(rfg chat.RemovedFromGroup) {
	u.Lock()
	defer u.Unlock()

	rfgBytes, err := msgpack.Marshal(&rfg)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("RemovedFromGroup"),
		1: rfgBytes,
	})
}

func (u *uiBuffer) GroupDeleted(gd chat.GroupDeleted) {
	u.Lock()
	defer u.Unlock()

	gdBytes, err := msgpack.Marshal(&gd)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling group deleted")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("GroupDeleted"),
		1: gdBytes,
	})
}

func (u *uiBuffer) UserBlockedGroup(ubg chat.UserBlockedGroup) {
	u.Lock()
	defer u.Unlock()

	ubgBytes, err := msgpack.Marshal(&ubg)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("UserBlockedGroup"),
		1: ubgBytes,
	})
}

func (u *uiBuffer) RenameGroup(ugn chat.UpdateGroupName) {
	u.Lock()
	defer u.Unlock()

	ugnBytes, err := msgpack.Marshal(&ugn)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("RenameGroup"),
		1: ugnBytes,
	})
}

func (u *uiBuffer) GroupRetentionChanged(ugr chat.UpdateGroupRetention) {
	u.Lock()
	defer u.Unlock()

	ugrBytes, err := msgpack.Marshal(&ugr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("GroupRetentionChanged"),
		1: ugrBytes,
	})
}

func (u *uiBuffer) GroupChatHistoryCleared(ugch chat.UpdateGroupClearHistory) {
	u.Lock()
	defer u.Unlock()

	ugchBytes, err := msgpack.Marshal(&ugch)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("GroupChatHistoryCleared"),
		1: ugchBytes,
	})
}

func (u *uiBuffer) AdminPromoted(ugap chat.UpdateGroupAdminPromoted) {
	u.Lock()
	defer u.Unlock()

	bytes, err := msgpack.Marshal(&ugap)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("AdminPromoted"),
		1: bytes,
	})
}

func (u *uiBuffer) AdminDemoted(ugad chat.UpdateGroupAdminDemoted) {
	u.Lock()
	defer u.Unlock()

	bytes, err := msgpack.Marshal(&ugad)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("AdminDemoted"),
		1: bytes,
	})
}

func (u *uiBuffer) UserManagementRestricted(ugumr chat.UpdateGroupUserManagementRestricted) {
	u.Lock()
	defer u.Unlock()

	bytes, err := msgpack.Marshal(&ugumr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("UserManagementRestricted"),
		1: bytes,
	})
}

func (u *uiBuffer) UserManagementUnrestricted(ugumu chat.UpdateGroupUserManagementUnrestricted) {
	u.Lock()
	defer u.Unlock()

	bytes, err := msgpack.Marshal(&ugumu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("UserManagementUnrestricted"),
		1: bytes,
	})
}

func (u *uiBuffer) GroupEditsRestricted(uger chat.UpdateGroupEditsRestricted) {
	u.Lock()
	defer u.Unlock()

	bytes, err := msgpack.Marshal(&uger)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("GroupEditsRestricted"),
		1: bytes,
	})
}

func (u *uiBuffer) GroupEditsUnrestricted(ugeu chat.UpdateGroupEditsUnrestricted) {
	u.Lock()
	defer u.Unlock()

	bytes, err := msgpack.Marshal(&ugeu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("GroupEditsUnrestricted"),
		1: bytes,
	})
}

func (u *uiBuffer) PostingRestricted(chat.UpdateGroupPostingRestricted)         {}
func (u *uiBuffer) PostingUnrestricted(chat.UpdateGroupPostingUnrestricted)     {}
func (u *uiBuffer) InviteUser(chat.UpdateGroupInviteUser)                       {}
func (u *uiBuffer) RollbackGroup(uuid.UUID)                                     {}
func (u *uiBuffer) NotifyAddedToGroup(string)                                   {}
func (u *uiBuffer) NotifyInvitedToGroup(string)                                 {}
func (u *uiBuffer) GroupInviteRevoked(chat.UpdateGroupInviteRevoked)            {}
func (u *uiBuffer) GroupInviteAccepted(chat.UpdateGroupInviteAccepted)          {}
func (u *uiBuffer) GroupInviteRejected(chat.UpdateGroupInviteRejected)          {}
func (u *uiBuffer) ShowTypingIndicator(userID, threadID uuid.UUID)              {}
func (u *uiBuffer) HideTypingIndicator(userID, threadID uuid.UUID)              {}
func (u *uiBuffer) UserOnline(userID uuid.UUID)                                 {}
func (u *uiBuffer) UserOffline(userID uuid.UUID)                                {}
func (u *uiBuffer) DeviceOnline(uuid.UUID)                                      {}
func (u *uiBuffer) DeviceOffline(uuid.UUID)                                     {}
func (u *uiBuffer) DeviceAdded(chat.Device)                                     {}
func (u *uiBuffer) DeviceRevoked(uuid.UUID)                                     {}
func (u *uiBuffer) DeviceRenamed(uuid.UUID, string)                             {}
func (u *uiBuffer) DeviceLastSeen(uuid.UUID, int64)                             {}
func (u *uiBuffer) MessageSeen(uuid.UUID)                                       {}
func (u *uiBuffer) ReceivedReadReceipt(chat.ReadReceipt)                        {}
func (u *uiBuffer) SetSettings(chat.Settings)                                   {}
func (u *uiBuffer) MessageDelivered(messageID, userID uuid.UUID)                {}
func (u *uiBuffer) SetDarkMode(value bool)                                      {}
func (u *uiBuffer) SetUserState(chat.User)                                      {}
func (u *uiBuffer) FileCompleted(uuid.UUID)                                     {}
func (u *uiBuffer) UserChangedGroupImage(chat.UpdateGroupUserChangedGroupImage) {}
func (u *uiBuffer) UserImageUpdated(chat.UpdateUserUpdateImage)                 {}
func (u *uiBuffer) FileDownloadProgress(uuid.UUID, float64)                     {}
func (u *uiBuffer) CatchUpMessages(chat.BulkUpdate, bool)                       {}
func (u *uiBuffer) AnotherDeviceActive()                                        {}
func (u *uiBuffer) NoOtherDeviceActive()                                        {}
func (u *uiBuffer) EncryptedDeviceAdded()                                       {}
func (u *uiBuffer) EncryptedDeviceRejected()                                    {}
func (u *uiBuffer) EncryptedDeviceManagable(uuid.UUID)                          {}
func (u *uiBuffer) EncryptedDeviceUnmanagable(uuid.UUID)                        {}
func (u *uiBuffer) UpdateDraft(chat.Draft)                                      {}
