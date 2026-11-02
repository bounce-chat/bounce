package chat

import (
	"time"
)

const OnlyAutoJoinGroupsWithNoNewUsers = 0
const NeverAutoJoinGroups = 1
const AlwaysAutoJoinGroups = 2

type profileSettings struct {
	ID                             ID `gorm:"primary_key;" msgpack:"-"`
	UserID                         ID
	BlockedGroups                  string
	DefaultGroupRetention          int64
	DefaultSendReadReceipts        bool
	DefaultSendTypingIndicators    bool
	NewGroupRestrictUserManagement bool
	NewGroupRestrictGroupEdits     bool
	NewGroupRestrictPosting        bool
	AutoJoinGroups                 int
	DefaultDMRetention             int64
}

func defaultProfileSettings() *profileSettings {
	return &profileSettings{
		DefaultGroupRetention:          int64(time.Duration(24 * time.Hour * 7 * 4).Seconds()),
		DefaultSendReadReceipts:        true,
		DefaultSendTypingIndicators:    true,
		NewGroupRestrictUserManagement: true,
		NewGroupRestrictGroupEdits:     false,
		NewGroupRestrictPosting:        false,
		DefaultDMRetention:             int64(time.Duration(24 * time.Hour * 7 * 4).Seconds()),
	}
}

func (b *Bounce) lastAutoJoinGroupSettingChange() int64 {
	var us updateSettings
	err := b.database.Select("timestamp", "MAX(timestamp)").Find(&us).Error
	if err != nil {
		return 0
	}
	return us.Timestamp
}
