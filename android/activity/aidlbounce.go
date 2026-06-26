package main

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"io/ioutil"
	"os"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
	log "github.com/sirupsen/logrus"
)

// A shim that takes all calls and serializes them to the chat engine over AIDL
type aidlBounce struct{}

func (b *aidlBounce) GetInitialState() chat.InitialState {
	encodedData, err := callCommand(map[int][]byte{
		0: []byte("GetInitialState"),
	})
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error calling getInitialState")
	}
	is := chat.InitialState{}
	data, err := base64.StdEncoding.DecodeString(encodedData)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error decoding initial state")
	}
	err = msgpack.Unmarshal([]byte(data), &is)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling initial state")
	}
	return is
}

func (b *aidlBounce) AcceptInvite(groupID uuid.UUID) error {
	errStr, err := callCommand(map[int][]byte{
		0: []byte("AcceptInvite"),
		1: groupID[:],
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) AliasUser(userID uuid.UUID, alias string) error {
	errStr, err := callCommand(map[int][]byte{
		0: []byte("AliasUser"),
		1: userID[:],
		2: []byte(alias),
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) BlockGroup(groupID uuid.UUID) error {
	errStr, err := callCommand(map[int][]byte{
		0: []byte("BlockGroup"),
		1: groupID[:],
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) BlockUser(userID uuid.UUID) error {
	errStr, err := callCommand(map[int][]byte{
		0: []byte("BlockUser"),
		1: userID[:],
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) CancelDownload(fileID uuid.UUID) {
	callCommand(map[int][]byte{
		0: []byte("CancelDownload"),
		1: fileID[:],
	})
}

func (b *aidlBounce) ClearDMChatHistory(userID uuid.UUID) error {
	errStr, err := callCommand(map[int][]byte{
		0: []byte("ClearDMChatHistory"),
		1: userID[:],
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) ClearGroupChatHistory(groupID uuid.UUID) error {
	errStr, err := callCommand(map[int][]byte{
		0: []byte("ClearGroupChatHistory"),
		1: groupID[:],
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) CreateGroup(ng chat.NewGroup) error {
	groupBytes, err := msgpack.Marshal(&ng)
	if err != nil {
		return err
	}

	errStr, err := callCommand(map[int][]byte{
		0: []byte("CreateGroup"),
		1: groupBytes,
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) CurrentDeviceActive() {
	callCommand(map[int][]byte{
		0: []byte("CurrentDeviceActive"),
	})
}

func (b *aidlBounce) DeleteGroup(groupID uuid.UUID) error {
	errStr, err := callCommand(map[int][]byte{
		0: []byte("DeleteGroup"),
		1: groupID[:],
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) DemoteGroupAdmin(groupID uuid.UUID, userID uuid.UUID) error {
	errStr, err := callCommand(map[int][]byte{
		0: []byte("DemoteGroupAdmin"),
		1: groupID[:],
		2: userID[:],
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) DownloadFileToDisk(fileID uuid.UUID, destination string) {
	callCommand(map[int][]byte{
		0: []byte("DownloadFileToDisk"),
		1: fileID[:],
		2: []byte(destination),
	})
}

func (b *aidlBounce) FileDownloaded(fileID uuid.UUID) bool {
	resp, _ := callCommand(map[int][]byte{
		0: []byte("FileDownloaded"),
		1: fileID[:],
	})
	return resp == "true"
}

func (b *aidlBounce) FileEmbedded(fileID uuid.UUID) bool {
	resp, _ := callCommand(map[int][]byte{
		0: []byte("FileEmbedded"),
		1: fileID[:],
	})
	return resp == "true"
}

func (b *aidlBounce) FileWanted(fileID uuid.UUID) bool {
	resp, _ := callCommand(map[int][]byte{
		0: []byte("FileWanted"),
		1: fileID[:],
	})
	return resp == "true"
}

func (b *aidlBounce) GetDMHistory(userID uuid.UUID) chat.InitialState {
	encodedData, err := callCommand(map[int][]byte{
		0: []byte("GetDMHistory"),
	})
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error calling getInitialState")
	}
	is := chat.InitialState{}
	data, err := base64.StdEncoding.DecodeString(encodedData)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error decoding initial state")
	}
	err = msgpack.Unmarshal([]byte(data), &is)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling initial state")
	}
	return is
}

func (b *aidlBounce) GetFileData(fileID uuid.UUID) ([]byte, error) {
	if b.FileDownloaded(fileID) {
		return ioutil.ReadFile(getConfigDirectory() + "/blobs/" + fileID.String())
	}
	return []byte{}, errors.New("file not found")
}

func (b *aidlBounce) GetNewAddUserString() string {
	str, _ := callCommand(map[int][]byte{
		0: []byte("GetNewAddUserString"),
	})
	return str
}

func (b *aidlBounce) GetNewSyncString() string {
	str, _ := callCommand(map[int][]byte{
		0: []byte("GetNewSyncString"),
	})
	return str
}

func (b *aidlBounce) GroupConnectionDesired(id uuid.UUID) {
	callCommand(map[int][]byte{
		0: []byte("GroupConnectionDesired"),
		1: id[:],
	})
}

func (b *aidlBounce) InviteUserToGroup(groupID uuid.UUID, userID uuid.UUID) error {
	errStr, err := callCommand(map[int][]byte{
		0: []byte("InviteUserToGroup"),
		1: groupID[:],
		2: userID[:],
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) MarkAllDirectMessagesAsRead(id uuid.UUID) {
	callCommand(map[int][]byte{
		0: []byte("MarkAllDirectMessagesAsRead"),
		1: id[:],
	})
}

func (b *aidlBounce) MarkAllGroupMessagesAsRead(id uuid.UUID) {
	callCommand(map[int][]byte{
		0: []byte("MarkAllGroupMessagesAsRead"),
		1: id[:],
	})
}

func (b *aidlBounce) MarkAsRead(id uuid.UUID, frameType string) {
	callCommand(map[int][]byte{
		0: []byte("MarkAsRead"),
		1: id[:],
		2: []byte(frameType),
	})
}

func (b *aidlBounce) NetworkOnline() bool {
	resp, _ := callCommand(map[int][]byte{
		0: []byte("NetworkOnline"),
	})
	return resp == "true"
}

func (b *aidlBounce) PromoteGroupAdmin(groupID uuid.UUID, userID uuid.UUID) error {
	errStr, err := callCommand(map[int][]byte{
		0: []byte("PromoteGroupAdmin"),
		1: groupID[:],
		2: userID[:],
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) RejectInvite(groupID uuid.UUID) error {
	errStr, err := callCommand(map[int][]byte{
		0: []byte("RejectInvite"),
		1: groupID[:],
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) RemoveUserFromGroup(groupID uuid.UUID, userID uuid.UUID) error {
	errStr, err := callCommand(map[int][]byte{
		0: []byte("RemoveUserFromGroup"),
		1: groupID[:],
		2: userID[:],
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) RenameDevice(deviceID uuid.UUID, name string) error {
	errStr, err := callCommand(map[int][]byte{
		0: []byte("RenameDevice"),
		1: deviceID[:],
		2: []byte(name),
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) RenameGroup(groupID uuid.UUID, name string) error {
	errStr, err := callCommand(map[int][]byte{
		0: []byte("RenameGroup"),
		1: groupID[:],
		2: []byte(name),
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) RequestToAddUser(offer string) error {
	_, err := callCommand(map[int][]byte{
		0: []byte("RequestToAddUser"),
		1: []byte(offer),
	})
	return err
}

func (b *aidlBounce) RequestToManageEncryptedDevice(data string) error {
	errStr, err := callCommand(map[int][]byte{
		0: []byte("RequestToManageEncryptedDevice"),
		1: []byte(data),
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) RequestToSync(data string) error {
	errStr, err := callCommand(map[int][]byte{
		0: []byte("RequestToSync"),
		1: []byte(data),
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) RestrictGroupEdits(groupID uuid.UUID) error {
	errStr, err := callCommand(map[int][]byte{
		0: []byte("RestrictGroupEdits"),
		1: groupID[:],
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) RestrictPosting(groupID uuid.UUID) error {
	errStr, err := callCommand(map[int][]byte{
		0: []byte("RestrictPosting"),
		1: groupID[:],
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) RestrictUserManagement(groupID uuid.UUID) error {
	errStr, err := callCommand(map[int][]byte{
		0: []byte("RestrictUserManagement"),
		1: groupID[:],
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) RevokeDevice(deviceID uuid.UUID) error {
	errStr, err := callCommand(map[int][]byte{
		0: []byte("RevokeDevice"),
		1: deviceID[:],
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) RevokeInvite(groupID uuid.UUID, userID uuid.UUID) error {
	errStr, err := callCommand(map[int][]byte{
		0: []byte("RevokeInvite"),
		1: groupID[:],
		2: userID[:],
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) SendDirectMessage(dm chat.DirectMessage, readers map[uuid.UUID]io.ReadCloser, sources map[uuid.UUID]string) {
	dmBytes, err := msgpack.Marshal(&dm)
	if err != nil {
		log.Error("unable to marshal direct message")
		return
	}

	for k, v := range readers {
		path := "/data/data/chat.bounce/cache/" + k.String()
		f, err := os.Create(path)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
				"path":  path,
			}).Error("error creating file")
			return
		}
		_, err = io.Copy(f, v)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
				"path":  path,
			}).Error("error copying into file")
			return
		}
		f.Sync()
		f.Close()
	}

	sourcesBytes, err := msgpack.Marshal(&sources)
	if err != nil {
		log.Error("unable to marshal sources")
		return
	}

	callCommand(map[int][]byte{
		0: []byte("SendDirectMessage"),
		1: dmBytes,
		2: sourcesBytes,
	})

}
func (b *aidlBounce) SendGroupMessage(gm chat.GroupMessage, readers map[uuid.UUID]io.ReadCloser, sources map[uuid.UUID]string) {
	gmBytes, err := msgpack.Marshal(&gm)
	if err != nil {
		return
	}

	for k, v := range readers {
		path := "/data/data/chat.bounce/cache/" + k.String()
		f, err := os.Create(path)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
				"path":  path,
			}).Error("error creating file")
			return
		}
		_, err = io.Copy(f, v)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
				"path":  path,
			}).Error("error copying into file")
			return
		}
		defer f.Close()
	}

	sourcesBytes, err := msgpack.Marshal(&sources)
	if err != nil {
		log.Error("unable to marshal sources")
		return
	}

	callCommand(map[int][]byte{
		0: []byte("SendGroupMessage"),
		1: gmBytes,
		2: sourcesBytes,
	})
}

func (b *aidlBounce) SetAutoJoinGroups(value int) {
	bytes := make([]byte, 8)
	binary.BigEndian.PutUint64(bytes, uint64(value))

	callCommand(map[int][]byte{
		0: []byte("SetAutoJoinGroups"),
		1: bytes,
	})
}

func (b *aidlBounce) SetDMLastOpened(userID uuid.UUID, timestamp int64) {
	bytes := make([]byte, 8)
	binary.BigEndian.PutUint64(bytes, uint64(timestamp))

	callCommand(map[int][]byte{
		0: []byte("SetDMLastOpened"),
		1: bytes,
	})
}

func (b *aidlBounce) SetDMMutedUntil(userID uuid.UUID, mutedUntil int64) error {
	bytes := make([]byte, 8)
	binary.BigEndian.PutUint64(bytes, uint64(mutedUntil))

	errStr, err := callCommand(map[int][]byte{
		0: []byte("SetDMMutedUntil"),
		1: userID[:],
		2: bytes,
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) SetDMReadReceiptSettings(userID uuid.UUID, override bool, enabled bool) error {
	overrideBytes := []byte{0x00}
	if override {
		overrideBytes = []byte{0x01}
	}
	enabledBytes := []byte{0x00}
	if enabled {
		enabledBytes = []byte{0x01}
	}

	errStr, err := callCommand(map[int][]byte{
		0: []byte("SetDMReadReceiptSettings"),
		1: userID[:],
		2: overrideBytes,
		3: enabledBytes,
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}
func (b *aidlBounce) SetDMRetention(userID uuid.UUID, retention int64) error {
	bytes := make([]byte, 8)
	binary.BigEndian.PutUint64(bytes, uint64(retention))

	errStr, err := callCommand(map[int][]byte{
		0: []byte("SetDMRetention"),
		1: userID[:],
		2: bytes,
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) SetDMTypingIndicatorSettings(userID uuid.UUID, override bool, enabled bool) error {
	overrideBytes := []byte{0x00}
	if override {
		overrideBytes = []byte{0x01}
	}
	enabledBytes := []byte{0x00}
	if enabled {
		enabledBytes = []byte{0x01}
	}

	errStr, err := callCommand(map[int][]byte{
		0: []byte("SetDMTypingIndicatorSettings"),
		1: userID[:],
		2: overrideBytes,
		3: enabledBytes,
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) SetGroupImage(groupID uuid.UUID, image []byte) error {
	tempID := uuid.New()
	path := "/data/data/chat.bounce/cache/" + tempID.String()

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	_, err = f.Write(image)
	if err != nil {
		f.Close()
		return err
	}
	err = f.Sync()
	if err != nil {
		f.Close()
		return err
	}
	f.Close()

	errStr, err := callCommand(map[int][]byte{
		0: []byte("SetGroupImage"),
		1: groupID[:],
		2: tempID[:],
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) SetGroupLastOpened(groupID uuid.UUID, timestamp int64) {
	bytes := make([]byte, 8)
	binary.BigEndian.PutUint64(bytes, uint64(timestamp))

	callCommand(map[int][]byte{
		0: []byte("SetGroupLastOpened"),
		1: bytes,
	})
}

func (b *aidlBounce) SetGroupMutedUntil(groupID uuid.UUID, mutedUntil int64) error {
	bytes := make([]byte, 8)
	binary.BigEndian.PutUint64(bytes, uint64(mutedUntil))

	errStr, err := callCommand(map[int][]byte{
		0: []byte("SetGroupMutedUntil"),
		1: groupID[:],
		2: bytes,
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) SetGroupReadReceiptSettings(groupID uuid.UUID, override bool, enabled bool) error {
	overrideBytes := []byte{0x00}
	if override {
		overrideBytes = []byte{0x01}
	}
	enabledBytes := []byte{0x00}
	if enabled {
		enabledBytes = []byte{0x01}
	}

	errStr, err := callCommand(map[int][]byte{
		0: []byte("SetGroupReadReceiptSettings"),
		1: groupID[:],
		2: overrideBytes,
		3: enabledBytes,
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) SetGroupRetention(groupID uuid.UUID, retention int64) error {
	bytes := make([]byte, 8)
	binary.BigEndian.PutUint64(bytes, uint64(retention))

	errStr, err := callCommand(map[int][]byte{
		0: []byte("SetGroupRetention"),
		1: groupID[:],
		2: bytes,
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) SetGroupTypingIndicatorSettings(groupID uuid.UUID, override bool, enabled bool) error {
	overrideBytes := []byte{0x00}
	if override {
		overrideBytes = []byte{0x01}
	}
	enabledBytes := []byte{0x00}
	if enabled {
		enabledBytes = []byte{0x01}
	}

	errStr, err := callCommand(map[int][]byte{
		0: []byte("SetGroupTypingIndicatorSettings"),
		1: groupID[:],
		2: overrideBytes,
		3: enabledBytes,
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) SetNewDMRetention(value int64) {
	bytes := make([]byte, 8)
	binary.BigEndian.PutUint64(bytes, uint64(value))

	callCommand(map[int][]byte{
		0: []byte("SetNewDMRetention"),
		1: bytes,
	})
}

func (b *aidlBounce) SetNewGroupRestrictGroupEdits(value bool) {
	valueBytes := []byte{0x00}
	if value {
		valueBytes = []byte{0x01}
	}

	callCommand(map[int][]byte{
		0: []byte("SetNewGroupRestrictGroupEdits"),
		1: valueBytes,
	})
}

func (b *aidlBounce) SetNewGroupRestrictPosting(value bool) {
	valueBytes := []byte{0x00}
	if value {
		valueBytes = []byte{0x01}
	}

	callCommand(map[int][]byte{
		0: []byte("SetNewGroupRestrictPosting"),
		1: valueBytes,
	})
}

func (b *aidlBounce) SetNewGroupRestrictUserManagement(value bool) {
	valueBytes := []byte{0x00}
	if value {
		valueBytes = []byte{0x01}
	}

	callCommand(map[int][]byte{
		0: []byte("SetNewGroupRestrictUserManagement"),
		1: valueBytes,
	})
}

func (b *aidlBounce) SetNewGroupRetention(value int64) {
	bytes := make([]byte, 8)
	binary.BigEndian.PutUint64(bytes, uint64(value))

	callCommand(map[int][]byte{
		0: []byte("SetNewGroupRetention"),
		1: bytes,
	})
}

func (b *aidlBounce) SetOpenDM(userID uuid.UUID, value bool) error {
	valueBytes := []byte{0x00}
	if value {
		valueBytes = []byte{0x01}
	}

	errStr, err := callCommand(map[int][]byte{
		0: []byte("SetOpenDM"),
		1: userID[:],
		2: valueBytes,
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) SetProfile(profileName string, image []byte, deviceName string) error {
	tempID := uuid.New()
	path := "/data/data/chat.bounce/cache/" + tempID.String()
	err := os.WriteFile(path, image, 0644)
	if err != nil {
		log.WithFields(log.Fields{
			"path":  path,
			"error": err.Error(),
		}).Error("error writing temp file")
		return err
	}

	errStr, err := callCommand(map[int][]byte{
		0: []byte("SetProfile"),
		1: []byte(profileName),
		2: tempID[:],
		3: []byte(deviceName),
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) SetReadReceiptsByDefault(value bool) {
	valueBytes := []byte{0x00}
	if value {
		valueBytes = []byte{0x01}
	}

	callCommand(map[int][]byte{
		0: []byte("SetReadReceiptsByDefault"),
		1: valueBytes,
	})
}

func (b *aidlBounce) SetTypingIndicatorsByDefault(value bool) {
	valueBytes := []byte{0x00}
	if value {
		valueBytes = []byte{0x01}
	}

	callCommand(map[int][]byte{
		0: []byte("SetTypingIndicatorsByDefault"),
		1: valueBytes,
	})
}

func (b *aidlBounce) SetUserNotes(userID uuid.UUID, notes string) error {
	errStr, err := callCommand(map[int][]byte{
		0: []byte("SetUserNotes"),
		1: userID[:],
		2: []byte(notes),
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) Shutdown() {
	// Activity should not be able to shut down the service
}

func (b *aidlBounce) TypingInDirectMessage(userID uuid.UUID) {
	callCommand(map[int][]byte{
		0: []byte("TypingInDirectMessage"),
		1: userID[:],
	})
}

func (b *aidlBounce) TypingInGroup(groupID uuid.UUID) {
	callCommand(map[int][]byte{
		0: []byte("TypingInGroup"),
		1: groupID[:],
	})
}

func (b *aidlBounce) UnblockUser(userID uuid.UUID) error {
	errStr, err := callCommand(map[int][]byte{
		0: []byte("UnblockUser"),
		1: userID[:],
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) UnrestrictGroupEdits(groupID uuid.UUID) error {
	errStr, err := callCommand(map[int][]byte{
		0: []byte("UnrestrictGroupEdits"),
		1: groupID[:],
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) UnrestrictPosting(groupID uuid.UUID) error {
	errStr, err := callCommand(map[int][]byte{
		0: []byte("UnrestrictPosting"),
		1: groupID[:],
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) UnrestrictUserManagement(groupID uuid.UUID) error {
	errStr, err := callCommand(map[int][]byte{
		0: []byte("UnrestrictUserManagement"),
		1: groupID[:],
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) UpdateDraft(threadID uuid.UUID, text string) {
	callCommand(map[int][]byte{
		0: []byte("UpdateDraft"),
		1: threadID[:],
		2: []byte(text),
	})
}

func (b *aidlBounce) UpdateProfileImage(image []byte) error {
	tempID := uuid.New()
	path := "/data/data/chat.bounce/cache/" + tempID.String()

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	_, err = f.Write(image)
	if err != nil {
		f.Close()
		return err
	}
	err = f.Sync()
	if err != nil {
		f.Close()
		return err
	}
	f.Close()

	errStr, err := callCommand(map[int][]byte{
		0: []byte("UpdateProfileImage"),
		1: tempID[:],
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) UpdateProfileName(newName string) error {
	errStr, err := callCommand(map[int][]byte{
		0: []byte("UpdateProfileName"),
		1: []byte(newName),
	})
	if err != nil {
		return err
	}
	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (b *aidlBounce) UserConnectionDesired(id uuid.UUID) {
	callCommand(map[int][]byte{
		0: []byte("UserConnectionDesired"),
		1: id[:],
	})
}

func callCommand(cmd map[int][]byte) (string, error) {
	data, err := msgpack.Marshal(&cmd)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling set profile command")
	}
	res, err := callServiceFunction(string(data))
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error calling service function")
	}
	return res, err
}
