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
	DefaultSendReadReceipts         bool `gorm:"default:true"`
	DefaultSendTypingIndicators     bool `gorm:"default:true"`
	NeverAskForBatteryOptimizations bool
}

func (b *bounce) neverAskForBatteryOptimizations() {
	err := b.database.Table("profile_settings").Where("user_id = ?", b.currentUserID()).Update("never_ask_for_battery_optimizations", true).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error permanently dismissing battery optimization check")
	}
}
