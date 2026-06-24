package main

import (
	"encoding/base64"
	"encoding/binary"
	"math"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
	log "github.com/sirupsen/logrus"
)

func pollEvents(userInterface chat.UI) {
	for range time.NewTicker(500 * time.Millisecond).C {
		dataStr, err := callStringMethod("getEvents")
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error calling getEvents")
			continue
		}

		if len(dataStr) == 0 {
			continue
		}

		data, err := base64.StdEncoding.DecodeString(dataStr)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error decoding event data")
			continue

		}
		cmds := []map[int][]byte{}
		err = msgpack.Unmarshal(data, &cmds)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error unmarshalling event data")
			continue

		}

		for _, cmd := range cmds {
			if len(cmd) == 0 {
				log.Error("empty event data set")
				continue

			}

			funcName, ok := cmd[0]
			if !ok {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("event data is missing command name")
				continue

			}

			switch string(funcName) {
			case "ProfileSet":
				userBytes, ok := cmd[1]
				if !ok {
					log.Error("no user bytes for SetProfile")
					continue
				}
				var u chat.User
				err = msgpack.Unmarshal(userBytes, &u)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error unmarshalling user bytes")
					continue
				}

				deviceBytes, ok := cmd[2]
				if !ok {
					log.Error("no device bytes for set profile")
					continue
				}
				var d chat.Device
				err = msgpack.Unmarshal(deviceBytes, &d)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error unmarshalling device bytes")
					continue
				}

				userInterface.ProfileSet(u, d)
			case "NetworkOnline":
				userInterface.NetworkOnline()
			case "NetworkOffline":
				userInterface.NetworkOffline()
			case "NewSyncDeviceAdded":
				userInterface.NewSyncDeviceAdded()
			case "SyncDeviceRequestAccepted":
				userBytes, ok := cmd[1]
				if !ok {
					log.Error("no user bytes for sync device request accepted")
					continue
				}
				var u chat.User
				err = msgpack.Unmarshal(userBytes, &u)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error unmarshalling user bytes")
					continue
				}

				devicesBytes, ok := cmd[2]
				if !ok {
					log.Error("no device bytes for sdra")
					continue
				}
				var ds []chat.Device
				err = msgpack.Unmarshal(devicesBytes, &ds)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error unmarshalling devices bytes")
					continue
				}

				referencesBytes, ok := cmd[3]
				if !ok {
					log.Error("no device bytes for sdra")
					continue
				}
				ref := false
				if len(referencesBytes) == 1 && referencesBytes[0] == 0x01 {
					ref = true
				}

				userInterface.SyncDeviceRequestAccepted(u, ds, ref)
			case "SyncDeviceRequestRejected":
				peerBytes, ok := cmd[1]
				if !ok {
					log.Error("no peer bytes for sdrr")
					continue
				}
				userInterface.SyncDeviceRequestRejected(string(peerBytes))
			case "InitialSyncStarting":
				userInterface.InitialSyncStarting()
			case "InitialSyncProgress":
				floatBytes, ok := cmd[1]
				if !ok || len(floatBytes) != 9 {
					log.Error("bad float bytes for sync progress")
					continue
				}
				bits := binary.LittleEndian.Uint64(floatBytes)
				f := math.Float64frombits(bits)

				userInterface.InitialSyncProgress(f)
			case "InitialSyncComplete":
				userInterface.InitialSyncComplete()
			case "AddUserRequestRejected":
				peerBytes, ok := cmd[1]
				if !ok {
					log.Error("no peer bytes for sdrr")
					continue
				}
				userInterface.AddUserRequestRejected(string(peerBytes))
			case "UserAdded":
				userBytes, ok := cmd[1]
				if !ok {
					log.Error("no user bytes for user added")
					continue
				}
				var u chat.User
				err = msgpack.Unmarshal(userBytes, &u)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error unmarshalling user bytes")
					continue
				}

				userInterface.UserAdded(u)
			case "DeleteItem":
				idBytes, ok := cmd[1]
				if !ok {
					log.Error("no id bytes for delete item")
					continue
				}
				id, err := uuid.FromBytes(idBytes)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error parsing uuid")
					continue
				}

				userInterface.DeleteItem(id)
			case "MarkMessageUndeliverable":
				idBytes, ok := cmd[1]
				if !ok {
					log.Error("no id bytes for mark message underliverable")
					continue
				}
				id, err := uuid.FromBytes(idBytes)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error parsing uuid")
					continue
				}

				userInterface.MarkMessageUndeliverable(id)
			case "DisplayDirectMessage":
				dmBytes, ok := cmd[1]
				if !ok {
					log.Error("no dm bytes")
					continue
				}

				var dm chat.DirectMessage
				err = msgpack.Unmarshal(dmBytes, &dm)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error unmarshalling dm")
					continue
				}

				userInterface.DisplayDirectMessage(dm)
			case "DisplaySentDirectMessage":
				dmBytes, ok := cmd[1]
				if !ok {
					log.Error("no dm bytes")
					continue
				}

				var dm chat.DirectMessage
				err = msgpack.Unmarshal(dmBytes, &dm)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error unmarshalling dm")
					continue
				}

				userInterface.DisplaySentDirectMessage(dm)
			case "SetDMState":
				idBytes, ok := cmd[1]
				if !ok {
					log.Error("no id bytes for set dm state")
					continue
				}
				id, err := uuid.FromBytes(idBytes)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error parsing uuid")
					continue
				}

				stateBytes, ok := cmd[2]
				if !ok {
					log.Error("no state bytes for set dm state")
					continue
				}
				var state chat.DMState
				err = msgpack.Unmarshal(stateBytes, &state)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error unmarshalling dm state")
					continue
				}

				userInterface.SetDMState(id, state)
			case "DMRetentionChanged":
				bytes, ok := cmd[1]
				if !ok {
					log.Error("no bytes")
					continue
				}

				var udmr chat.UpdateDMRetention
				err = msgpack.Unmarshal(bytes, &udmr)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error unmarshalling")
					continue
				}

				userInterface.DMRetentionChanged(udmr)
			case "DMChatHistoryCleared":
				bytes, ok := cmd[1]
				if !ok {
					log.Error("no bytes")
					continue
				}

				var udch chat.UpdateDMClearHistory
				err = msgpack.Unmarshal(bytes, &udch)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error unmarshalling")
					continue
				}

				userInterface.DMChatHistoryCleared(udch)
			case "UserAliased":
				bytes, ok := cmd[1]
				if !ok {
					log.Error("no bytes")
					continue
				}

				var udsa chat.UpdateDMSetAlias
				err = msgpack.Unmarshal(bytes, &udsa)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error unmarshalling")
					continue
				}

				userInterface.UserAliased(udsa)
			case "UserNameUpdated":
				bytes, ok := cmd[1]
				if !ok {
					log.Error("no bytes")
					continue
				}

				var uuun chat.UpdateUserUpdateName
				err = msgpack.Unmarshal(bytes, &uuun)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error unmarshalling")
					continue
				}

				userInterface.UserNameUpdated(uuun)
			case "OpenNewGroupChat":
				bytes, ok := cmd[1]
				if !ok {
					log.Error("no bytes")
					continue
				}

				var g chat.Group
				err = msgpack.Unmarshal(bytes, &g)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error unmarshalling")
					continue
				}

				userInterface.OpenNewGroupChat(g)
			case "SetGroupState":
				bytes, ok := cmd[1]
				if !ok {
					log.Error("no bytes")
					continue
				}

				var g chat.Group
				err = msgpack.Unmarshal(bytes, &g)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error unmarshalling")
					continue
				}

				userInterface.SetGroupState(g)
			case "DisplaySentGroupMessage":
				bytes, ok := cmd[1]
				if !ok {
					log.Error("no bytes")
					continue
				}

				var gm chat.GroupMessage
				err = msgpack.Unmarshal(bytes, &gm)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error unmarshalling")
					continue
				}

				userInterface.DisplaySentGroupMessage(gm)
			case "DisplayGroupMessage":
				bytes, ok := cmd[1]
				if !ok {
					log.Error("no bytes")
					continue
				}

				var gm chat.GroupMessage
				err = msgpack.Unmarshal(bytes, &gm)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error unmarshalling")
					continue
				}

				userInterface.DisplayGroupMessage(gm)
			case "RemoveUser":
				bytes, ok := cmd[1]
				if !ok {
					log.Error("no bytes")
					continue
				}

				var ugru chat.UpdateGroupRemoveUser
				err = msgpack.Unmarshal(bytes, &ugru)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error unmarshalling")
					continue
				}

				userInterface.RemoveUser(ugru)
			case "RemovedFromGroup":
				bytes, ok := cmd[1]
				if !ok {
					log.Error("no bytes")
					continue
				}

				var rfg chat.RemovedFromGroup
				err = msgpack.Unmarshal(bytes, &rfg)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error unmarshalling")
					continue
				}

				userInterface.RemovedFromGroup(rfg)
			case "GroupDeleted":
				bytes, ok := cmd[1]
				if !ok {
					log.Error("no bytes")
					continue
				}

				var gd chat.GroupDeleted
				err = msgpack.Unmarshal(bytes, &gd)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error unmarshalling")
					continue
				}

				userInterface.GroupDeleted(gd)
			case "UserBlockedGroup":
				bytes, ok := cmd[1]
				if !ok {
					log.Error("no bytes")
					continue
				}

				var ubg chat.UserBlockedGroup
				err = msgpack.Unmarshal(bytes, &ubg)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error unmarshalling")
					continue
				}

				userInterface.UserBlockedGroup(ubg)
			case "RenameGroup":
				bytes, ok := cmd[1]
				if !ok {
					log.Error("no bytes")
					continue
				}

				var ugn chat.UpdateGroupName
				err = msgpack.Unmarshal(bytes, &ugn)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error unmarshalling")
					continue
				}

				userInterface.RenameGroup(ugn)
			case "GroupRetentionChanged":
				bytes, ok := cmd[1]
				if !ok {
					log.Error("no bytes")
					continue
				}

				var ugr chat.UpdateGroupRetention
				err = msgpack.Unmarshal(bytes, &ugr)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error unmarshalling")
					continue
				}

				userInterface.GroupRetentionChanged(ugr)
			case "GroupChatHistoryCleared":
				bytes, ok := cmd[1]
				if !ok {
					log.Error("no bytes")
					continue
				}

				var ugch chat.UpdateGroupClearHistory
				err = msgpack.Unmarshal(bytes, &ugch)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error unmarshalling")
					continue
				}

				userInterface.GroupChatHistoryCleared(ugch)
			case "AdminPromoted":
				bytes, ok := cmd[1]
				if !ok {
					log.Error("no bytes")
					continue
				}

				var ugap chat.UpdateGroupAdminPromoted
				err = msgpack.Unmarshal(bytes, &ugap)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error unmarshalling")
					continue
				}

				userInterface.AdminPromoted(ugap)
			case "AdminDemoted":
				bytes, ok := cmd[1]
				if !ok {
					log.Error("no bytes")
					continue
				}

				var ugad chat.UpdateGroupAdminDemoted
				err = msgpack.Unmarshal(bytes, &ugad)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error unmarshalling")
					continue
				}

				userInterface.AdminDemoted(ugad)
			case "UserManagementRestricted":
				bytes, ok := cmd[1]
				if !ok {
					log.Error("no bytes")
					continue
				}

				var ugumr chat.UpdateGroupUserManagementRestricted
				err = msgpack.Unmarshal(bytes, &ugumr)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error unmarshalling")
					continue
				}

				userInterface.UserManagementRestricted(ugumr)
			case "UserManagementUnrestricted":
				bytes, ok := cmd[1]
				if !ok {
					log.Error("no bytes")
					continue
				}

				var ugumu chat.UpdateGroupUserManagementUnrestricted
				err = msgpack.Unmarshal(bytes, &ugumu)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error unmarshalling")
					continue
				}

				userInterface.UserManagementUnrestricted(ugumu)
			case "GroupEditsRestricted":
				bytes, ok := cmd[1]
				if !ok {
					log.Error("no bytes")
					continue
				}

				var uger chat.UpdateGroupEditsRestricted
				err = msgpack.Unmarshal(bytes, &uger)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error unmarshalling")
					continue
				}

				userInterface.GroupEditsRestricted(uger)
			case "GroupEditsUnrestricted":
				bytes, ok := cmd[1]
				if !ok {
					log.Error("no bytes")
					continue
				}

				var ugeu chat.UpdateGroupEditsUnrestricted
				err = msgpack.Unmarshal(bytes, &ugeu)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error unmarshalling")
					continue
				}

				userInterface.GroupEditsUnrestricted(ugeu)
			default:
				log.WithFields(log.Fields{
					"function": string(funcName),
				}).Warn("unsupposed event from the chat engine")
			}
		}
	}
}
