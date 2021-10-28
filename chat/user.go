package chat

import (
	"errors"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type user struct {
	ID               uuid.UUID `gorm:"type:uuid;primary_key;"`
	Name             string
	Profile          bool `json:"-"`
	MessageRetention int64
	Devices          []device
}

func (u *user) BeforeCreate(tx *gorm.DB) error {
	if u.Profile {
		var count int64
		tx.Model(&user{}).Where("profile = ?", true).Count(&count)
		if count > 0 {
			return errors.New("profile user already exists")
		}
	}
	return nil
}

func (bounce *Bounce) currentUser() (user, bool) {
	var currentUser user
	err := bounce.database.Preload(clause.Associations).Where("profile = ?", true).First(&currentUser).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return currentUser, false
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error loading current user")
		}
	}
	return currentUser, true
}

func (bounce *Bounce) currentUserID() uuid.UUID {
	if bounce.userID == uuid.Nil {
		currentUser, ok := bounce.currentUser()
		if !ok {
			log.Fatal("a current user must exist before currentUserID can be called")
		}
		bounce.userID = currentUser.ID
	}
	return bounce.userID
}
