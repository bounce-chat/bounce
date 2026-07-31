package goengine

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/bounce-chat/bounce/chat"
	"github.com/bounce-chat/bounce/config"
	"github.com/bounce-chat/bounce/network"
	"github.com/google/uuid"
)

var errNotStarted = errors.New("engine not started")

// Engine is the bound handle around chat.Engine. All fields are unexported:
// gobind generates getters and setters for exported struct fields and we want
// none of that surface.
type Engine struct {
	mu sync.RWMutex
	e  chat.Engine

	// userID caches the local profile's ID. chat.Engine.CurrentUserID() calls
	// log.Fatal when no profile row exists, which would take the whole process
	// down, so this package never calls it speculatively - it is populated from
	// SetProfile and from GetInitialState instead.
	userID string
}

// NewEngine allocates an unstarted handle. Named NewEngine rather than New
// because gobind renders package functions as static methods and `new` is a
// Java keyword.
func NewEngine() *Engine { return &Engine{} }

// DefaultConfigDir is where the engine keeps its database, blobs and Tor keys.
// On Android this is fixed at /data/data/chat.bounce/bounce by
// config.GetConfigDirectory, which is why the app's applicationId must stay
// chat.bounce.
func DefaultConfigDir() string { return config.GetConfigDirectory() }

// Start constructs the chat engine and returns once it is running.
//
// BLOCKING: opens the sqlite database, loads keys and starts the embedded Tor
// stack. This takes seconds, not milliseconds. Never call it on the main thread.
func (a *Engine) Start(configDir string, events EventSink, notes NotificationSink) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.e != nil {
		return errors.New("engine already started")
	}
	if events == nil {
		return errors.New("an event sink is required")
	}
	if notes == nil {
		return errors.New("a notification sink is required")
	}
	if configDir == "" {
		configDir = config.GetConfigDirectory()
	}

	// Before chat.Open, so the Tor bootstrap is visible. chat.Open only sets a
	// level and adds its file hook, so it will not undo this.
	installPlatformLogger()

	post := func(id, title, content, openThread string, icon []byte) {
		notes.Post(id, title, content, openThread, icon)
	}
	clear := func(id string) {
		notes.Clear(id)
	}

	a.e = chat.Open(&uiAdapter{sink: events}, &network.TorNetwork{}, configDir, post, clear)
	return nil
}

// Stop shuts the engine down. Safe to call more than once.
func (a *Engine) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.e != nil {
		a.e.Shutdown()
		a.e = nil
	}
}

// Ready reports whether Start has completed.
func (a *Engine) Ready() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.e != nil
}

// engine returns the live engine or an error. This replaces the old eval.go
// `for b == nil { sleep(500ms) }` spin, which blocked a Binder thread with no
// timeout and no way to tell "not ready" from "hung".
func (a *Engine) engine() (chat.Engine, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.e == nil {
		return nil, errNotStarted
	}
	return a.e, nil
}

func (a *Engine) setUserID(id string) {
	a.mu.Lock()
	a.userID = id
	a.mu.Unlock()
}

// CurrentUserID returns the local profile's user ID, or an error when no
// profile has been created yet.
func (a *Engine) CurrentUserID() (string, error) {
	a.mu.RLock()
	id := a.userID
	a.mu.RUnlock()
	if id == "" {
		return "", errors.New("no profile has been created on this device")
	}
	return id, nil
}

// parseID centralizes UUID conversion so a malformed ID is always reported as a
// distinct, attributable error rather than being mixed in with engine errors.
func parseID(field, s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid %s %q: %w", field, s, err)
	}
	return id, nil
}

// ---------------------------------------------------------------------------
// Profile and setup
// ---------------------------------------------------------------------------

func (a *Engine) SetProfile(profileName string, image []byte, deviceName string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	if err := e.SetProfile(profileName, image, deviceName); err != nil {
		return err
	}
	// Safe now: SetProfile succeeded, so the profile row exists.
	a.setUserID(e.CurrentUserID().String())
	return nil
}

func (a *Engine) UpdateProfileName(newName string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	return e.UpdateProfileName(newName)
}

func (a *Engine) UpdateProfileImage(newImage []byte) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	return e.UpdateProfileImage(newImage)
}

func (a *Engine) GetNewAddUserString() (string, error) {
	e, err := a.engine()
	if err != nil {
		return "", err
	}
	return e.GetNewAddUserString(), nil
}

func (a *Engine) RequestToAddUser(offer string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	return e.RequestToAddUser(offer)
}

// ---------------------------------------------------------------------------
// Devices and sync
// ---------------------------------------------------------------------------

func (a *Engine) GetNewSyncString() (string, error) {
	e, err := a.engine()
	if err != nil {
		return "", err
	}
	return e.GetNewSyncString(), nil
}

func (a *Engine) RequestToSync(data string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	return e.RequestToSync(data)
}

func (a *Engine) RequestToManageEncryptedDevice(data string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	return e.RequestToManageEncryptedDevice(data)
}

func (a *Engine) RenameDevice(deviceID string, name string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("deviceID", deviceID)
	if err != nil {
		return err
	}
	return e.RenameDevice(id, name)
}

func (a *Engine) RevokeDevice(deviceID string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("deviceID", deviceID)
	if err != nil {
		return err
	}
	return e.RevokeDevice(id)
}

func (a *Engine) CurrentDeviceActive() {
	if e, err := a.engine(); err == nil {
		e.CurrentDeviceActive()
	}
}

// ---------------------------------------------------------------------------
// Users
// ---------------------------------------------------------------------------

func (a *Engine) AliasUser(userID string, alias string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("userID", userID)
	if err != nil {
		return err
	}
	return e.AliasUser(id, alias)
}

func (a *Engine) SetUserNotes(userID string, notes string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("userID", userID)
	if err != nil {
		return err
	}
	return e.SetUserNotes(id, notes)
}

func (a *Engine) BlockUser(userID string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("userID", userID)
	if err != nil {
		return err
	}
	return e.BlockUser(id)
}

func (a *Engine) UnblockUser(userID string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("userID", userID)
	if err != nil {
		return err
	}
	return e.UnblockUser(id)
}

func (a *Engine) UserConnectionDesired(userID string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("userID", userID)
	if err != nil {
		return err
	}
	e.UserConnectionDesired(id)
	return nil
}

func (a *Engine) GroupConnectionDesired(groupID string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("groupID", groupID)
	if err != nil {
		return err
	}
	e.GroupConnectionDesired(id)
	return nil
}

// ---------------------------------------------------------------------------
// Direct message threads
// ---------------------------------------------------------------------------

func (a *Engine) SetOpenDM(userID string, open bool) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("userID", userID)
	if err != nil {
		return err
	}
	return e.SetOpenDM(id, open)
}

func (a *Engine) SetDMRetention(userID string, retention int64) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("userID", userID)
	if err != nil {
		return err
	}
	return e.SetDMRetention(id, retention)
}

func (a *Engine) SetDMMutedUntil(userID string, mutedUntil int64) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("userID", userID)
	if err != nil {
		return err
	}
	return e.SetDMMutedUntil(id, mutedUntil)
}

func (a *Engine) SetDMReadReceiptSettings(userID string, override bool, enabled bool) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("userID", userID)
	if err != nil {
		return err
	}
	return e.SetDMReadReceiptSettings(id, override, enabled)
}

func (a *Engine) SetDMTypingIndicatorSettings(userID string, override bool, enabled bool) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("userID", userID)
	if err != nil {
		return err
	}
	return e.SetDMTypingIndicatorSettings(id, override, enabled)
}

func (a *Engine) SetDMLastOpened(userID string, timestamp int64) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("userID", userID)
	if err != nil {
		return err
	}
	e.SetDMLastOpened(id, timestamp)
	return nil
}

func (a *Engine) ClearDMChatHistory(userID string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("userID", userID)
	if err != nil {
		return err
	}
	return e.ClearDMChatHistory(id)
}

func (a *Engine) MarkAllDirectMessagesAsRead(userID string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("userID", userID)
	if err != nil {
		return err
	}
	e.MarkAllDirectMessagesAsRead(id)
	return nil
}

func (a *Engine) TypingInDirectMessage(userID string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("userID", userID)
	if err != nil {
		return err
	}
	e.TypingInDirectMessage(id)
	return nil
}

// ---------------------------------------------------------------------------
// Groups
// ---------------------------------------------------------------------------

// CreateGroup takes chat.NewGroup as JSON with the avatar supplied separately,
// so image bytes never pass through a JSON string.
func (a *Engine) CreateGroup(newGroupJSON string, image []byte) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	var ng chat.NewGroup
	if err := json.Unmarshal([]byte(newGroupJSON), &ng); err != nil {
		return fmt.Errorf("createGroup: bad NewGroup JSON: %w", err)
	}
	ng.Image = image
	return e.CreateGroup(ng)
}

func (a *Engine) RenameGroup(groupID string, newName string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("groupID", groupID)
	if err != nil {
		return err
	}
	return e.RenameGroup(id, newName)
}

func (a *Engine) SetGroupImage(groupID string, image []byte) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("groupID", groupID)
	if err != nil {
		return err
	}
	return e.SetGroupImage(id, image)
}

func (a *Engine) DeleteGroup(groupID string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("groupID", groupID)
	if err != nil {
		return err
	}
	return e.DeleteGroup(id)
}

func (a *Engine) BlockGroup(groupID string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("groupID", groupID)
	if err != nil {
		return err
	}
	return e.BlockGroup(id)
}

func (a *Engine) AcceptInvite(groupID string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("groupID", groupID)
	if err != nil {
		return err
	}
	return e.AcceptInvite(id)
}

func (a *Engine) RejectInvite(groupID string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("groupID", groupID)
	if err != nil {
		return err
	}
	return e.RejectInvite(id)
}

func (a *Engine) InviteUserToGroup(groupID string, userID string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	gid, err := parseID("groupID", groupID)
	if err != nil {
		return err
	}
	uid, err := parseID("userID", userID)
	if err != nil {
		return err
	}
	return e.InviteUserToGroup(gid, uid)
}

func (a *Engine) RevokeInvite(groupID string, userID string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	gid, err := parseID("groupID", groupID)
	if err != nil {
		return err
	}
	uid, err := parseID("userID", userID)
	if err != nil {
		return err
	}
	return e.RevokeInvite(gid, uid)
}

func (a *Engine) RemoveUserFromGroup(groupID string, userID string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	gid, err := parseID("groupID", groupID)
	if err != nil {
		return err
	}
	uid, err := parseID("userID", userID)
	if err != nil {
		return err
	}
	return e.RemoveUserFromGroup(gid, uid)
}

func (a *Engine) PromoteGroupAdmin(groupID string, userID string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	gid, err := parseID("groupID", groupID)
	if err != nil {
		return err
	}
	uid, err := parseID("userID", userID)
	if err != nil {
		return err
	}
	return e.PromoteGroupAdmin(gid, uid)
}

func (a *Engine) DemoteGroupAdmin(groupID string, userID string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	gid, err := parseID("groupID", groupID)
	if err != nil {
		return err
	}
	uid, err := parseID("userID", userID)
	if err != nil {
		return err
	}
	return e.DemoteGroupAdmin(gid, uid)
}

func (a *Engine) RestrictUserManagement(groupID string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("groupID", groupID)
	if err != nil {
		return err
	}
	return e.RestrictUserManagement(id)
}

func (a *Engine) UnrestrictUserManagement(groupID string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("groupID", groupID)
	if err != nil {
		return err
	}
	return e.UnrestrictUserManagement(id)
}

func (a *Engine) RestrictGroupEdits(groupID string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("groupID", groupID)
	if err != nil {
		return err
	}
	return e.RestrictGroupEdits(id)
}

func (a *Engine) UnrestrictGroupEdits(groupID string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("groupID", groupID)
	if err != nil {
		return err
	}
	return e.UnrestrictGroupEdits(id)
}

func (a *Engine) RestrictPosting(groupID string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("groupID", groupID)
	if err != nil {
		return err
	}
	return e.RestrictPosting(id)
}

func (a *Engine) UnrestrictPosting(groupID string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("groupID", groupID)
	if err != nil {
		return err
	}
	return e.UnrestrictPosting(id)
}

func (a *Engine) SetGroupRetention(groupID string, retention int64) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("groupID", groupID)
	if err != nil {
		return err
	}
	return e.SetGroupRetention(id, retention)
}

func (a *Engine) SetGroupMutedUntil(groupID string, mutedUntil int64) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("groupID", groupID)
	if err != nil {
		return err
	}
	return e.SetGroupMutedUntil(id, mutedUntil)
}

func (a *Engine) SetGroupReadReceiptSettings(groupID string, override bool, enabled bool) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("groupID", groupID)
	if err != nil {
		return err
	}
	return e.SetGroupReadReceiptSettings(id, override, enabled)
}

func (a *Engine) SetGroupTypingIndicatorSettings(groupID string, override bool, enabled bool) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("groupID", groupID)
	if err != nil {
		return err
	}
	return e.SetGroupTypingIndicatorSettings(id, override, enabled)
}

func (a *Engine) SetGroupLastOpened(groupID string, timestamp int64) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("groupID", groupID)
	if err != nil {
		return err
	}
	e.SetGroupLastOpened(id, timestamp)
	return nil
}

func (a *Engine) ClearGroupChatHistory(groupID string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("groupID", groupID)
	if err != nil {
		return err
	}
	return e.ClearGroupChatHistory(id)
}

func (a *Engine) MarkAllGroupMessagesAsRead(groupID string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("groupID", groupID)
	if err != nil {
		return err
	}
	e.MarkAllGroupMessagesAsRead(id)
	return nil
}

func (a *Engine) TypingInGroup(groupID string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("groupID", groupID)
	if err != nil {
		return err
	}
	e.TypingInGroup(id)
	return nil
}

// ---------------------------------------------------------------------------
// Reading state
// ---------------------------------------------------------------------------

// GetInitialState returns the entire UI state as JSON.
//
// BLOCKING: reads the whole database and marshals it. On a heavy account this
// can be megabytes; call it once at startup, off the main thread.
func (a *Engine) GetInitialState() (string, error) {
	e, err := a.engine()
	if err != nil {
		return "", err
	}
	st := e.GetInitialState()
	if st.Profile != nil {
		a.setUserID(st.Profile.ID.String())
	}
	b, err := json.Marshal(&st)
	if err != nil {
		return "", fmt.Errorf("marshalling initial state: %w", err)
	}
	return string(b), nil
}

// GetDMHistory returns the message history for one DM thread as JSON, in the
// same InitialState shape.
func (a *Engine) GetDMHistory(userID string) (string, error) {
	e, err := a.engine()
	if err != nil {
		return "", err
	}
	id, err := parseID("userID", userID)
	if err != nil {
		return "", err
	}
	st := e.GetDMHistory(id)
	b, err := json.Marshal(&st)
	if err != nil {
		return "", fmt.Errorf("marshalling dm history: %w", err)
	}
	return string(b), nil
}

func (a *Engine) NetworkOnline() bool {
	e, err := a.engine()
	if err != nil {
		return false
	}
	return e.NetworkOnline()
}

// ---------------------------------------------------------------------------
// Read state, drafts, presence hints
// ---------------------------------------------------------------------------

// MarkAsRead marks one thread item read. frameType is one of the Type*
// constants in this package.
func (a *Engine) MarkAsRead(id string, frameType string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	fid, err := parseID("id", id)
	if err != nil {
		return err
	}
	e.MarkAsRead(fid, frameType)
	return nil
}

func (a *Engine) SetActiveThread(threadID string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	// uuid.Nil is meaningful here: it means "no thread is open".
	if threadID == "" {
		e.SetActiveThread(uuid.Nil)
		return nil
	}
	id, err := parseID("threadID", threadID)
	if err != nil {
		return err
	}
	e.SetActiveThread(id)
	return nil
}

func (a *Engine) SetScrolledDown(threadID string, value bool) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("threadID", threadID)
	if err != nil {
		return err
	}
	e.SetScrolledDown(id, value)
	return nil
}

func (a *Engine) UpdateDraft(threadID string, text string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("threadID", threadID)
	if err != nil {
		return err
	}
	e.UpdateDraft(id, text)
	return nil
}

func (a *Engine) SetForeground(foreground bool) {
	if e, err := a.engine(); err == nil {
		e.SetForeground(foreground)
	}
}

// SetNotificationIcon caches the avatar the engine should attach to
// notifications for a thread. thread is a UUID string.
func (a *Engine) SetNotificationIcon(thread string, icon []byte) {
	if e, err := a.engine(); err == nil {
		e.SetNotificationIcon(thread, icon)
	}
}

// ---------------------------------------------------------------------------
// Global settings
// ---------------------------------------------------------------------------

// SetAutoJoinGroups takes int32 rather than int: gobind maps Go int to Java
// long, which is a misleading signature for a small enum-like setting.
func (a *Engine) SetAutoJoinGroups(value int32) {
	if e, err := a.engine(); err == nil {
		e.SetAutoJoinGroups(int(value))
	}
}

func (a *Engine) SetNewDMRetention(value int64) {
	if e, err := a.engine(); err == nil {
		e.SetNewDMRetention(value)
	}
}

func (a *Engine) SetNewGroupRetention(value int64) {
	if e, err := a.engine(); err == nil {
		e.SetNewGroupRetention(value)
	}
}

func (a *Engine) SetNewGroupRestrictUserManagement(value bool) {
	if e, err := a.engine(); err == nil {
		e.SetNewGroupRestrictUserManagement(value)
	}
}

func (a *Engine) SetNewGroupRestrictGroupEdits(value bool) {
	if e, err := a.engine(); err == nil {
		e.SetNewGroupRestrictGroupEdits(value)
	}
}

func (a *Engine) SetNewGroupRestrictPosting(value bool) {
	if e, err := a.engine(); err == nil {
		e.SetNewGroupRestrictPosting(value)
	}
}

func (a *Engine) SetReadReceiptsByDefault(value bool) {
	if e, err := a.engine(); err == nil {
		e.SetReadReceiptsByDefault(value)
	}
}

func (a *Engine) SetTypingIndicatorsByDefault(value bool) {
	if e, err := a.engine(); err == nil {
		e.SetTypingIndicatorsByDefault(value)
	}
}

// ---------------------------------------------------------------------------
// Files
// ---------------------------------------------------------------------------

// BlobPath returns the on-disk path of a completed blob so Kotlin can decode it
// directly. Cheap and main-thread safe: pure string concatenation, no engine
// call. The old build did the same thing implicitly - eval.go's GetFileData arm
// was empty, with the comment "the activity can read files without IPC".
func (a *Engine) BlobPath(fileID string) string {
	return filepath.Join(config.GetConfigDirectory(), "blobs", fileID)
}

// GetFileData is the fallback for callers that genuinely need the bytes in
// memory. Prefer BlobPath. BLOCKING: reads from disk.
func (a *Engine) GetFileData(fileID string) ([]byte, error) {
	e, err := a.engine()
	if err != nil {
		return nil, err
	}
	id, err := parseID("fileID", fileID)
	if err != nil {
		return nil, err
	}
	return e.GetFileData(id)
}

func (a *Engine) FileDownloaded(fileID string) (bool, error) {
	e, err := a.engine()
	if err != nil {
		return false, err
	}
	id, err := parseID("fileID", fileID)
	if err != nil {
		return false, err
	}
	return e.FileDownloaded(id), nil
}

func (a *Engine) FileEmbedded(fileID string) (bool, error) {
	e, err := a.engine()
	if err != nil {
		return false, err
	}
	id, err := parseID("fileID", fileID)
	if err != nil {
		return false, err
	}
	return e.FileEmbedded(id), nil
}

func (a *Engine) FileWanted(fileID string) (bool, error) {
	e, err := a.engine()
	if err != nil {
		return false, err
	}
	id, err := parseID("fileID", fileID)
	if err != nil {
		return false, err
	}
	return e.FileWanted(id), nil
}

func (a *Engine) DownloadFileToDisk(fileID string, destination string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("fileID", fileID)
	if err != nil {
		return err
	}
	e.DownloadFileToDisk(id, destination)
	return nil
}

func (a *Engine) CancelDownload(fileID string) error {
	e, err := a.engine()
	if err != nil {
		return err
	}
	id, err := parseID("fileID", fileID)
	if err != nil {
		return err
	}
	e.CancelDownload(id)
	return nil
}
