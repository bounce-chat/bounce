package goservice

import (
	"encoding/base64"
	"encoding/binary"
	"math"
	"sync"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/bounce-chat/bounce/chat"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

var lastPoll = time.Time{}

// A struct that holds calls the UI needs to evaluate for the UI to poll
type uiBuffer struct {
	sync.Mutex

	events []map[int][]byte
}

func (u *uiBuffer) getEvents() string {
	u.Lock()
	defer u.Unlock()

	restarting := time.Since(lastPoll) > 2*time.Second
	lastPoll = time.Now()

	if len(u.events) == 0 {
		return ""
	}
	if b == nil {
		return ""
	}

	if restarting {
		// The activity polling loop was paused, so we want to
		// send the accumulated UI updates in a more condensed
		// form (chats in a bulk update, no typing indicators)
		u.events = collapse(u.events)
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
	binary.BigEndian.PutUint64(bytes, bits)

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
		0: []byte("DeleteItem"),
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

func (u *uiBuffer) PostingRestricted(ugpr chat.UpdateGroupPostingRestricted) {
	u.Lock()
	defer u.Unlock()

	bytes, err := msgpack.Marshal(&ugpr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("PostingRestricted"),
		1: bytes,
	})
}

func (u *uiBuffer) PostingUnrestricted(ugpu chat.UpdateGroupPostingUnrestricted) {
	u.Lock()
	defer u.Unlock()

	bytes, err := msgpack.Marshal(&ugpu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("PostingUnrestricted"),
		1: bytes,
	})
}

func (u *uiBuffer) InviteUser(ugiu chat.UpdateGroupInviteUser) {
	u.Lock()
	defer u.Unlock()

	bytes, err := msgpack.Marshal(&ugiu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("InviteUser"),
		1: bytes,
	})
}

func (u *uiBuffer) RollbackGroup(id uuid.UUID) {
	u.Lock()
	defer u.Unlock()

	u.events = append(u.events, map[int][]byte{
		0: []byte("RollbackGroup"),
		1: id[:],
	})
}

func (u *uiBuffer) GroupInviteRevoked(ugir chat.UpdateGroupInviteRevoked) {
	u.Lock()
	defer u.Unlock()

	bytes, err := msgpack.Marshal(&ugir)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("GroupInviteRevoked"),
		1: bytes,
	})
}

func (u *uiBuffer) GroupInviteAccepted(ugia chat.UpdateGroupInviteAccepted) {
	u.Lock()
	defer u.Unlock()

	bytes, err := msgpack.Marshal(&ugia)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("GroupInviteAccepted"),
		1: bytes,
	})
}

func (u *uiBuffer) GroupInviteRejected(ugir chat.UpdateGroupInviteRejected) {
	u.Lock()
	defer u.Unlock()

	bytes, err := msgpack.Marshal(&ugir)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("GroupInviteRejected"),
		1: bytes,
	})
}

func (u *uiBuffer) ShowTypingIndicator(userID, threadID uuid.UUID) {
	u.Lock()
	defer u.Unlock()

	u.events = append(u.events, map[int][]byte{
		0: []byte("ShowTypingIndicator"),
		1: userID[:],
		2: threadID[:],
	})
}

func (u *uiBuffer) HideTypingIndicator(userID, threadID uuid.UUID) {
	u.Lock()
	defer u.Unlock()

	u.events = append(u.events, map[int][]byte{
		0: []byte("HideTypingIndicator"),
		1: userID[:],
		2: threadID[:],
	})
}

func (u *uiBuffer) UserOnline(userID uuid.UUID) {
	u.Lock()
	defer u.Unlock()

	u.events = append(u.events, map[int][]byte{
		0: []byte("UserOnline"),
		1: userID[:],
	})
}

func (u *uiBuffer) UserOffline(userID uuid.UUID) {
	u.Lock()
	defer u.Unlock()

	u.events = append(u.events, map[int][]byte{
		0: []byte("UserOffline"),
		1: userID[:],
	})
}

func (u *uiBuffer) DeviceOnline(id uuid.UUID) {
	u.Lock()
	defer u.Unlock()

	u.events = append(u.events, map[int][]byte{
		0: []byte("DeviceOnline"),
		1: id[:],
	})
}

func (u *uiBuffer) DeviceOffline(id uuid.UUID) {
	u.Lock()
	defer u.Unlock()

	u.events = append(u.events, map[int][]byte{
		0: []byte("DeviceOffline"),
		1: id[:],
	})
}

func (u *uiBuffer) DeviceAdded(d chat.Device) {
	u.Lock()
	defer u.Unlock()

	bytes, err := msgpack.Marshal(&d)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("DeviceAdded"),
		1: bytes,
	})
}

func (u *uiBuffer) DeviceRevoked(id uuid.UUID) {
	u.Lock()
	defer u.Unlock()

	u.events = append(u.events, map[int][]byte{
		0: []byte("DeviceRevoked"),
		1: id[:],
	})
}

func (u *uiBuffer) DeviceRenamed(id uuid.UUID, name string) {
	u.Lock()
	defer u.Unlock()

	u.events = append(u.events, map[int][]byte{
		0: []byte("DeviceRenamed"),
		1: id[:],
		2: []byte(name),
	})
}

func (u *uiBuffer) DeviceLastSeen(id uuid.UUID, ts int64) {
	u.Lock()
	defer u.Unlock()

	bytes := make([]byte, 8)
	binary.BigEndian.PutUint64(bytes, uint64(ts))

	u.events = append(u.events, map[int][]byte{
		0: []byte("DeviceLastSeen"),
		1: id[:],
		2: bytes,
	})
}

func (u *uiBuffer) MessageSeen(id uuid.UUID) {
	u.Lock()
	defer u.Unlock()

	u.events = append(u.events, map[int][]byte{
		0: []byte("MessageSeen"),
		1: id[:],
	})
}

func (u *uiBuffer) ReceivedReadReceipt(rr chat.ReadReceipt) {
	u.Lock()
	defer u.Unlock()

	bytes, err := msgpack.Marshal(&rr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("ReceivedReadReceipt"),
		1: bytes,
	})
}

func (u *uiBuffer) SetSettings(s chat.Settings) {
	u.Lock()
	defer u.Unlock()

	bytes, err := msgpack.Marshal(&s)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("SetSettings"),
		1: bytes,
	})
}

func (u *uiBuffer) MessageDelivered(messageID, userID uuid.UUID) {
	u.Lock()
	defer u.Unlock()

	u.events = append(u.events, map[int][]byte{
		0: []byte("MessageDelivered"),
		1: messageID[:],
		2: userID[:],
	})
}

func (u *uiBuffer) SetUserState(chatUser chat.User) {
	u.Lock()
	defer u.Unlock()

	bytes, err := msgpack.Marshal(&chatUser)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("SetUserState"),
		1: bytes,
	})
}

func (u *uiBuffer) FileCompleted(id uuid.UUID) {
	u.Lock()
	defer u.Unlock()

	u.events = append(u.events, map[int][]byte{
		0: []byte("FileCompleted"),
		1: id[:],
	})
}

func (u *uiBuffer) UserChangedGroupImage(ugucgi chat.UpdateGroupUserChangedGroupImage) {
	u.Lock()
	defer u.Unlock()

	bytes, err := msgpack.Marshal(&ugucgi)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("UserChangedGroupImage"),
		1: bytes,
	})
}

func (u *uiBuffer) UserImageUpdated(uuui chat.UpdateUserUpdateImage) {
	u.Lock()
	defer u.Unlock()

	bytes, err := msgpack.Marshal(&uuui)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("UserImageUpdated"),
		1: bytes,
	})
}

func (u *uiBuffer) FileDownloadProgress(id uuid.UUID, progress float64) {
	u.Lock()
	defer u.Unlock()

	bits := math.Float64bits(progress)
	progressBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(progressBytes, bits)

	u.events = append(u.events, map[int][]byte{
		0: []byte("FileDownloadProgress"),
		1: id[:],
		2: progressBytes,
	})
}

func (u *uiBuffer) CatchUpMessages(bu chat.BulkUpdate, initialSync bool) {
	u.Lock()
	defer u.Unlock()

	bytes, err := msgpack.Marshal(&bu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling")
		return
	}

	initialSyncBytes := []byte{0x00}
	if initialSync {
		initialSyncBytes = []byte{0x01}
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("CatchUpMessages"),
		1: bytes,
		2: initialSyncBytes,
	})
}

func (u *uiBuffer) AnotherDeviceActive() {
	u.Lock()
	defer u.Unlock()

	u.events = append(u.events, map[int][]byte{
		0: []byte("AnotherDeviceActive"),
	})
}

func (u *uiBuffer) NoOtherDeviceActive() {
	u.Lock()
	defer u.Unlock()

	u.events = append(u.events, map[int][]byte{
		0: []byte("NoOtherDeviceActive"),
	})
}

func (u *uiBuffer) EncryptedDeviceAdded() {
	u.Lock()
	defer u.Unlock()

	u.events = append(u.events, map[int][]byte{
		0: []byte("EncryptedDeviceAdded"),
	})
}

func (u *uiBuffer) EncryptedDeviceRejected() {
	u.Lock()
	defer u.Unlock()

	u.events = append(u.events, map[int][]byte{
		0: []byte("EncryptedDeviceRejected"),
	})
}

func (u *uiBuffer) EncryptedDeviceManagable(id uuid.UUID) {
	u.Lock()
	defer u.Unlock()

	u.events = append(u.events, map[int][]byte{
		0: []byte("EncryptedDeviceManagable"),
		1: id[:],
	})
}

func (u *uiBuffer) EncryptedDeviceUnmanagable(id uuid.UUID) {
	u.Lock()
	defer u.Unlock()

	u.events = append(u.events, map[int][]byte{
		0: []byte("EncryptedDeviceUnmanagable"),
		1: id[:],
	})
}

func (u *uiBuffer) UpdateDraft(d chat.Draft) {
	u.Lock()
	defer u.Unlock()

	bytes, err := msgpack.Marshal(&d)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling")
		return
	}

	u.events = append(u.events, map[int][]byte{
		0: []byte("UpdateDraft"),
		1: bytes,
	})
}

func collapse(events []map[int][]byte) []map[int][]byte {
	collapsedEvents := []map[int][]byte{}

	bulkUpdate := chat.BulkUpdate{
		Source:         uuid.Nil,
		Seen:           make([]uuid.UUID, 0),
		GroupMessages:  make(map[uuid.UUID][]chat.GroupMessage),
		DirectMessages: make(map[uuid.UUID][]chat.DirectMessage),
		ReadReceipts:   make(map[uuid.UUID][]chat.ReadReceipt),
	}

	lastDraftPerThread := map[uuid.UUID][]byte{}
	lastSetGroupState := map[uuid.UUID][]byte{}

	// TODO
	//lastSetUserState := map[uuid.UUID][]byte{}
	//lastSetDMState := map[uuid.UUID][]byte{}
	//lastSetSettings := map[uuid.UUID][]byte{}

	for _, e := range events {
		switch string(e[0]) {
		case "ShowTypingIndicator":
			continue
		case "DisplayDirectMessage", "DisplaySentDirectMessage":
			dmBytes := e[1]
			var dm chat.DirectMessage
			err := msgpack.Unmarshal(dmBytes, &dm)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error unmarshalling direct message")
				continue
			}
			bulkUpdate.DirectMessages[dm.Thread] = append(bulkUpdate.DirectMessages[dm.Thread], dm)
		case "DisplayGroupMessage", "DisplaySentGroupMessage":
			gmBytes := e[1]
			var gm chat.GroupMessage
			err := msgpack.Unmarshal(gmBytes, &gm)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error unmarshalling group message")
				continue
			}
			bulkUpdate.GroupMessages[gm.Thread] = append(bulkUpdate.GroupMessages[gm.Thread], gm)
		case "ReceivedReadReceipt":
			rrBytes := e[1]
			var rr chat.ReadReceipt
			err := msgpack.Unmarshal(rrBytes, &rr)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error unmarshalling read receipt")
			}

			bulkUpdate.ReadReceipts[rr.Target] = append(bulkUpdate.ReadReceipts[rr.Target], rr)

			if rr.Actor == b.CurrentUserID() {
				bulkUpdate.Seen = append(bulkUpdate.Seen, rr.Target)
			}
		case "UpdateDraft":
			draftBytes := e[1]
			var d chat.Draft
			err := msgpack.Unmarshal(draftBytes, &d)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error unmarshalling draft")
			}
			lastDraftPerThread[d.Thread] = draftBytes
		case "SetGroupState":
			gBytes := e[1]
			var g chat.Group
			err := msgpack.Unmarshal(gBytes, &g)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error unmarshalling group")
			}
			lastSetGroupState[g.ID] = gBytes
		default:
			collapsedEvents = append(collapsedEvents, e)
		}
	}

	bulkBytes, err := msgpack.Marshal(&bulkUpdate)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling bulk update")
	}
	collapsedEvents = append(collapsedEvents, map[int][]byte{
		0: []byte("CatchUpMessages"),
		1: bulkBytes,
		2: []byte{0x00},
	})

	for _, payload := range lastDraftPerThread {
		collapsedEvents = append(collapsedEvents, map[int][]byte{
			0: []byte("UpdateDraft"),
			1: payload,
		})
	}
	for _, payload := range lastSetGroupState {
		collapsedEvents = append(collapsedEvents, map[int][]byte{
			0: []byte("SetGroupState"),
			1: payload,
		})
	}

	return collapsedEvents
}
