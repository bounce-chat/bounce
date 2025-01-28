package chat

import (
	"github.com/google/uuid"
)

type profileSettings struct {
	ID                             uuid.UUID `gorm:"type:uuid;primary_key;"`
	UserID                         uuid.UUID
	BlockedGroups                  string
	DefaultGroupRetention          int64
	DefaultSendReadReceipts        bool
	DefaultSendTypingIndicators    bool
	NewGroupRestrictUserManagement bool
	NewGroupRestrictGroupEdits     bool
	NewGroupRestrictPosting        bool
}
