package chat

import (
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

// Local settings are setting that apply only to this device and are not replicated on other sync devices
type localSettings struct {
	ID                              uuid.UUID `gorm:"type:uuid;primary_key;"`
	UserID                          uuid.UUID
	NetworkPrivateKey               []byte
	NetworkPublicKey                []byte
	DarkMode                        bool
	NeverAskForBatteryOptimizations bool
}

func (b *bounce) neverAskForBatteryOptimizations() {
	err := b.database.Table("local_settings").Where("user_id = ?", b.currentUserID()).Update("never_ask_for_battery_optimizations", true).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error permanently dismissing battery optimization check")
	}
}

func (b *bounce) setDarkMode(value bool) {
	err := b.database.Table("local_settings").Where("user_id = ?", b.currentUserID()).Update("dark_mode", value).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error setting dark mode in local settings")
	}

	b.userInterface.SetDarkMode(value)
}

func (b *bounce) darkMode() bool {
	currentUser, ok := b.currentUser()
	if !ok {
		return true
	}
	var ls localSettings
	err := b.database.First(&ls, "user_id = ?", currentUser.ID).Error
	if err != nil {
		return true
	}
	return ls.DarkMode
}
