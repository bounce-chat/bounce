package goengine

import (
	"encoding/json"

	"github.com/bounce-chat/bounce/chat"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

// uiAdapter implements chat.UI by pushing into the Kotlin sink.
//
// It replaces android/service/ui_buffer.go outright: no mutex-guarded event
// slice, no 500ms poll, no collapse(). Backpressure and conflation are the
// Kotlin channel's job, where they can be expressed explicitly instead of as a
// side effect of a buffer overflowing.
type uiAdapter struct {
	sink EventSink
}

// Fails the build if chat.UI gains or changes a method, which is the point:
// a missed callback would otherwise be an event that silently never reaches
// the UI.
var _ chat.UI = (*uiAdapter)(nil)

// emit marshals v and pushes it under the given kind. Callbacks whose only
// argument is a struct pass it through directly; scalar-only callbacks are
// wrapped so Kotlin always receives a JSON object and never a bare value, which
// keeps the sealed hierarchy on that side uniform.
func (u *uiAdapter) emit(kind string, v any) {
	if v == nil {
		u.sink.OnEvent(kind, "")
		return
	}
	b, err := json.Marshal(v)
	if err != nil {
		log.WithFields(log.Fields{
			"kind":  kind,
			"error": err.Error(),
		}).Error("dropping ui event, marshal failed")
		return
	}
	u.sink.OnEvent(kind, string(b))
}

type idPayload struct {
	ID string `json:"id"`
}

func (u *uiAdapter) emitID(kind string, id uuid.UUID) {
	u.emit(kind, idPayload{ID: id.String()})
}

// --- app lifecycle -----------------------------------------------------------

func (u *uiAdapter) AnotherDeviceActive()  { u.sink.OnEvent("AnotherDeviceActive", "") }
func (u *uiAdapter) NoOtherDeviceActive()  { u.sink.OnEvent("NoOtherDeviceActive", "") }
func (u *uiAdapter) Quit()                 { u.sink.OnEvent("Quit", "") }
func (u *uiAdapter) NetworkOnline()        { u.sink.OnEvent("NetworkOnline", "") }
func (u *uiAdapter) NetworkOffline()       { u.sink.OnEvent("NetworkOffline", "") }
func (u *uiAdapter) NewSyncDeviceAdded()   { u.sink.OnEvent("NewSyncDeviceAdded", "") }
func (u *uiAdapter) InitialSyncStarting()  { u.sink.OnEvent("InitialSyncStarting", "") }
func (u *uiAdapter) InitialSyncPreparing() { u.sink.OnEvent("InitialSyncPreparing", "") }
func (u *uiAdapter) InitialSyncComplete()  { u.sink.OnEvent("InitialSyncComplete", "") }

// --- profile creation and device pairing -------------------------------------

func (u *uiAdapter) ProfileSet(usr chat.User, dev chat.Device) {
	u.emit("ProfileSet", struct {
		User   chat.User   `json:"user"`
		Device chat.Device `json:"device"`
	}{usr, dev})
}

func (u *uiAdapter) SyncDeviceRequestAccepted(usr chat.User, devs []chat.Device, refs bool) {
	u.emit("SyncDeviceRequestAccepted", struct {
		User       chat.User     `json:"user"`
		Devices    []chat.Device `json:"devices"`
		References bool          `json:"references"`
	}{usr, devs, refs})
}

func (u *uiAdapter) SyncDeviceRequestRejected(peer string) {
	u.emit("SyncDeviceRequestRejected", struct {
		Peer string `json:"peer"`
	}{peer})
}

func (u *uiAdapter) AddUserRequestRejected(peer string) {
	u.emit("AddUserRequestRejected", struct {
		Peer string `json:"peer"`
	}{peer})
}

// --- devices -----------------------------------------------------------------

func (u *uiAdapter) DeviceAdded(d chat.Device)  { u.emit("DeviceAdded", &d) }
func (u *uiAdapter) DeviceRevoked(id uuid.UUID) { u.emitID("DeviceRevoked", id) }

func (u *uiAdapter) DeviceRenamed(id uuid.UUID, name string) {
	u.emit("DeviceRenamed", struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}{id.String(), name})
}

func (u *uiAdapter) DeviceLastSeen(id uuid.UUID, ts int64) {
	u.emit("DeviceLastSeen", struct {
		ID       string `json:"id"`
		LastSeen int64  `json:"lastSeen"`
	}{id.String(), ts})
}

func (u *uiAdapter) EncryptedDeviceAdded()    { u.sink.OnEvent("EncryptedDeviceAdded", "") }
func (u *uiAdapter) EncryptedDeviceRejected() { u.sink.OnEvent("EncryptedDeviceRejected", "") }

func (u *uiAdapter) EncryptedDeviceManagable(id uuid.UUID) {
	u.emitID("EncryptedDeviceManagable", id)
}

func (u *uiAdapter) EncryptedDeviceUnmanagable(id uuid.UUID) {
	u.emitID("EncryptedDeviceUnmanagable", id)
}

// --- users -------------------------------------------------------------------

func (u *uiAdapter) UserAdded(usr chat.User)    { u.emit("UserAdded", &usr) }
func (u *uiAdapter) SetUserState(usr chat.User) { u.emit("SetUserState", &usr) }

func (u *uiAdapter) UserNameUpdated(v chat.UpdateUserUpdateName) {
	u.emit("UserNameUpdated", &v)
}

func (u *uiAdapter) UserImageUpdated(v chat.UpdateUserUpdateImage) {
	u.emit("UserImageUpdated", &v)
}

// --- direct messages ---------------------------------------------------------

func (u *uiAdapter) DisplayDirectMessage(dm chat.DirectMessage) {
	u.emit("DisplayDirectMessage", &dm)
}

func (u *uiAdapter) DisplaySentDirectMessage(dm chat.DirectMessage) {
	u.emit("DisplaySentDirectMessage", &dm)
}

func (u *uiAdapter) SetDMState(id uuid.UUID, s chat.DMState) {
	u.emit("SetDMState", struct {
		ID    string       `json:"id"`
		State chat.DMState `json:"state"`
	}{id.String(), s})
}

func (u *uiAdapter) DMRetentionChanged(v chat.UpdateDMRetention) {
	u.emit("DMRetentionChanged", &v)
}

func (u *uiAdapter) DMChatHistoryCleared(v chat.UpdateDMClearHistory) {
	u.emit("DMChatHistoryCleared", &v)
}

func (u *uiAdapter) UserAliased(v chat.UpdateDMSetAlias) { u.emit("UserAliased", &v) }

// --- group chats -------------------------------------------------------------

func (u *uiAdapter) OpenNewGroupChat(g chat.Group) { u.emit("OpenNewGroupChat", &g) }
func (u *uiAdapter) SetGroupState(g chat.Group)    { u.emit("SetGroupState", &g) }

func (u *uiAdapter) DisplayGroupMessage(gm chat.GroupMessage) {
	u.emit("DisplayGroupMessage", &gm)
}

func (u *uiAdapter) DisplaySentGroupMessage(gm chat.GroupMessage) {
	u.emit("DisplaySentGroupMessage", &gm)
}

func (u *uiAdapter) InviteUser(v chat.UpdateGroupInviteUser) { u.emit("InviteUser", &v) }
func (u *uiAdapter) RemoveUser(v chat.UpdateGroupRemoveUser) { u.emit("RemoveUser", &v) }
func (u *uiAdapter) RemovedFromGroup(v chat.RemovedFromGroup) {
	u.emit("RemovedFromGroup", &v)
}
func (u *uiAdapter) GroupDeleted(v chat.GroupDeleted)   { u.emit("GroupDeleted", &v) }
func (u *uiAdapter) RenameGroup(v chat.UpdateGroupName) { u.emit("RenameGroup", &v) }

func (u *uiAdapter) GroupRetentionChanged(v chat.UpdateGroupRetention) {
	u.emit("GroupRetentionChanged", &v)
}

func (u *uiAdapter) GroupChatHistoryCleared(v chat.UpdateGroupClearHistory) {
	u.emit("GroupChatHistoryCleared", &v)
}

func (u *uiAdapter) AdminPromoted(v chat.UpdateGroupAdminPromoted) {
	u.emit("AdminPromoted", &v)
}

func (u *uiAdapter) AdminDemoted(v chat.UpdateGroupAdminDemoted) {
	u.emit("AdminDemoted", &v)
}

func (u *uiAdapter) UserManagementRestricted(v chat.UpdateGroupUserManagementRestricted) {
	u.emit("UserManagementRestricted", &v)
}

func (u *uiAdapter) UserManagementUnrestricted(v chat.UpdateGroupUserManagementUnrestricted) {
	u.emit("UserManagementUnrestricted", &v)
}

func (u *uiAdapter) GroupEditsRestricted(v chat.UpdateGroupEditsRestricted) {
	u.emit("GroupEditsRestricted", &v)
}

func (u *uiAdapter) GroupEditsUnrestricted(v chat.UpdateGroupEditsUnrestricted) {
	u.emit("GroupEditsUnrestricted", &v)
}

func (u *uiAdapter) PostingRestricted(v chat.UpdateGroupPostingRestricted) {
	u.emit("PostingRestricted", &v)
}

func (u *uiAdapter) PostingUnrestricted(v chat.UpdateGroupPostingUnrestricted) {
	u.emit("PostingUnrestricted", &v)
}

func (u *uiAdapter) UserBlockedGroup(v chat.UserBlockedGroup) {
	u.emit("UserBlockedGroup", &v)
}

func (u *uiAdapter) UserChangedGroupImage(v chat.UpdateGroupUserChangedGroupImage) {
	u.emit("UserChangedGroupImage", &v)
}

func (u *uiAdapter) GroupInviteRevoked(v chat.UpdateGroupInviteRevoked) {
	u.emit("GroupInviteRevoked", &v)
}

func (u *uiAdapter) GroupInviteAccepted(v chat.UpdateGroupInviteAccepted) {
	u.emit("GroupInviteAccepted", &v)
}

func (u *uiAdapter) GroupInviteRejected(v chat.UpdateGroupInviteRejected) {
	u.emit("GroupInviteRejected", &v)
}

func (u *uiAdapter) RollbackGroup(id uuid.UUID) { u.emitID("RollbackGroup", id) }

// --- generic thread items ----------------------------------------------------

func (u *uiAdapter) DeleteItem(id uuid.UUID)  { u.emitID("DeleteItem", id) }
func (u *uiAdapter) MessageSeen(id uuid.UUID) { u.emitID("MessageSeen", id) }

func (u *uiAdapter) MarkMessageUndeliverable(id uuid.UUID) {
	u.emitID("MarkMessageUndeliverable", id)
}

func (u *uiAdapter) MessageDelivered(messageID, userID uuid.UUID) {
	u.emit("MessageDelivered", struct {
		MessageID string `json:"messageId"`
		UserID    string `json:"userId"`
	}{messageID.String(), userID.String()})
}

func (u *uiAdapter) ReceivedReadReceipt(rr chat.ReadReceipt) {
	u.emit("ReceivedReadReceipt", &rr)
}

func (u *uiAdapter) UpdateDraft(d chat.Draft) { u.emit("UpdateDraft", &d) }

// CatchUpMessages carries the largest payload in the system. In-process this is
// a plain string handoff; the old build wrote it to a cache file and sent the
// filename.
func (u *uiAdapter) CatchUpMessages(bu chat.BulkUpdate, initialSync bool) {
	u.emit("CatchUpMessages", struct {
		Update      chat.BulkUpdate `json:"update"`
		InitialSync bool            `json:"initialSync"`
	}{bu, initialSync})
}

// --- settings ----------------------------------------------------------------

func (u *uiAdapter) SetSettings(s chat.Settings) { u.emit("SetSettings", &s) }

// --- files -------------------------------------------------------------------

func (u *uiAdapter) FileCompleted(id uuid.UUID) { u.emitID("FileCompleted", id) }

// --- hot paths: typed, no JSON ----------------------------------------------

func (u *uiAdapter) ShowTypingIndicator(userID, threadID uuid.UUID) {
	u.sink.OnTyping(userID.String(), threadID.String(), true)
}

func (u *uiAdapter) HideTypingIndicator(userID, threadID uuid.UUID) {
	u.sink.OnTyping(userID.String(), threadID.String(), false)
}

func (u *uiAdapter) UserOnline(id uuid.UUID)    { u.sink.OnPresence(id.String(), "user", true) }
func (u *uiAdapter) UserOffline(id uuid.UUID)   { u.sink.OnPresence(id.String(), "user", false) }
func (u *uiAdapter) DeviceOnline(id uuid.UUID)  { u.sink.OnPresence(id.String(), "device", true) }
func (u *uiAdapter) DeviceOffline(id uuid.UUID) { u.sink.OnPresence(id.String(), "device", false) }

func (u *uiAdapter) FileDownloadProgress(id uuid.UUID, fraction float64) {
	u.sink.OnProgress(id.String(), "file", fraction)
}

func (u *uiAdapter) InitialSyncProgress(fraction float64) {
	u.sink.OnProgress("", "initialsync", fraction)
}
