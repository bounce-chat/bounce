package goservice

import (
	"encoding/base64"
	"encoding/binary"
	"io"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
	log "github.com/sirupsen/logrus"
)

func Eval(rawTask string) string {
	for b == nil {
		log.Warn("waiting for bounce object to be defined before calling")
		time.Sleep(500 * time.Millisecond)
	}

	task, err := base64.StdEncoding.DecodeString(rawTask)
	if err != nil {
		return ""
	}
	cmd := make(map[int][]byte)
	err = msgpack.Unmarshal([]byte(task), &cmd)
	if err != nil {
		log.Error("could not unmarshal cmd in eval")
		return ""
	}

	if len(cmd) == 0 {
		log.Error("cmd is empty")
		return ""
	}

	funcName, ok := cmd[0]
	if !ok {
		log.Error("cmd is has no function")
		return ""
	}

	switch string(funcName) {
	case "GetInitialState":
		initialState := b.GetInitialState()
		data, err := msgpack.Marshal(&initialState)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error marshalling initial state")
			return ""
		}
		return base64.StdEncoding.EncodeToString(data)
	case "AcceptInvite":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		b.AcceptInvite(id)
	case "AliasUser":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		aliasBytes, ok := cmd[2]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		b.AliasUser(id, string(aliasBytes))
	case "BlockGroup":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		b.BlockGroup(id)
	case "BlockUser":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		b.BlockUser(id)
	case "CancelDownload":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		b.CancelDownload(id)
	case "ClearDMChatHistory":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		b.ClearDMChatHistory(id)
	case "ClearGroupChatHistory":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		b.ClearGroupChatHistory(id)
	case "CreateGroup":
		groupBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		ng := chat.NewGroup{}
		err = msgpack.Unmarshal(groupBytes, &ng)
		if err != nil {
			return err.Error()
		}

		b.CreateGroup(ng)
	case "CurrentDeviceActive":
		b.CurrentDeviceActive()
	case "DeleteGroup":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		b.DeleteGroup(id)
	case "DemoteGroupAdmin":
		groupIDBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		groupID, err := uuid.FromBytes(groupIDBytes)
		if err != nil {
			return err.Error()
		}

		userIDBytes, ok := cmd[2]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		userID, err := uuid.FromBytes(userIDBytes)
		if err != nil {
			return err.Error()
		}

		b.DemoteGroupAdmin(groupID, userID)
	case "DownloadFileToDisk":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		pathBytes, ok := cmd[2]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		b.DownloadFileToDisk(id, string(pathBytes))
	case "FileDownloaded":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		res := b.FileDownloaded(id)
		if res {
			return "true"
		} else {
			return "false"
		}
	case "FileEmbedded":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		res := b.FileEmbedded(id)
		if res {
			return "true"
		} else {
			return "false"
		}
	case "FileWanted":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		res := b.FileWanted(id)
		if res {
			return "true"
		} else {
			return "false"
		}
	case "GetDMHistory":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		initialState := b.GetDMHistory(id)
		data, err := msgpack.Marshal(&initialState)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error marshalling initial state")
			return ""
		}
		return base64.StdEncoding.EncodeToString(data)
	case "GetFileData":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		bytes, _ := b.GetFileData(id)
		return base64.StdEncoding.EncodeToString(bytes)
	case "GetNewAddUserString":
		return b.GetNewAddUserString()
	case "GetNewSyncString":
		return b.GetNewSyncString()
	case "GroupConnectionDesired":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		b.GroupConnectionDesired(id)
	case "InviteUserToGroup":
		groupIDBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		groupID, err := uuid.FromBytes(groupIDBytes)
		if err != nil {
			return err.Error()
		}

		userIDBytes, ok := cmd[2]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		userID, err := uuid.FromBytes(userIDBytes)
		if err != nil {
			return err.Error()
		}

		err = b.InviteUserToGroup(groupID, userID)
		if err != nil {
			return err.Error()
		}
	case "MarkAllDirectMessagesAsRead":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		b.MarkAllDirectMessagesAsRead(id)
	case "MarkAllGroupMessagesAsRead":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		b.MarkAllGroupMessagesAsRead(id)
	case "MarkAsRead":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		typeBytes, ok := cmd[2]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		b.MarkAsRead(id, string(typeBytes))
	case "NetworkOnline":
		if b.NetworkOnline() {
			return "true"
		} else {
			return "false"
		}
	case "PromoteGroupAdmin":
		groupIDBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		groupID, err := uuid.FromBytes(groupIDBytes)
		if err != nil {
			return err.Error()
		}

		userIDBytes, ok := cmd[2]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		userID, err := uuid.FromBytes(userIDBytes)
		if err != nil {
			return err.Error()
		}

		err = b.PromoteGroupAdmin(groupID, userID)
		if err != nil {
			return err.Error()
		}
	case "Reject Invite":
		groupIDBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		groupID, err := uuid.FromBytes(groupIDBytes)
		if err != nil {
			return err.Error()
		}

		err = b.RejectInvite(groupID)
		if err != nil {
			return err.Error()
		}
	case "RenameDevice":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		nameBytes, ok := cmd[2]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		b.RenameDevice(id, string(nameBytes))
	case "RenameGroup":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		nameBytes, ok := cmd[2]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		b.RenameGroup(id, string(nameBytes))
	case "RequestToAddUser":
		offerBytes, ok := cmd[1]
		if !ok {
			log.Error("no offer bytes")
			return ""
		}
		b.RequestToAddUser(string(offerBytes))
	case "RemoveUserFromGroup":
		groupIDBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		groupID, err := uuid.FromBytes(groupIDBytes)
		if err != nil {
			return err.Error()
		}

		userIDBytes, ok := cmd[2]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		userID, err := uuid.FromBytes(userIDBytes)
		if err != nil {
			return err.Error()
		}

		err = b.RemoveUserFromGroup(groupID, userID)
		if err != nil {
			return err.Error()
		}
	case "RequestToManageEncryptedDevice":
		strBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}
		err := b.RequestToManageEncryptedDevice(string(strBytes))
		if err != nil {
			return err.Error()
		}
	case "RequestToSync":
		strBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}
		err := b.RequestToSync(string(strBytes))
		if err != nil {
			return err.Error()
		}
	case "RestrictGroupEdits":
		groupIDBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		groupID, err := uuid.FromBytes(groupIDBytes)
		if err != nil {
			return err.Error()
		}

		err = b.RestrictGroupEdits(groupID)
		if err != nil {
			return err.Error()
		}
	case "RestrictPosting":
		groupIDBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		groupID, err := uuid.FromBytes(groupIDBytes)
		if err != nil {
			return err.Error()
		}

		err = b.RestrictPosting(groupID)
		if err != nil {
			return err.Error()
		}
	case "RestrictUserManagement":
		groupIDBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		groupID, err := uuid.FromBytes(groupIDBytes)
		if err != nil {
			return err.Error()
		}

		err = b.RestrictUserManagement(groupID)
		if err != nil {
			return err.Error()
		}
	case "RevokeDevice":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		err = b.RevokeDevice(id)
		if err != nil {
			return err.Error()
		}
	case "RevokeInvite":
		groupIDBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		groupID, err := uuid.FromBytes(groupIDBytes)
		if err != nil {
			return err.Error()
		}

		userIDBytes, ok := cmd[2]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		userID, err := uuid.FromBytes(userIDBytes)
		if err != nil {
			return err.Error()
		}

		err = b.RevokeInvite(groupID, userID)
		if err != nil {
			return err.Error()
		}
	case "SendDirectMessage":
		dmBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		dm := chat.DirectMessage{}
		err = msgpack.Unmarshal(dmBytes, &dm)
		if err != nil {
			return err.Error()
		}

		// TODO: handle files
		readers := map[uuid.UUID]io.ReadCloser{}
		names := map[uuid.UUID]string{}
		b.SendDirectMessage(dm, readers, names)
	case "SendGroupMessage":
		gmBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		gm := chat.GroupMessage{}
		err = msgpack.Unmarshal(gmBytes, &gm)
		if err != nil {
			return err.Error()
		}

		// TODO: handle files
		readers := map[uuid.UUID]io.ReadCloser{}
		names := map[uuid.UUID]string{}
		b.SendGroupMessage(gm, readers, names)
	case "SetAutoJoinGroups":
		valueBytes, ok := cmd[1]
		if !ok || len(valueBytes) != 8 {
			log.Error("bad ts bytes")
			return "malformed command"
		}
		value := int(binary.BigEndian.Uint64(valueBytes))

		b.SetAutoJoinGroups(value)
	case "SetDMLastOpened":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		valueBytes, ok := cmd[2]
		if !ok || len(valueBytes) != 8 {
			log.Error("bad ts bytes")
			return "malformed command"
		}
		value := int64(binary.BigEndian.Uint64(valueBytes))

		b.SetDMLastOpened(id, value)
	case "SetDMMutedUntil":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		valueBytes, ok := cmd[2]
		if !ok || len(valueBytes) != 8 {
			log.Error("bad ts bytes")
			return "malformed command"
		}
		value := int64(binary.BigEndian.Uint64(valueBytes))

		err = b.SetDMMutedUntil(id, value)
		if err != nil {
			return err.Error()
		}
	case "SetProfile":
		name, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}
		image, ok := cmd[2]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}
		deviceName, ok := cmd[3]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		err := b.SetProfile(string(name), image, string(deviceName))
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Warn("error setting profile")
			return err.Error()
		}
	}
	return ""
}
