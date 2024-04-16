package chat

import "github.com/google/uuid"

type profileSettings struct {
	ID                    uuid.UUID `gorm:"type:uuid;primary_key;"`
	UserID                uuid.UUID
	NetworkPrivateKey     []byte
	NetworkPublicKey      []byte
	BlockedGroups         string
	DefaultGroupRetention int64
}
