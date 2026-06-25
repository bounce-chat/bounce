package main

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"

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
	encodedData, err := callCommand(map[int][]byte{
		0: []byte("GetFileData"),
		1: fileID[:],
	})
	if err != nil {
		return []byte{}, err
	}
	return base64.StdEncoding.DecodeString(encodedData)
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

func (b *aidlBounce) SendDirectMessage(dm chat.DirectMessage, _ map[uuid.UUID]io.ReadCloser, _ map[uuid.UUID]string) {
	dmBytes, err := msgpack.Marshal(&dm)
	if err != nil {
		return
	}

	callCommand(map[int][]byte{
		0: []byte("SendDirectMessage"),
		1: dmBytes,
	})
	// TODO: handling files

}
func (b *aidlBounce) SendGroupMessage(gm chat.GroupMessage, _ map[uuid.UUID]io.ReadCloser, _ map[uuid.UUID]string) {
	gmBytes, err := msgpack.Marshal(&gm)
	if err != nil {
		return
	}

	callCommand(map[int][]byte{
		0: []byte("SendGroupMessage"),
		1: gmBytes,
	})
	// TODO: handling files
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
		0: []byte("RevokeInvite"),
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
	errStr, err := callCommand(map[int][]byte{
		0: []byte("SetProfile"),
		1: []byte(profileName),
		2: image,
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
