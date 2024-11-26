package chat

import (
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

type profileSettings struct {
	ID                              uuid.UUID `gorm:"type:uuid;primary_key;"`
	UserID                          uuid.UUID
	NetworkPrivateKey               []byte
	NetworkPublicKey                []byte
	BlockedGroups                   string
	DefaultGroupRetention           int64
	DefaultSendReadReceipts         bool
	DefaultSendTypingIndicators     bool
	NeverAskForBatteryOptimizations bool
	NewGroupRestrictUserManagement  bool
	NewGroupRestrictGroupEdits      bool
	NewGroupRestrictPosting         bool
}

func (b *bounce) neverAskForBatteryOptimizations() {
	err := b.database.Table("profile_settings").Where("user_id = ?", b.currentUserID()).Update("never_ask_for_battery_optimizations", true).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error permanently dismissing battery optimization check")
	}
}

func (b *bounce) setReadReceiptsByDefault(value bool) {
	err := b.database.Table("profile_settings").Where("user_id = ?", b.currentUserID()).Update("default_send_read_receipts", value).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error setting default send read receipts")
	} else {
		b.userInterface.DefaultReadReceiptSettingSet(value)
	}
}

func (b *bounce) setTypingIndicatorsByDefault(value bool) {
	err := b.database.Table("profile_settings").Where("user_id = ?", b.currentUserID()).Update("default_send_typing_indicators", value).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error setting default send typing indicators")
	} else {
		b.userInterface.DefaultTypingIndicatorSettingSet(value)
	}
}

func (b *bounce) setNewGroupRetention(value int64) {
	err := b.database.Table("profile_settings").Where("user_id = ?", b.currentUserID()).Update("default_group_retention", value).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error setting default group retention")
	} else {
		b.userInterface.DefaultGroupRetentionSettingSet(value)
	}
}

func (b *bounce) setNewGroupRestrictUserManagement(value bool) {
	err := b.database.Table("profile_settings").Where("user_id = ?", b.currentUserID()).Update("new_group_restrict_user_management", value).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error setting new group restrict user management")
	} else {
		b.userInterface.NewGroupRestrictUserManagementSettingSet(value)
	}
}

func (b *bounce) setNewGroupRestrictGroupEdits(value bool) {
	err := b.database.Table("profile_settings").Where("user_id = ?", b.currentUserID()).Update("new_group_restrict_group_edits", value).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error setting new group restrict group edits")
	} else {
		b.userInterface.NewGroupRestrictGroupEditsSettingSet(value)
	}
}

func (b *bounce) setNewGroupRestrictPosting(value bool) {
	err := b.database.Table("profile_settings").Where("user_id = ?", b.currentUserID()).Update("new_group_restrict_posting", value).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error setting new group restrict posting")
	} else {
		b.userInterface.NewGroupRestrictPostingSettingSet(value)
	}
}
