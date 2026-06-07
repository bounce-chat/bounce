package chat

import (
	"encoding/binary"
	"errors"
	"os"
	"strings"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"

	stdlog "log"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

// Frames that are sent over the network that have a corresponding database table
var typeTable = map[uint16]string{
	typeDirectMessage:                "direct_messages",
	typeGroupMessage:                 "group_messages",
	typeDevice:                       "devices",
	typeUpdateDM:                     "update_dms",
	typeGroupCreation:                "group_creations",
	typeUpdateGroup:                  "update_groups",
	typeAddUser:                      "add_users",
	typeConfirmation:                 "confirmations",
	typeUpdateUser:                   "update_users",
	typeUpdateDevice:                 "update_devices",
	typeReadReceipt:                  "read_receipts",
	typeUpdateSettings:               "update_settings",
	typeFile:                         "files",
	typeChunk:                        "chunks",
	typeChunkOffer:                   "chunk_offers",
	typeAppendRecipient:              "append_recipients",
	typeDraft:                        "drafts",
	typeEncryptedChunkOffer:          "encrypted_chunk_offers",
	typeEncryptedChunkStorageRequest: "encrypted_chunk_storage_requests",
}

func (b *Bounce) openDatabase() {
	databaseFile := b.configDirectory + "/bounce.db"

	// Define a logger for gorm that uses logrus
	gormLogger := logger.New(
		stdlog.New(os.Stdout, "\r\n", stdlog.LstdFlags), // TODO: https://gist.github.com/bnadland/2e4287b801a47dcfcc94
		logger.Config{
			LogLevel:                  logger.Error,
			IgnoreRecordNotFoundError: true,
		},
	)

	// Open the database
	var err error
	b.database, err = gorm.Open(sqlite.Open(databaseFile), &gorm.Config{
		TranslateError: true,
		Logger:         gormLogger,
	})
	if err != nil {
		log.WithFields(log.Fields{
			"file":  databaseFile,
			"error": err.Error(),
		}).Fatal("error opening database")
	}

	// Set max connections to 1 to avoid locks
	sqliteDB, err := b.database.DB()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error getting underlying database interface from gorm while opening database")
	}
	sqliteDB.SetMaxOpenConns(1)

	// Migrate
	err = b.database.AutoMigrate(
		&user{},
		&device{},
		&introductionSignature{},
		&directMessage{},
		&syncDeviceOffer{},
		&deliveryRecord{},
		&updateDM{},
		&groupCreation{},
		&group{},
		&groupMessage{},
		&updateGroup{},
		&addUser{},
		&addUserOffer{},
		&customScope{},
		&confirmation{},
		&profileSettings{},
		&updateUser{},
		&updateDevice{},
		&readReceipt{},
		&updateSettings{},
		&file{},
		&chunk{},
		&chunkOffer{},
		&imageAttachment{},
		&fileAttachment{},
		&encryptedSyncDevice{},
		&appendRecipient{},
		&draft{},
		&encryptedChunkOffer{},
	)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error migrating the database")
	}

	// Set bool values that are nulled
	err = b.database.Table("profile_settings").Where("default_send_read_receipts IS NULL").Update("default_send_read_receipts", true).Error
	if err != nil {
		log.WithFields(log.Fields{"error": err.Error()}).Fatal("error migrating profile settings")
	}
	err = b.database.Table("profile_settings").Where("default_send_typing_indicators IS NULL").Update("default_send_typing_indicators", true).Error
	if err != nil {
		log.WithFields(log.Fields{"error": err.Error()}).Fatal("error migrating profile settings")
	}
	err = b.database.Table("profile_settings").Where("new_group_restrict_user_management IS NULL").Update("new_group_restrict_user_management", true).Error
	if err != nil {
		log.WithFields(log.Fields{"error": err.Error()}).Fatal("error migrating profile settings")
	}
	err = b.database.Table("profile_settings").Where("new_group_restrict_group_edits IS NULL").Update("new_group_restrict_group_edits", false).Error
	if err != nil {
		log.WithFields(log.Fields{"error": err.Error()}).Fatal("error migrating profile settings")
	}
	err = b.database.Table("profile_settings").Where("new_group_restrict_posting IS NULL").Update("new_group_restrict_posting", false).Error
	if err != nil {
		log.WithFields(log.Fields{"error": err.Error()}).Fatal("error migrating profile settings")
	}

	// Load revoked devices into the in-memory map
	b.populateRevokedDevices()
}

func (b *Bounce) pruneDirectMessages() {
	// Delete messages from the database that are past expiration
	err := b.database.Clauses(clause.Returning{}).Where("delete_at != 0 AND delete_at < ?", time.Now().Unix()).Delete(&directMessage{}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error batch deleting direct messages past retention")
	}

	// Find messages that will expire in the future and create goroutines to delete them when needed
	var dmsToDeleteLater []directMessage
	err = b.database.Select("id", "delete_at").Where("delete_at != 0").Find(&dmsToDeleteLater).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error selecting direct messages that will expire")
	}
	for _, dm := range dmsToDeleteLater {
		go b.deleteDirectMessageAt(dm.DeleteAt, dm.ID)
	}

	// Mark any messages that can no longer be delivered as undeliverable
	now := time.Now()
	var undeliverableDMs []directMessage
	err = b.database.
		Select("direct_messages.id").
		Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == direct_messages.id AND delivery_records.frame_type == ?", typeDirectMessage).
		Where(
			"delivery_records.id IS NULL AND direct_messages.written_at <= ? AND undeliverable = false",
			now.Add(-undeliverableAfter).Unix(),
		).
		Find(&undeliverableDMs).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error selecting direct message that are undeliverable while pruning database")
	}
	for _, dm := range undeliverableDMs {
		err = b.database.Model(&dm).Update("undeliverable", true).Error
		if err != nil {
			log.WithFields(log.Fields{
				"message_id": dm.ID,
				"error":      err.Error(),
			}).Fatal("error updating undeliverable field of undeliverable direct message")
		}
	}
}

func (b *Bounce) pruneGroupMessages() {
	// Delete messages from the database that are past expiration
	err := b.database.Clauses(clause.Returning{}).Where("delete_at != 0 AND delete_at < ?", time.Now().Unix()).Delete(&groupMessage{}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error batch deleting group messages past retention")
	}

	// Find messages that will expire in the future and create goroutines to delete them when needed
	var gmsToDeleteLater []groupMessage
	err = b.database.Select("id", "delete_at").Where("delete_at != 0").Find(&gmsToDeleteLater).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error selecting group messages that will expire")
	}
	for _, gm := range gmsToDeleteLater {
		go b.deleteGroupMessageAt(gm.DeleteAt, gm.ID)
	}

	// Mark any messages that can no longer be delivered as undeliverable
	now := time.Now()
	var undeliverableGMs []groupMessage
	err = b.database.
		Select("group_messages.id").
		Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == group_messages.id AND delivery_records.frame_type == ?", typeGroupMessage).
		Where(
			"delivery_records.id IS NULL AND group_messages.written_at <= ? AND undeliverable = false",
			now.Add(-undeliverableAfter).Unix(),
		).
		Find(&undeliverableGMs).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error selecting group message that are undeliverable while pruning database")
	}
	for _, gm := range undeliverableGMs {
		err = b.database.Model(&gm).Update("undeliverable", true).Error
		if err != nil {
			log.WithFields(log.Fields{
				"message_id": gm.ID,
				"error":      err.Error(),
			}).Fatal("error updating undeliverable field of undeliverable group message")
		}
	}
}

func (b *Bounce) pruneUndeliverableCustomScopes() {
	deleteBefore := time.Now().Unix() - int64(undeliverableAfter.Seconds())

	// Prune update groups
	err := b.database.Clauses(clause.Returning{}).Where("custom_scope != ? AND timestamp < ?", uuid.Nil, deleteBefore).Delete(&updateGroup{}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error pruning update groups")
	}
}

func (b *Bounce) GetInitialState() InitialState {
	// Prune the database
	b.pruneDirectMessages()
	b.pruneGroupMessages()
	b.pruneUndeliverableCustomScopes()

	// Load the profile and sync devices
	var profile *User
	var settings Settings
	syncDevices := []Device{}
	var dbProfile user
	err := b.database.Preload(clause.Associations).Where("profile = ?", true).First(&dbProfile).Error
	if err == nil {
		imageHistory := []uuid.UUID{}
		if len(dbProfile.Images) > 0 {
			for _, imageIDString := range strings.Split(dbProfile.Images, ",") {
				imageID, err := uuid.Parse(imageIDString)
				if err != nil {
					log.WithFields(log.Fields{
						"error":  err.Error(),
						"images": dbProfile.Images,
					}).Fatal("invalid UUID in user images list")
				}
				imageHistory = append(imageHistory, imageID)
			}
		}

		profile = &User{
			ID:               dbProfile.ID,
			Name:             dbProfile.Name,
			Alias:            dbProfile.Alias,
			Notes:            dbProfile.Notes,
			IntroductionTime: dbProfile.IntroductionTime,
			Images:           imageHistory,
			State: DMState{
				Open:                           dbProfile.OpenDM,
				Retention:                      dbProfile.Retention,
				MutedUntil:                     dbProfile.MutedUntil,
				LastActivity:                   dbProfile.LastActivity,
				OverrideReadReceiptSetting:     dbProfile.ReadReceiptsOverridden,
				ReadReceiptsEnabled:            dbProfile.ReadReceiptsEnabled,
				OverrideTypingIndicatorSetting: dbProfile.TypingIndicatorsOverridden,
				TypingIndicatorsEnabled:        dbProfile.TypingIndicatorsEnabled,
			},
		}
		settings.DefaultDMRetention = dbProfile.ProfileSettings.DefaultDMRetention
		settings.DefaultGroupRetention = dbProfile.ProfileSettings.DefaultGroupRetention
		settings.DefaultSendReadReceipts = dbProfile.ProfileSettings.DefaultSendReadReceipts
		settings.DefaultSendTypingIndicators = dbProfile.ProfileSettings.DefaultSendTypingIndicators
		settings.NewGroupRestrictUserManagement = dbProfile.ProfileSettings.NewGroupRestrictUserManagement
		settings.NewGroupRestrictGroupEdits = dbProfile.ProfileSettings.NewGroupRestrictGroupEdits
		settings.NewGroupRestrictPosting = dbProfile.ProfileSettings.NewGroupRestrictPosting
		for _, dev := range dbProfile.Devices {
			if dev.RevokedAt != 0 {
				continue
			}
			syncDevices = append(
				syncDevices,
				Device{
					ID:        dev.ID,
					Name:      dev.Name,
					Address:   dev.Address,
					CreatedAt: dev.Timestamp,
					LastSeen:  dev.LastSeen,
					Local:     dev.Address == b.network.Address(),
					Encrypted: false,
					Online:    false,
				},
			)
		}

		var allEncryptedSyncDevices []encryptedSyncDevice
		err := b.database.Find(&allEncryptedSyncDevices).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error getting all encrypted sync devices")
		}
		for _, esd := range allEncryptedSyncDevices {
			encryptedDeviceCache[esd.Address] = dbProfile.ID
			syncDevices = append(
				syncDevices,
				Device{
					ID:        esd.ID,
					Name:      esd.Name,
					Address:   esd.Address,
					CreatedAt: esd.CreatedAt,
					LastSeen:  esd.LastSeen,
					Local:     false,
					Encrypted: true,
					Online:    false,
				},
			)
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up profile user")
	}

	// Load all users
	users := []user{}
	err = b.database.Where("profile = ?", false).Find(&users).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up all users")
	}
	chatUsers := []User{}
	for _, u := range users {
		chatUsers = append(chatUsers, User{
			ID:               u.ID,
			Name:             u.Name,
			Alias:            u.Alias,
			Notes:            u.Notes,
			Blocked:          u.Blocked,
			Images:           u.images(),
			IntroductionTime: u.IntroductionTime,
			State: DMState{
				Open:                           u.OpenDM,
				Retention:                      u.Retention,
				MutedUntil:                     u.MutedUntil,
				LastActivity:                   u.LastActivity,
				OverrideReadReceiptSetting:     u.ReadReceiptsOverridden,
				ReadReceiptsEnabled:            u.ReadReceiptsEnabled,
				OverrideTypingIndicatorSetting: u.TypingIndicatorsOverridden,
				TypingIndicatorsEnabled:        u.TypingIndicatorsEnabled,
			},
		})

		if u.Blocked {
			cacheBlockedUser(u.ID)
		}

		if len(u.EncryptedDevices) > 0 {
			for _, addr := range strings.Split(u.EncryptedDevices, ",") {
				if assigned, ok := encryptedDeviceCache[addr]; ok {
					if assigned != u.ID {
						log.WithFields(log.Fields{
							"assigned_to": assigned,
							"claiming":    u.ID,
							"address":     addr,
						}).Error("encrypted device already assigned to different user")
						continue
					}
				}
				encryptedDeviceCache[addr] = u.ID
			}
		}
	}

	// Load all groups
	groups := []group{}
	err = b.database.Preload(clause.Associations).Find(&groups).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up all groups")
	}
	chatGroups := []Group{}
	for _, g := range groups {
		imageHistory := []uuid.UUID{}
		if len(g.Images) > 0 {
			for _, imageIDString := range strings.Split(g.Images, ",") {
				imageID, err := uuid.Parse(imageIDString)
				if err != nil {
					log.WithFields(log.Fields{
						"error":  err.Error(),
						"images": g.Images,
					}).Fatal("invalid UUID in group images list")
				}
				imageHistory = append(imageHistory, imageID)
			}
		}
		userList := []User{}
		for _, u := range g.Users {
			userList = append(userList, User{ID: u.ID, Name: u.Name})
		}
		adminList := []uuid.UUID{}
		if len(g.Admins) > 0 {
			for _, adminIDString := range strings.Split(g.Admins, ",") {
				adminID, err := uuid.Parse(adminIDString)
				if err != nil {
					log.WithFields(log.Fields{
						"error":  err.Error(),
						"admins": g.Admins,
					}).Fatal("invalid UUID in group admin list")
				}
				adminList = append(adminList, adminID)
			}
		}
		invitedList := []User{}
		if len(g.Invites) > 0 {
			for _, inviteIDString := range strings.Split(g.Invites, ",") {
				invitedID, err := uuid.Parse(inviteIDString)
				if err != nil {
					log.WithFields(log.Fields{
						"error":   err.Error(),
						"invites": g.Invites,
					}).Fatal("invalid UUID in group invite list")
				}

				var u user
				err = b.database.Select("name").First(&u, "id = ?", invitedID).Error
				if err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						log.WithFields(log.Fields{
							"error":   err.Error(),
							"user_id": invitedID,
						}).Error("group final state contains invited user not in database")
						continue
					} else {
						log.WithFields(log.Fields{
							"error": err.Error(),
						}).Fatal("database error looking up user")
					}
				}
				invitedList = append(invitedList, User{ID: invitedID, Name: u.Name})
			}
		}
		blockedList := []uuid.UUID{}
		if len(g.BlockedUsers) > 0 {
			for _, blockedIDString := range strings.Split(g.BlockedUsers, ",") {
				blockedID, err := uuid.Parse(blockedIDString)
				if err != nil {
					log.WithFields(log.Fields{
						"error":         err.Error(),
						"blocked_users": g.BlockedUsers,
					}).Fatal("invalid UUID in group block list")
				}
				blockedList = append(blockedList, blockedID)
			}
		}
		chatGroups = append(chatGroups, Group{
			ID:                             g.ID,
			Name:                           g.Name,
			Images:                         imageHistory,
			Users:                          userList,
			Admins:                         adminList,
			Invites:                        invitedList,
			InvitedBy:                      g.InvitedBy,
			InvitedAt:                      g.InvitedAt,
			AcceptedAt:                     g.AcceptedAt,
			BlockedUsers:                   blockedList,
			CreatedBy:                      g.CreatedBy,
			CreatedAt:                      g.CreatedAt,
			Retention:                      g.Retention,
			MutedUntil:                     g.MutedUntil,
			LastActivity:                   g.LastActivity,
			RestrictUserManagement:         g.RestrictUserManagement,
			RestrictGroupEdits:             g.RestrictGroupEdits,
			RestrictPosting:                g.RestrictPosting,
			OverrideReadReceiptSetting:     g.ReadReceiptsOverridden,
			ReadReceiptsEnabled:            g.ReadReceiptsEnabled,
			OverrideTypingIndicatorSetting: g.TypingIndicatorsOverridden,
			TypingIndicatorsEnabled:        g.TypingIndicatorsEnabled,
		})
	}

	// Load all direct messages
	dms := []directMessage{}
	err = b.database.Preload(clause.Associations).Order("saved_at asc").Find(&dms).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up all direct messages")
	}

	dmRRs := []readReceipt{}
	dmRRmap := map[uuid.UUID][]ReadReceipt{}
	err = b.database.Where("target_type = ?", typeDirectMessage).Find(&dmRRs).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error looking up read receipts")
	}
	for _, rr := range dmRRs {
		dmRRmap[rr.Target] = append(
			dmRRmap[rr.Target],
			ReadReceipt{
				ID:     rr.ID,
				Actor:  rr.Actor,
				Target: rr.Target,
			},
		)
	}

	dmDRs := []deliveryRecord{}
	dmDRmap := map[uuid.UUID][]uuid.UUID{}
	err = b.database.Where("frame_type = ?", typeDirectMessage).Find(&dmDRs).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error looking up delivery records")
	}
	for _, dr := range dmDRs {
		dev, ok := b.getDeviceFromAddress(dr.Destination)
		if ok {
			dmDRmap[dr.FrameID] = append(
				dmDRmap[dr.FrameID],
				dev.UserID,
			)
		} else {
			encryptedDeviceCacheMutex.Lock()
			userID, ok := encryptedDeviceCache[dr.Destination]
			encryptedDeviceCacheMutex.Unlock()
			if ok {
				dmDRmap[dr.FrameID] = append(
					dmDRmap[dr.FrameID],
					userID,
				)
			}
		}
	}

	exportedDMs := []DirectMessage{}
	for _, dm := range dms {
		readReceipts, ok := dmRRmap[dm.ID]
		if !ok {
			readReceipts = []ReadReceipt{}
		}
		deliveredTo, ok := dmDRmap[dm.ID]
		if !ok {
			deliveredTo = []uuid.UUID{}
			if !dm.Undeliverable {
				go b.checkIfDirectMessageUndeliverableAt(time.Unix(dm.WrittenAt, 0).Add(undeliverableAfter).Unix(), dm.ID)
			}
		}
		uiImageAttachments := []ImageAttachment{}
		for _, ia := range dm.ImageAttachments {
			uiImageAttachments = append(uiImageAttachments, ImageAttachment{
				ID:       ia.FileID,
				Name:     ia.Name,
				Size:     ia.Size,
				Width:    ia.Width,
				Height:   ia.Height,
				BlurHash: ia.BlurHash,
			})
		}
		uiFileAttachments := []FileAttachment{}
		for _, fa := range dm.FileAttachments {
			uiFileAttachments = append(uiFileAttachments, FileAttachment{
				ID:   fa.FileID,
				Name: fa.Name,
				Size: fa.Size,
			})
		}
		exportedDMs = append(
			exportedDMs,
			DirectMessage{
				ID:               dm.ID,
				Author:           dm.Author,
				Thread:           dm.getDestination(b.currentUserID()),
				WrittenAt:        dm.WrittenAt,
				SavedAt:          dm.SavedAt,
				ExpiresAt:        dm.DeleteAt,
				Text:             dm.Text,
				ImageAttachments: uiImageAttachments,
				FileAttachments:  uiFileAttachments,
				Seen:             dm.Seen,
				Undeliverable:    dm.Undeliverable,
				ReadReceipts:     readReceipts,
				DeliveredTo:      deliveredTo,
			},
		)
	}

	udms := []updateDM{}
	err = b.database.Order("timestamp asc").Find(&udms).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up all update DMs")
	}
	exportedUpdateDMRetentions := []UpdateDMRetention{}
	exportedUpdateDMClearHistories := []UpdateDMClearHistory{}
	for _, udm := range udms {
		switch udm.Type {
		case updateDMTypeChangeRetention:
			exportedUpdateDMRetentions = append(
				exportedUpdateDMRetentions,
				UpdateDMRetention{
					ID:        udm.ID,
					Thread:    xor(udm.Target, b.currentUserID()),
					Actor:     udm.Actor,
					Timestamp: udm.Timestamp,
					Seen:      udm.Seen,
					Retention: int64(binary.LittleEndian.Uint64(udm.Data)),
				},
			)
		case updateDMTypeSetClearBefore:
			exportedUpdateDMClearHistories = append(
				exportedUpdateDMClearHistories,
				UpdateDMClearHistory{
					ID:        udm.ID,
					Thread:    xor(udm.Target, b.currentUserID()),
					Actor:     udm.Actor,
					Timestamp: udm.Timestamp,
					Seen:      udm.Seen,
					ClearTime: int64(binary.LittleEndian.Uint64(udm.Data)),
				},
			)
		}
	}

	// Load all group messages
	gms := []groupMessage{}
	err = b.database.Preload(clause.Associations).Order("saved_at asc").Find(&gms).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up all group messages")
	}

	gmRRs := []readReceipt{}
	gmRRmap := map[uuid.UUID][]ReadReceipt{}
	err = b.database.Where("target_type = ?", typeGroupMessage).Find(&gmRRs).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error looking up read receipts")
	}
	for _, rr := range gmRRs {
		gmRRmap[rr.Target] = append(
			gmRRmap[rr.Target],
			ReadReceipt{
				ID:     rr.ID,
				Actor:  rr.Actor,
				Target: rr.Target,
			},
		)
	}

	gmDRs := []deliveryRecord{}
	gmDRmap := map[uuid.UUID][]uuid.UUID{}
	err = b.database.Where("frame_type = ?", typeGroupMessage).Find(&gmDRs).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error looking up delivery records")
	}
	for _, dr := range gmDRs {
		dev, ok := b.getDeviceFromAddress(dr.Destination)
		if ok {
			gmDRmap[dr.FrameID] = append(
				gmDRmap[dr.FrameID],
				dev.UserID,
			)
		} else {
			encryptedDeviceCacheMutex.Lock()
			userID, ok := encryptedDeviceCache[dr.Destination]
			encryptedDeviceCacheMutex.Unlock()
			if ok {
				gmDRmap[dr.FrameID] = append(
					gmDRmap[dr.FrameID],
					userID,
				)
			}
		}
	}

	exportedGMs := []GroupMessage{}
	for _, gm := range gms {
		readReceipts, ok := gmRRmap[gm.ID]
		if !ok {
			readReceipts = []ReadReceipt{}
		}
		deliveredTo, ok := gmDRmap[gm.ID]
		if !ok {
			deliveredTo = []uuid.UUID{}
			if !gm.Undeliverable {
				go b.checkIfGroupMessageUndeliverableAt(time.Unix(gm.WrittenAt, 0).Add(undeliverableAfter).Unix(), gm.ID)
			}
		}
		uiImageAttachments := []ImageAttachment{}
		for _, ia := range gm.ImageAttachments {
			uiImageAttachments = append(uiImageAttachments, ImageAttachment{
				ID:       ia.FileID,
				Name:     ia.Name,
				Size:     ia.Size,
				Width:    ia.Width,
				Height:   ia.Height,
				BlurHash: ia.BlurHash,
			})
		}
		uiFileAttachments := []FileAttachment{}
		for _, fa := range gm.FileAttachments {
			uiFileAttachments = append(uiFileAttachments, FileAttachment{
				ID:   fa.FileID,
				Name: fa.Name,
				Size: fa.Size,
			})
		}
		exportedGMs = append(
			exportedGMs,
			GroupMessage{
				ID:               gm.ID,
				Author:           gm.Author,
				Thread:           gm.getDestination(b.currentUserID()),
				WrittenAt:        gm.WrittenAt,
				SavedAt:          gm.SavedAt,
				ExpiresAt:        gm.DeleteAt,
				Text:             gm.Text,
				ImageAttachments: uiImageAttachments,
				FileAttachments:  uiFileAttachments,
				Seen:             gm.Seen,
				Undeliverable:    gm.Undeliverable,
				ReadReceipts:     readReceipts,
				DeliveredTo:      deliveredTo,
			},
		)
	}

	// Load all update groups
	ugs := []updateGroup{}
	err = b.database.Where("applied = true AND custom_scope = ?", uuid.Nil).Order("timestamp asc").Find(&ugs).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up all update groups")
	}
	exportedUpdateGroupRetentions := []UpdateGroupRetention{}
	exportedUpdateGroupNames := []UpdateGroupName{}
	exportedUpdateGroupInvitedUsers := []UpdateGroupInviteUser{}
	exportedUpdateGroupRemoveUsers := []UpdateGroupRemoveUser{}
	exportedUpdateGroupClearHistories := []UpdateGroupClearHistory{}
	exportedUpdateGroupAdminPromotions := []UpdateGroupAdminPromoted{}
	exportedUpdateGroupAdminDemotions := []UpdateGroupAdminDemoted{}
	exportedUpdateGroupUserManagementsRestricted := []UpdateGroupUserManagementRestricted{}
	exportedUpdateGroupUserManagementsUnrestricted := []UpdateGroupUserManagementUnrestricted{}
	exportedUpdateGroupEditsRestricted := []UpdateGroupEditsRestricted{}
	exportedUpdateGroupEditsUnrestricted := []UpdateGroupEditsUnrestricted{}
	exportedUpdateGroupPostingsRestricted := []UpdateGroupPostingRestricted{}
	exportedUpdateGroupPostingsUnrestricted := []UpdateGroupPostingUnrestricted{}
	exportedUpdateGroupUserBlockedGroups := []UserBlockedGroup{}
	exportedUpdateGroupUserChangedGroupImages := []UpdateGroupUserChangedGroupImage{}
	exportedUpdateGroupInvitesRevoked := []UpdateGroupInviteRevoked{}
	exportedUpdateGroupInvitesAccepted := []UpdateGroupInviteAccepted{}
	exportedUpdateGroupInvitesRejected := []UpdateGroupInviteRejected{}
	for _, ug := range ugs {
		switch ug.Type {
		case updateGroupTypeChangeName:
			exportedUpdateGroupNames = append(
				exportedUpdateGroupNames,
				UpdateGroupName{
					ID:        ug.ID,
					Thread:    ug.Target,
					Actor:     ug.Actor,
					Timestamp: ug.Timestamp,
					Seen:      ug.Seen,
					Name:      string(ug.Data),
				},
			)
		case updateGroupTypeChangeRetention:
			exportedUpdateGroupRetentions = append(
				exportedUpdateGroupRetentions,
				UpdateGroupRetention{
					ID:        ug.ID,
					Thread:    ug.Target,
					Actor:     ug.Actor,
					Timestamp: ug.Timestamp,
					Seen:      ug.Seen,
					Retention: int64(binary.LittleEndian.Uint64(ug.Data)),
				},
			)
		case updateGroupTypeInviteUser:
			var u user
			err := msgpack.Unmarshal(ug.Data, &u)
			if err != nil {
				log.WithFields(log.Fields{
					"id":    ug.ID,
					"error": err.Error(),
				}).Fatal("error unmarshalling user in update group invite user in database")
			}

			exportedUpdateGroupInvitedUsers = append(
				exportedUpdateGroupInvitedUsers,
				UpdateGroupInviteUser{
					ID:        ug.ID,
					Thread:    ug.Target,
					Actor:     ug.Actor,
					Timestamp: ug.Timestamp,
					Seen:      ug.Seen,
					User: User{
						ID:   u.ID,
						Name: u.Name,
					},
				},
			)
		case updateGroupTypeRemoveUser:
			userID, err := uuid.FromBytes(ug.Data)
			if err != nil {
				log.WithFields(log.Fields{
					"id":    ug.Data,
					"error": err.Error(),
				}).Fatal("error parsing user ID for remove user in database")
			}
			exportedUpdateGroupRemoveUsers = append(
				exportedUpdateGroupRemoveUsers,
				UpdateGroupRemoveUser{
					ID:        ug.ID,
					Thread:    ug.Target,
					Actor:     ug.Actor,
					Timestamp: ug.Timestamp,
					Seen:      ug.Seen,
					User:      userID,
				},
			)
		case updateGroupTypeSetClearBefore:
			exportedUpdateGroupClearHistories = append(
				exportedUpdateGroupClearHistories,
				UpdateGroupClearHistory{
					ID:        ug.ID,
					Thread:    ug.Target,
					Actor:     ug.Actor,
					Timestamp: ug.Timestamp,
					Seen:      ug.Seen,
					ClearTime: int64(binary.LittleEndian.Uint64(ug.Data)),
				},
			)
		case updateGroupTypePromoteAdmin:
			userID, err := uuid.FromBytes(ug.Data)
			if err != nil {
				log.WithFields(log.Fields{
					"id":    ug.Data,
					"error": err.Error(),
				}).Fatal("error parsing user ID for admin promotion in database")
			}

			exportedUpdateGroupAdminPromotions = append(
				exportedUpdateGroupAdminPromotions,
				UpdateGroupAdminPromoted{
					ID:        ug.ID,
					Thread:    ug.Target,
					Actor:     ug.Actor,
					Timestamp: ug.Timestamp,
					Seen:      ug.Seen,
					UserID:    userID,
				},
			)
		case updateGroupTypeDemoteAdmin:
			userID, err := uuid.FromBytes(ug.Data)
			if err != nil {
				log.WithFields(log.Fields{
					"id":    ug.Data,
					"error": err.Error(),
				}).Fatal("error parsing user ID for admin demotion in database")
			}

			exportedUpdateGroupAdminDemotions = append(
				exportedUpdateGroupAdminDemotions,
				UpdateGroupAdminDemoted{
					ID:        ug.ID,
					Thread:    ug.Target,
					Actor:     ug.Actor,
					Timestamp: ug.Timestamp,
					Seen:      ug.Seen,
					UserID:    userID,
				},
			)
		case updateGroupTypeChangeUserManagementPermission:
			restricted, err := ug.permissionPayloadIsRestricted()
			if err != nil {
				log.WithFields(log.Fields{
					"id":    ug.ID,
					"error": err.Error(),
				}).Fatal("invalid update group permission payload in database")
			}

			if restricted {
				exportedUpdateGroupUserManagementsRestricted = append(
					exportedUpdateGroupUserManagementsRestricted,
					UpdateGroupUserManagementRestricted{
						ID:        ug.ID,
						Thread:    ug.Target,
						Actor:     ug.Actor,
						Timestamp: ug.Timestamp,
						Seen:      ug.Seen,
					},
				)
			} else {
				exportedUpdateGroupUserManagementsUnrestricted = append(
					exportedUpdateGroupUserManagementsUnrestricted,
					UpdateGroupUserManagementUnrestricted{
						ID:        ug.ID,
						Thread:    ug.Target,
						Actor:     ug.Actor,
						Timestamp: ug.Timestamp,
						Seen:      ug.Seen,
					},
				)
			}
		case updateGroupTypeChangeGroupEditsPermission:
			restricted, err := ug.permissionPayloadIsRestricted()
			if err != nil {
				log.WithFields(log.Fields{
					"id":    ug.ID,
					"error": err.Error(),
				}).Fatal("invalid update group permission payload in database")
			}

			if restricted {
				exportedUpdateGroupEditsRestricted = append(
					exportedUpdateGroupEditsRestricted,
					UpdateGroupEditsRestricted{
						ID:        ug.ID,
						Thread:    ug.Target,
						Actor:     ug.Actor,
						Timestamp: ug.Timestamp,
						Seen:      ug.Seen,
					},
				)
			} else {
				exportedUpdateGroupEditsUnrestricted = append(
					exportedUpdateGroupEditsUnrestricted,
					UpdateGroupEditsUnrestricted{
						ID:        ug.ID,
						Thread:    ug.Target,
						Actor:     ug.Actor,
						Timestamp: ug.Timestamp,
						Seen:      ug.Seen,
					},
				)
			}
		case updateGroupTypeChangePostingPermission:
			restricted, err := ug.permissionPayloadIsRestricted()
			if err != nil {
				log.WithFields(log.Fields{
					"id":    ug.ID,
					"error": err.Error(),
				}).Fatal("invalid update group permission payload in database")
			}

			if restricted {
				exportedUpdateGroupPostingsRestricted = append(
					exportedUpdateGroupPostingsRestricted,
					UpdateGroupPostingRestricted{
						ID:        ug.ID,
						Thread:    ug.Target,
						Actor:     ug.Actor,
						Timestamp: ug.Timestamp,
						Seen:      ug.Seen,
					},
				)
			} else {
				exportedUpdateGroupPostingsUnrestricted = append(
					exportedUpdateGroupPostingsUnrestricted,
					UpdateGroupPostingUnrestricted{
						ID:        ug.ID,
						Thread:    ug.Target,
						Actor:     ug.Actor,
						Timestamp: ug.Timestamp,
						Seen:      ug.Seen,
					},
				)
			}

		case updateGroupTypeBlock:
			exportedUpdateGroupUserBlockedGroups = append(
				exportedUpdateGroupUserBlockedGroups,
				UserBlockedGroup{
					ID:        ug.ID,
					Thread:    ug.Target,
					Actor:     ug.Actor,
					Timestamp: ug.Timestamp,
				},
			)
		case updateGroupTypeSetImage:
			exportedUpdateGroupUserChangedGroupImages = append(
				exportedUpdateGroupUserChangedGroupImages,
				UpdateGroupUserChangedGroupImage{
					ID:        ug.ID,
					Thread:    ug.Target,
					Actor:     ug.Actor,
					Timestamp: ug.Timestamp,
				},
			)
		case updateGroupTypeRevokeInvite:
			userID, err := uuid.FromBytes(ug.Data)
			if err != nil {
				log.WithFields(log.Fields{
					"id":    ug.Data,
					"error": err.Error(),
				}).Fatal("error parsing user ID for invite revocation in database")
			}

			exportedUpdateGroupInvitesRevoked = append(
				exportedUpdateGroupInvitesRevoked,
				UpdateGroupInviteRevoked{
					ID:        ug.ID,
					Thread:    ug.Target,
					Actor:     ug.Actor,
					Timestamp: ug.Timestamp,
					UserID:    userID,
					Seen:      ug.Seen,
				},
			)
		case updateGroupTypeRespondToInvite:
			if len(ug.Data) != 1 {
				log.WithFields(log.Fields{
					"id": ug.ID,
				}).Error("invalid payload length for update group respond to invite")
				continue
			}
			rejected := ug.Data[0] == rejectInvite
			accepted := ug.Data[0] == acceptInvite

			if accepted {
				exportedUpdateGroupInvitesAccepted = append(
					exportedUpdateGroupInvitesAccepted,
					UpdateGroupInviteAccepted{
						ID:        ug.ID,
						Thread:    ug.Target,
						Actor:     ug.Actor,
						Timestamp: ug.Timestamp,
						Seen:      ug.Seen,
					},
				)
			} else if rejected {
				exportedUpdateGroupInvitesRejected = append(
					exportedUpdateGroupInvitesRejected,
					UpdateGroupInviteRejected{
						ID:        ug.ID,
						Thread:    ug.Target,
						Actor:     ug.Actor,
						Timestamp: ug.Timestamp,
						Seen:      ug.Seen,
					},
				)
			} else {
				log.WithFields(log.Fields{
					"id":      ug.ID,
					"payload": ug.Data[0],
				}).Error("invalid payload for update group respond to invite")
				continue
			}
		}
	}

	// Load all update users
	uus := []updateUser{}
	err = b.database.Order("timestamp asc").Find(&uus).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up all update users")
	}
	exportedUpdateUserUpdateNames := []UpdateUserUpdateName{}
	exportedUpdateUserUpdateImages := []UpdateUserUpdateImage{}
	for _, uu := range uus {
		switch uu.Type {
		case updateUserTypeUpdateName:
			exportedUpdateUserUpdateNames = append(
				exportedUpdateUserUpdateNames,
				UpdateUserUpdateName{
					ID:        uu.ID,
					User:      uu.Target,
					Name:      string(uu.Data),
					OldName:   string(uu.PreviousData),
					Timestamp: uu.Timestamp,
				},
			)
		case updateUserTypeUpdateImage:
			exportedUpdateUserUpdateImages = append(
				exportedUpdateUserUpdateImages,
				UpdateUserUpdateImage{
					ID:        uu.ID,
					User:      uu.Target,
					Timestamp: uu.Timestamp,
				},
			)
		case updateUserTypeAddEncryptedDevice:
			// Encrypted devices do not create status changes
		case updateUserTypeRemoveEncryptedDevice:
			// Encrypted devices do not create status changes
		case updateUserTypeSetEncryptedDeviceName:
			// Setting encrypted device name doesn't create status change
		case updateUserTypeReplaceKeys:
			// Changing keys does not create a status change
		case updateUserTypeReplaceECDHPublicKey:
			// Changing keys does not create a status change
		default:
			log.WithFields(log.Fields{
				"id":      uu.ID,
				"type":    uu.Type,
				"user_id": uu.Target,
			}).Warn("unsupported update user type")
		}
	}

	fileProgress := []FileProgress{}
	var pendingFiles []file
	err = b.database.Where("downloaded = ? AND wanted = ? AND size > ?", false, true, EmbeddedFileLimit).Find(&pendingFiles).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up files in progress")
	}
	for _, f := range pendingFiles {
		downloadedSize := int64(0)

		var chunks []chunk
		err = b.database.Where("file_id = ?", f.ID).Find(&chunks).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up chunks")
		}

		for _, c := range chunks {
			if c.Downloaded {
				if c.Index == len(chunks)-1 {
					// The last chunk might be smaller than the chunk size
					downloadedSize += int64(f.Size % int64(f.ChunkSize))
				} else {
					downloadedSize += int64(f.ChunkSize)
				}
			}
		}

		fileDataDownloaded[f.ID] = downloadedSize
		fileProgress = append(fileProgress, FileProgress{ID: f.ID, Progress: float64(downloadedSize) / float64(f.Size)})
	}

	var exportedDrafts []Draft
	var drafts []draft
	err = b.database.Find(&drafts).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error finding drafts")
	}
	latestDrafts := map[uuid.UUID]draft{}
	for _, d := range drafts {
		existing, ok := latestDrafts[d.Thread]
		if !ok || existing.Timestamp < d.Timestamp {
			latestDrafts[d.Thread] = d
		}
	}
	for _, d := range latestDrafts {
		exportedDrafts = append(exportedDrafts, Draft{Thread: d.Thread, Text: d.Text})
		draftMutex.Lock()
		dr := &draft{
			ID:        d.ID,
			Thread:    d.Thread,
			Text:      d.Text,
			Timestamp: d.Timestamp,
			Saved:     d.Saved,
		}
		dr.OriginalPayload = d.OriginalPayload
		dr.Signer = d.Signer
		dr.Signature = d.Signature
		draftCache[d.Thread] = dr
		draftMutex.Unlock()
	}

	// Create the initial state for the UI
	return InitialState{
		Profile:                                profile,
		Settings:                               settings,
		SyncDevices:                            syncDevices,
		Users:                                  chatUsers,
		Groups:                                 chatGroups,
		DirectMessages:                         exportedDMs,
		UpdateDMRetentions:                     exportedUpdateDMRetentions,
		UpdateDMClearHistories:                 exportedUpdateDMClearHistories,
		GroupMessages:                          exportedGMs,
		UpdateGroupRetentions:                  exportedUpdateGroupRetentions,
		UpdateGroupNames:                       exportedUpdateGroupNames,
		UpdateGroupInvitedUsers:                exportedUpdateGroupInvitedUsers,
		UpdateGroupRemoveUsers:                 exportedUpdateGroupRemoveUsers,
		UpdateGroupClearHistories:              exportedUpdateGroupClearHistories,
		UpdateGroupAdminPromotions:             exportedUpdateGroupAdminPromotions,
		UpdateGroupAdminDemotions:              exportedUpdateGroupAdminDemotions,
		UpdateGroupUserManagementsRestricted:   exportedUpdateGroupUserManagementsRestricted,
		UpdateGroupUserManagementsUnrestricted: exportedUpdateGroupUserManagementsUnrestricted,
		UpdateGroupEditsRestricted:             exportedUpdateGroupEditsRestricted,
		UpdateGroupEditsUnrestricted:           exportedUpdateGroupEditsUnrestricted,
		UpdateGroupPostingsRestricted:          exportedUpdateGroupPostingsRestricted,
		UpdateGroupPostingsUnrestricted:        exportedUpdateGroupPostingsUnrestricted,
		UpdateGroupUserBlockedGroups:           exportedUpdateGroupUserBlockedGroups,
		UpdateGroupUserChangedGroupImages:      exportedUpdateGroupUserChangedGroupImages,
		UpdateGroupRevokedInvites:              exportedUpdateGroupInvitesRevoked,
		UpdateGroupAcceptedInvites:             exportedUpdateGroupInvitesAccepted,
		UpdateGroupRejectedInvites:             exportedUpdateGroupInvitesRejected,
		UpdateUserUpdateNames:                  exportedUpdateUserUpdateNames,
		UpdateUserUpdateImages:                 exportedUpdateUserUpdateImages,
		FileProgress:                           fileProgress,
		Drafts:                                 exportedDrafts,
	}
}

// Get all structures that could be threaded in a direct message
func (b *Bounce) GetDMHistory(userID uuid.UUID) InitialState {
	// Load the user
	var chatUsers []User
	var u user
	err := b.database.Where("id = ?", userID).First(&u).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error":   err.Error(),
			"user_id": userID,
		}).Fatal("database error looking up user")
	}
	chatUsers = append(chatUsers, User{
		ID:               u.ID,
		Name:             u.Name,
		Images:           u.images(),
		IntroductionTime: u.IntroductionTime,
		State: DMState{
			Open:                           u.OpenDM,
			Retention:                      u.Retention,
			MutedUntil:                     u.MutedUntil,
			OverrideReadReceiptSetting:     u.ReadReceiptsOverridden,
			ReadReceiptsEnabled:            u.ReadReceiptsEnabled,
			OverrideTypingIndicatorSetting: u.TypingIndicatorsOverridden,
			TypingIndicatorsEnabled:        u.TypingIndicatorsEnabled,
		},
	})

	// Load all direct messages
	dms := []directMessage{}
	err = b.database.Preload(clause.Associations).Order("saved_at asc").Where("xor = ?", xor(b.currentUserID(), userID)).Find(&dms).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up direct messages")
	}

	dmRRs := []readReceipt{}
	dmRRmap := map[uuid.UUID][]ReadReceipt{}
	err = b.database.Where("target_type = ? AND destination = ?", typeDirectMessage, userID).Find(&dmRRs).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error looking up read receipts")
	}
	for _, rr := range dmRRs {
		dmRRmap[rr.Target] = append(
			dmRRmap[rr.Target],
			ReadReceipt{
				ID:     rr.ID,
				Actor:  rr.Actor,
				Target: rr.Target,
			},
		)
	}

	dmDRs := []deliveryRecord{}
	dmDRmap := map[uuid.UUID][]uuid.UUID{}
	err = b.database.Where("frame_type = ?", typeDirectMessage).Find(&dmDRs).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error looking up delivery records")
	}
	for _, dr := range dmDRs {
		dev, ok := b.getDeviceFromAddress(dr.Destination)
		if ok {
			dmDRmap[dr.FrameID] = append(
				dmDRmap[dr.FrameID],
				dev.UserID,
			)
		}
	}

	fileProgress := []FileProgress{}
	exportedDMs := []DirectMessage{}
	for _, dm := range dms {
		readReceipts, ok := dmRRmap[dm.ID]
		if !ok {
			readReceipts = []ReadReceipt{}
		}
		deliveredTo, ok := dmDRmap[dm.ID]
		if !ok {
			deliveredTo = []uuid.UUID{}
			if !dm.Undeliverable {
				go b.checkIfDirectMessageUndeliverableAt(time.Unix(dm.WrittenAt, 0).Add(undeliverableAfter).Unix(), dm.ID)
			}
		}

		uiImageAttachments := []ImageAttachment{}
		for _, ia := range dm.ImageAttachments {
			uiImageAttachments = append(uiImageAttachments, ImageAttachment{
				ID:       ia.FileID,
				Name:     ia.Name,
				Size:     ia.Size,
				Width:    ia.Width,
				Height:   ia.Height,
				BlurHash: ia.BlurHash,
			})
		}
		uiFileAttachments := []FileAttachment{}
		for _, fa := range dm.FileAttachments {
			uiFileAttachments = append(uiFileAttachments, FileAttachment{
				ID:   fa.FileID,
				Name: fa.Name,
				Size: fa.Size,
			})
		}

		var pendingFiles []file
		err = b.database.Where("downloaded = ? AND wanted = ? AND size > ? AND attached_to = ?", false, true, EmbeddedFileLimit, dm.ID).Find(&pendingFiles).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up files in progress")
		}
		for _, f := range pendingFiles {
			downloadedSize := int64(0)

			var chunks []chunk
			err = b.database.Where("file_id = ?", f.ID).Find(&chunks).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error looking up chunks")
			}

			for _, c := range chunks {
				if c.Downloaded {
					if c.Index == len(chunks)-1 {
						// The last chunk might be smaller than the chunk size
						downloadedSize += int64(f.Size % int64(f.ChunkSize))
					} else {
						downloadedSize += int64(f.ChunkSize)
					}
				}
			}

			fileDataDownloaded[f.ID] = downloadedSize
			fileProgress = append(fileProgress, FileProgress{ID: f.ID, Progress: float64(downloadedSize) / float64(f.Size)})
		}

		exportedDMs = append(
			exportedDMs,
			DirectMessage{
				ID:               dm.ID,
				Author:           dm.Author,
				Thread:           dm.getDestination(b.currentUserID()),
				WrittenAt:        dm.WrittenAt,
				SavedAt:          dm.SavedAt,
				ExpiresAt:        dm.DeleteAt,
				Text:             dm.Text,
				ImageAttachments: uiImageAttachments,
				FileAttachments:  uiFileAttachments,
				Seen:             dm.Seen,
				Undeliverable:    dm.Undeliverable,
				ReadReceipts:     readReceipts,
				DeliveredTo:      deliveredTo,
			},
		)
	}

	udms := []updateDM{}
	err = b.database.Order("timestamp asc").Where("target = ?", xor(b.currentUserID(), userID)).Find(&udms).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up all update DMs")
	}
	exportedUpdateDMRetentions := []UpdateDMRetention{}
	exportedUpdateDMClearHistories := []UpdateDMClearHistory{}
	for _, udm := range udms {
		switch udm.Type {
		case updateDMTypeChangeRetention:
			exportedUpdateDMRetentions = append(
				exportedUpdateDMRetentions,
				UpdateDMRetention{
					ID:        udm.ID,
					Thread:    xor(udm.Target, b.currentUserID()),
					Actor:     udm.Actor,
					Timestamp: udm.Timestamp,
					Seen:      udm.Seen,
					Retention: int64(binary.LittleEndian.Uint64(udm.Data)),
				},
			)
		case updateDMTypeSetClearBefore:
			exportedUpdateDMClearHistories = append(
				exportedUpdateDMClearHistories,
				UpdateDMClearHistory{
					ID:        udm.ID,
					Thread:    xor(udm.Target, b.currentUserID()),
					Actor:     udm.Actor,
					Timestamp: udm.Timestamp,
					Seen:      udm.Seen,
					ClearTime: int64(binary.LittleEndian.Uint64(udm.Data)),
				},
			)
		}
	}

	// Load all update users
	uus := []updateUser{}
	err = b.database.Order("timestamp asc").Where("timestamp >= ? AND (target = ? OR target = ?)", u.IntroductionTime, b.currentUserID(), userID).Find(&uus).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up all update users")
	}
	exportedUpdateUserUpdateNames := []UpdateUserUpdateName{}
	exportedUpdateUserUpdateImages := []UpdateUserUpdateImage{}
	for _, uu := range uus {
		switch uu.Type {
		case updateUserTypeUpdateName:
			exportedUpdateUserUpdateNames = append(
				exportedUpdateUserUpdateNames,
				UpdateUserUpdateName{
					ID:        uu.ID,
					User:      uu.Target,
					Name:      string(uu.Data),
					OldName:   string(uu.PreviousData),
					Timestamp: uu.Timestamp,
				},
			)
		case updateUserTypeUpdateImage:
			exportedUpdateUserUpdateImages = append(
				exportedUpdateUserUpdateImages,
				UpdateUserUpdateImage{
					ID:        uu.ID,
					User:      uu.Target,
					Timestamp: uu.Timestamp,
				},
			)
		case updateUserTypeAddEncryptedDevice:
			// Encrypted devices do not create status changes
		case updateUserTypeRemoveEncryptedDevice:
			// Encrypted devices do not create status changes
		case updateUserTypeSetEncryptedDeviceName:
			// Setting encrypted device name doesn't create state change
		case updateUserTypeReplaceKeys:
			// Changing keys does not create a status change
		case updateUserTypeReplaceECDHPublicKey:
			// Changing keys does not create a status change
		default:
			log.WithFields(log.Fields{
				"id":      uu.ID,
				"type":    uu.Type,
				"user_id": uu.Target,
			}).Warn("unsupported update user type")
		}
	}

	var exportedDrafts []Draft
	var d draft
	err = b.database.Where("thread = ?", userID).Order("timestamp asc").Limit(1).First(&d).Error
	if err == nil {
		exportedDrafts = append(exportedDrafts, Draft{Thread: userID, Text: d.Text})
	}

	// Create the initial state for the UI
	return InitialState{
		Users:                  chatUsers,
		DirectMessages:         exportedDMs,
		UpdateDMRetentions:     exportedUpdateDMRetentions,
		UpdateDMClearHistories: exportedUpdateDMClearHistories,
		UpdateUserUpdateNames:  exportedUpdateUserUpdateNames,
		UpdateUserUpdateImages: exportedUpdateUserUpdateImages,
		FileProgress:           fileProgress,
		Drafts:                 exportedDrafts,
	}
}
