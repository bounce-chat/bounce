package goservice

import (
	"encoding/base64"
	"encoding/binary"
	"io"
	"os"
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
		// The activity can read files without IPC
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
	case "RejectInvite":
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
		err := b.RequestToAddUser(string(offerBytes))
		if err != nil {
			return err.Error()
		}
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

		sourcesBytes, ok := cmd[2]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}
		var sources map[uuid.UUID]string
		err = msgpack.Unmarshal(sourcesBytes, &sources)
		if err != nil {
			return err.Error()
		}

		readers := map[uuid.UUID]io.ReadCloser{}
		for id, _ := range sources {
			path := "/data/data/chat.bounce/cache/" + id.String()
			f, err := os.Open(path)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
					"path":  path,
				}).Error("error opening file")
				continue
			}
			readers[id] = f
		}

		b.SendDirectMessage(dm, readers, sources)
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

		sourcesBytes, ok := cmd[2]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}
		var sources map[uuid.UUID]string
		err = msgpack.Unmarshal(sourcesBytes, &sources)
		if err != nil {
			return err.Error()
		}

		readers := map[uuid.UUID]io.ReadCloser{}
		for id, _ := range sources {
			path := "/data/data/chat.bounce/cache/" + id.String()
			f, err := os.Open(path)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
					"path":  path,
				}).Error("error opening file")
				continue
			}
			readers[id] = f
		}

		b.SendGroupMessage(gm, readers, sources)
	case "SetActiveThread":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		b.SetActiveThread(id)
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
	case "SetDMReadReceiptSettings":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		overrideBytes, ok := cmd[2]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}
		override := false
		if len(overrideBytes) == 1 && overrideBytes[0] == 0x01 {
			override = true
		}

		enabledBytes, ok := cmd[3]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}
		enabled := false
		if len(enabledBytes) == 1 && enabledBytes[0] == 0x01 {
			enabled = true
		}

		b.SetDMReadReceiptSettings(id, override, enabled)
	case "SetDMRetention":
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

		b.SetDMRetention(id, value)
	case "SetDMTypingIndicatorSettings":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		overrideBytes, ok := cmd[2]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}
		override := false
		if len(overrideBytes) == 1 && overrideBytes[0] == 0x01 {
			override = true
		}

		enabledBytes, ok := cmd[3]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}
		enabled := false
		if len(enabledBytes) == 1 && enabledBytes[0] == 0x01 {
			enabled = true
		}

		b.SetDMTypingIndicatorSettings(id, override, enabled)
	case "SetForeground":
		valueBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}
		value := false
		if len(valueBytes) == 1 && valueBytes[0] == 0x01 {
			value = true
		}

		b.SetForeground(value)
	case "SetGroupImage":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		imageIDBytes, ok := cmd[2]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		imageID, err := uuid.FromBytes(imageIDBytes)
		if err != nil {
			return err.Error()
		}
		path := "/data/data/chat.bounce/cache/" + imageID.String()
		defer os.Remove(path)
		image, err := os.ReadFile(path)
		if err != nil {
			log.WithFields(log.Fields{
				"path":  path,
				"error": err.Error(),
			}).Error("error reading temp file")
			return err.Error()
		}

		b.SetGroupImage(id, image)
	case "SetGroupLastOpened":
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

		b.SetGroupLastOpened(id, value)
	case "SetGroupMutedUntil":
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

		err = b.SetGroupMutedUntil(id, value)
		if err != nil {
			return err.Error()
		}
	case "SetGroupReadReceiptSettings":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		overrideBytes, ok := cmd[2]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}
		override := false
		if len(overrideBytes) == 1 && overrideBytes[0] == 0x01 {
			override = true
		}

		enabledBytes, ok := cmd[3]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}
		enabled := false
		if len(enabledBytes) == 1 && enabledBytes[0] == 0x01 {
			enabled = true
		}

		b.SetGroupReadReceiptSettings(id, override, enabled)
	case "SetGroupRetention":
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

		b.SetGroupRetention(id, value)
	case "SetGroupTypingIndicatorSettings":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		overrideBytes, ok := cmd[2]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}
		override := false
		if len(overrideBytes) == 1 && overrideBytes[0] == 0x01 {
			override = true
		}

		enabledBytes, ok := cmd[3]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}
		enabled := false
		if len(enabledBytes) == 1 && enabledBytes[0] == 0x01 {
			enabled = true
		}

		b.SetGroupTypingIndicatorSettings(id, override, enabled)
	case "SetNewDMRetention":
		valueBytes, ok := cmd[1]
		if !ok || len(valueBytes) != 8 {
			log.Error("bad ts bytes")
			return "malformed command"
		}
		value := int64(binary.BigEndian.Uint64(valueBytes))

		b.SetNewDMRetention(value)
	case "SetNewGroupRestrictGroupEdits":
		valueBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}
		value := false
		if len(valueBytes) == 1 && valueBytes[0] == 0x01 {
			value = true
		}
		b.SetNewGroupRestrictGroupEdits(value)
	case "SetNewGroupRestrictPosting":
		valueBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}
		value := false
		if len(valueBytes) == 1 && valueBytes[0] == 0x01 {
			value = true
		}
		b.SetNewGroupRestrictPosting(value)
	case "SetNewGroupRestrictUserManagement":
		valueBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}
		value := false
		if len(valueBytes) == 1 && valueBytes[0] == 0x01 {
			value = true
		}
		b.SetNewGroupRestrictUserManagement(value)
	case "SetNewGroupRetention":
		valueBytes, ok := cmd[1]
		if !ok || len(valueBytes) != 8 {
			log.Error("bad ts bytes")
			return "malformed command"
		}
		value := int64(binary.BigEndian.Uint64(valueBytes))

		b.SetNewGroupRetention(value)
	case "SetOpenDM":
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
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}
		value := false
		if len(valueBytes) == 1 && valueBytes[0] == 0x01 {
			value = true
		}

		err = b.SetOpenDM(id, value)
		if err != nil {
			return err.Error()
		}
	case "SetProfile":
		name, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}
		imageIDBytes, ok := cmd[2]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		imageID, err := uuid.FromBytes(imageIDBytes)
		if err != nil {
			return err.Error()
		}
		path := "/data/data/chat.bounce/cache/" + imageID.String()
		defer os.Remove(path)
		image, err := os.ReadFile(path)
		if err != nil {
			log.WithFields(log.Fields{
				"path":  path,
				"error": err.Error(),
			}).Error("error reading temp file")
			return err.Error()
		}

		deviceName, ok := cmd[3]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		err = b.SetProfile(string(name), image, string(deviceName))
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Warn("error setting profile")
			return err.Error()
		}
	case "SetReadReceiptsByDefault":
		valueBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}
		value := false
		if len(valueBytes) == 1 && valueBytes[0] == 0x01 {
			value = true
		}
		b.SetReadReceiptsByDefault(value)
	case "SetScrolledDown":
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
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}
		value := false
		if len(valueBytes) == 1 && valueBytes[0] == 0x01 {
			value = true
		}

		b.SetScrolledDown(id, value)
	case "SetTypingIndicatorsByDefault":
		valueBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}
		value := false
		if len(valueBytes) == 1 && valueBytes[0] == 0x01 {
			value = true
		}
		b.SetTypingIndicatorsByDefault(value)
	case "SetUserNotes":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		notesBytes, ok := cmd[2]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		b.SetUserNotes(id, string(notesBytes))
	case "TypingInDirectMessage":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		b.TypingInDirectMessage(id)
	case "TypingInGroup":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		b.TypingInGroup(id)
	case "UnblockUser":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		b.UnblockUser(id)
	case "UnrestrictGroupEdits":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		b.UnrestrictGroupEdits(id)
	case "UnrestrictPosting":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		b.UnrestrictPosting(id)
	case "UnrestrictUserManagement":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		b.UnrestrictUserManagement(id)
	case "UpdateDraft":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		draftBytes, ok := cmd[2]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		b.UpdateDraft(id, string(draftBytes))
	case "UpdateProfileImage":
		imageIDBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		imageID, err := uuid.FromBytes(imageIDBytes)
		if err != nil {
			return err.Error()
		}
		path := "/data/data/chat.bounce/cache/" + imageID.String()
		defer os.Remove(path)
		image, err := os.ReadFile(path)
		if err != nil {
			log.WithFields(log.Fields{
				"path":  path,
				"error": err.Error(),
			}).Error("error reading temp file")
			return err.Error()
		}

		err = b.UpdateProfileImage(image)
		if err != nil {
			return err.Error()
		}
	case "UpdateProfileName":
		nameBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		err := b.UpdateProfileName(string(nameBytes))
		if err != nil {
			return err.Error()
		}
	case "UserConnectionDesired":
		idBytes, ok := cmd[1]
		if !ok {
			log.Error("no bytes")
			return "malformed command"
		}

		id, err := uuid.FromBytes(idBytes)
		if err != nil {
			return err.Error()
		}

		b.UserConnectionDesired(id)
	}
	return ""
}
