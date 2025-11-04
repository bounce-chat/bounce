package chat

import (
	"github.com/google/uuid"
)

const OnlyAutoJoinGroupsWithNoNewUsers = 0
const NeverAutoJoinGroups = 1
const AlwaysAutoJoinGroups = 2

type profileSettings struct {
	ID                             uuid.UUID `gorm:"type:uuid;primary_key;" msgpack:"-"`
	UserID                         uuid.UUID
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

func (b *Bounce) lastAutoJoinGroupSettingChange() int64 {
	var us updateSettings
	err := b.database.Select("timestamp", "MAX(timestamp)").Find(&us).Error
	if err != nil {
		return 0
	}
	return us.Timestamp
}
