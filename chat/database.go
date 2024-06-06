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

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
)

func (b *bounce) openDatabase() {
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
		&profileExport{},
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
	)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error migrating the database")
	}

	// Prune the database
	b.pruneDirectMessages()
	b.pruneGroupMessages()
	b.pruneUndeliverableCustomScopes()
}

func (b *bounce) pruneDirectMessages() {
	// Delete messages from the database that are past expiration
	err := b.database.Where("delete_at != 0 AND delete_at < ?", time.Now().Unix()).Delete(&directMessage{}).Error
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
		Select("direct_messages.id", "direct_messages.retention_seconds").
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

		// Undeliverable messages should be deleted if there's a retention setting on the message.  Since these messages were never ack'd, the delete_at
		// field was never set and therefore they weren't pruned above, so we delete them based on how long their retention settings are
		if dm.RetentionSeconds > 0 {
			if dm.RetentionSeconds > int64(undeliverableAfter.Seconds()) {
				// The message retention is longer than the undeliverable window, keep this message around for the difference
				deleteAt := time.Now().Unix() + dm.RetentionSeconds - int64(undeliverableAfter.Seconds())
				err = b.database.Model(&dm).Update("delete_at", deleteAt).Error
				if err != nil {
					log.WithFields(log.Fields{
						"message_id": dm.ID,
						"error":      err.Error(),
					}).Fatal("error updating delete_at of undeliverable direct message with retention")
				}
				go b.deleteDirectMessageAt(deleteAt, dm.ID)
			} else {
				// The message retention is shorter than the undeliverable window, so we delete it now
				err = b.database.Where("id = ?", dm.ID).Delete(&directMessage{}).Error
				if err != nil {
					log.WithFields(log.Fields{
						"message_id": dm.ID,
						"error":      err.Error(),
					}).Fatal("error deleting undeliverable direct message with retention")
				}
			}
		}
	}

	// Find all messages that have not been delivered and kick off a goroutine to check if they should be marked undeliverable later
	var deliverableUndeliveredDMs []directMessage
	err = b.database.
		Select("direct_messages.id", "direct_messages.written_at").
		Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == direct_messages.id AND delivery_records.frame_type == ?", typeDirectMessage).
		Where("delivery_records.id IS NULL AND undeliverable = false").
		Find(&deliverableUndeliveredDMs).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error selecting message that are deliverable while pruning database")
	}
	for _, dm := range deliverableUndeliveredDMs {
		go b.checkIfDirectMessageUndeliverableAt(time.Unix(dm.WrittenAt, 0).Add(undeliverableAfter).Unix(), dm.ID)
	}
}

func (b *bounce) pruneGroupMessages() {
	// Delete messages from the database that are past expiration
	err := b.database.Where("delete_at != 0 AND delete_at < ?", time.Now().Unix()).Delete(&groupMessage{}).Error
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
		Select("group_messages.id", "group_messages.retention_seconds").
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

		// Undeliverable messages should be deleted if there's a retention setting on the message.  Since these messages were never ack'd, the delete_at
		// field was never set and therefore they weren't pruned above, so we delete them based on how long their retention settings are
		if gm.RetentionSeconds > 0 {
			if gm.RetentionSeconds > int64(undeliverableAfter.Seconds()) {
				// The message retention is longer than the undeliverable window, keep this message around for the difference
				deleteAt := time.Now().Unix() + gm.RetentionSeconds - int64(undeliverableAfter.Seconds())
				err = b.database.Model(&gm).Update("delete_at", deleteAt).Error
				if err != nil {
					log.WithFields(log.Fields{
						"message_id": gm.ID,
						"error":      err.Error(),
					}).Fatal("error updating delete_at of undeliverable group message with retention")
				}
				go b.deleteGroupMessageAt(deleteAt, gm.ID)
			} else {
				// The message retention is shorter than the undeliverable window, so we delete it now
				err = b.database.Where("id = ?", gm.ID).Delete(&groupMessage{}).Error
				if err != nil {
					log.WithFields(log.Fields{
						"message_id": gm.ID,
						"error":      err.Error(),
					}).Fatal("error deleting undeliverable group message with retention")
				}
			}
		}
	}

	// Find all messages that have not been delivered and kick off a goroutine to check if they should be marked undeliverable later
	var deliverableUndeliveredGMs []groupMessage
	err = b.database.
		Select("group_messages.id", "group_messages.written_at").
		Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == group_messages.id AND delivery_records.frame_type == ?", typeGroupMessage).
		Where("delivery_records.id IS NULL AND undeliverable = false").
		Find(&deliverableUndeliveredGMs).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error selecting message that are deliverable while pruning database")
	}
	for _, gm := range deliverableUndeliveredGMs {
		go b.checkIfGroupMessageUndeliverableAt(time.Unix(gm.WrittenAt, 0).Add(undeliverableAfter).Unix(), gm.ID)
	}
}

func (b *bounce) pruneUndeliverableCustomScopes() {
	deleteBefore := time.Now().Unix() - int64(undeliverableAfter.Seconds())

	// Prune update groups
	err := b.database.Where("custom_scope != ? AND timestamp < ?", uuid.Nil, deleteBefore).Delete(&updateGroup{}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error pruning update groups")
	}
}

func (b *bounce) buildInitialState() InitialState {
	// Load the profile
	var profile *User
	var dbProfile user
	err := b.database.Where("profile = ?", true).First(&dbProfile).Error
	if err == nil {
		profile = &User{
			ID:   dbProfile.ID,
			Name: dbProfile.Name,
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
			ID:   u.ID,
			Name: u.Name,
			State: DMState{
				Retention:  u.Retention,
				MutedUntil: u.MutedUntil,
			},
		})
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
		userList := []User{}
		for _, u := range g.Users {
			userList = append(userList, User{ID: u.ID, Name: u.Name})
		}
		adminList := []uuid.UUID{}
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
			ID:                     g.ID,
			Name:                   g.Name,
			Users:                  userList,
			Admins:                 adminList,
			BlockedUsers:           blockedList,
			CreatedBy:              g.CreatedBy,
			CreatedAt:              g.CreatedAt,
			Retention:              g.Retention,
			LastActivity:           g.LastActivity,
			RestrictUserManagement: g.RestrictUserManagement,
			RestrictGroupEdits:     g.RestrictGroupEdits,
			RestrictPosting:        g.RestrictPosting,
		})
	}

	// Load all direct messages
	dms := []directMessage{}
	err = b.database.Order("saved_at asc").Find(&dms).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up all direct messages")
	}
	exportedDMs := []DirectMessage{}
	for _, dm := range dms {
		exportedDMs = append(
			exportedDMs,
			DirectMessage{
				ID:            dm.ID,
				Author:        dm.Author,
				Thread:        dm.getDestination(b.currentUserID()),
				WrittenAt:     dm.WrittenAt,
				SavedAt:       dm.SavedAt,
				Text:          dm.Text,
				Expires:       dm.DeleteAt,
				Read:          true, // TODO
				Undeliverable: dm.Undeliverable,
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
					ClearTime: int64(binary.LittleEndian.Uint64(udm.Data)),
				},
			)
		}
	}

	// Load all group messages
	gms := []groupMessage{}
	err = b.database.Order("saved_at asc").Find(&gms).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up all group messages")
	}
	exportedGMs := []GroupMessage{}
	for _, gm := range gms {
		exportedGMs = append(
			exportedGMs,
			GroupMessage{
				ID:            gm.ID,
				Author:        gm.Author,
				Thread:        gm.getDestination(b.currentUserID()),
				WrittenAt:     gm.WrittenAt,
				SavedAt:       gm.SavedAt,
				Text:          gm.Text,
				Expires:       gm.DeleteAt,
				Read:          true, // TODO
				Undeliverable: gm.Undeliverable,
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
	exportedUpdateGroupAddUsers := []UpdateGroupAddUser{}
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
					Retention: int64(binary.LittleEndian.Uint64(ug.Data)),
				},
			)
		case updateGroupTypeAddUser:
			var u user
			err := msgpack.Unmarshal(ug.Data, &u)
			if err != nil {
				log.WithFields(log.Fields{
					"id":    ug.ID,
					"error": err.Error(),
				}).Fatal("error unmarshalling user in update group add user in database")
			}

			exportedUpdateGroupAddUsers = append(
				exportedUpdateGroupAddUsers,
				UpdateGroupAddUser{
					ID:        ug.ID,
					Thread:    ug.Target,
					Actor:     ug.Actor,
					Timestamp: ug.Timestamp,
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
					},
				)
			}
		}
	}

	// Load all update groups
	uus := []updateUser{}
	err = b.database.Order("timestamp asc").Find(&uus).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up all update users")
	}
	exportedUpdateUserUpdateNames := []UpdateUserUpdateName{}
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
		default:
			log.WithFields(log.Fields{
				"id":      uu.ID,
				"type":    uu.Type,
				"user_id": uu.Target,
			}).Warn("unsupported update user type")
		}
	}

	// Create the initial state for the UI
	return InitialState{
		Profile:                                profile,
		Users:                                  chatUsers,
		Groups:                                 chatGroups,
		DirectMessages:                         exportedDMs,
		UpdateDMRetentions:                     exportedUpdateDMRetentions,
		UpdateDMClearHistories:                 exportedUpdateDMClearHistories,
		GroupMessages:                          exportedGMs,
		UpdateGroupRetentions:                  exportedUpdateGroupRetentions,
		UpdateGroupNames:                       exportedUpdateGroupNames,
		UpdateGroupAddUsers:                    exportedUpdateGroupAddUsers,
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
		UpdateUserUpdateNames:                  exportedUpdateUserUpdateNames,
	}
}
