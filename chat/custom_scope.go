package chat

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type customScope struct {
	ID        uuid.UUID `gorm:"type:uuid;primary_key;"`
	CreatedAt int64
	Addresses string `gorm:"not null"` // not associating with the actual devices in case we want to delete them
}

func (cs *customScope) BeforeCreate(tx *gorm.DB) error {
	cs.CreatedAt = time.Now().Unix()
	if cs.ID == uuid.Nil {
		return errors.New("custom scope must have an ID assigned before creation")
	}
	return nil
}

func (cs *customScope) addresses() []string {
	if len(cs.Addresses) == 0 {
		log.WithFields(log.Fields{
			"id": cs.ID,
		}).Error("custom scope has no addresses")
		return []string{}
	}

	return strings.Split(cs.Addresses, ",")
}

func (b *bounce) createCustomScopeFromGroup(groupID uuid.UUID) error {
	var g group
	err := b.database.Preload("Users.Devices").Preload(clause.Associations).Where("id = ?", groupID).First(&g).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"group_id": groupID,
			}).Error("cannot create custom scope for unknown group")
			return err
		} else {
			log.WithFields(log.Fields{
				"group_id": groupID,
				"error":    err.Error(),
			}).Fatal("database error looking up group")
		}
	}

	addresses := []string{}
	for _, u := range g.Users {
		for _, dev := range u.Devices {
			addresses = append(addresses, dev.Address)
		}
	}

	cs := &customScope{
		ID:        g.ID,
		Addresses: strings.Join(addresses, ","),
	}

	err = b.database.Clauses(clause.OnConflict{DoNothing: true}).Create(cs).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error creating custom scope")
	}

	return nil
}

//
// CHeck if any of the frames that use custom scoping are depending on this custom scope, and if there's only
// one or zero frames using it, delete it
//
func deleteCustomScopeIfLastUse(tx *gorm.DB, id uuid.UUID) error {
	var ugCount int64
	err := tx.Model(&updateGroup{}).Where("custom_scope = ?", id).Count(&ugCount).Error
	if err != nil {
		return err
	}

	if ugCount <= 1 {
		err := tx.Where("id = ?", id).Delete(&customScope{}).Error
		if err != nil {
			return err
		}
	}

	return nil
}

//
// This function is called when we've been re-added to a group we were previously removed from and we want to
// undo the custom scoping of past frames
//
func (b *bounce) rescopeIfReAddedToGroup(groupID uuid.UUID) error {
	// Remove custom scopes from updateGroups for this group
	err := b.database.Model(&updateGroup{}).Where("custom_scope = ?", groupID).Update("custom_scope", uuid.Nil.String()).Error
	if err != nil {
		return err
	}

	// Delete the custom scope
	err = b.database.Where("id = ?", groupID).Delete(&customScope{}).Error
	if err != nil {
		return err
	}

	return nil
}
