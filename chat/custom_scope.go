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

func (b *Bounce) createCustomScopeFromGroup(groupID uuid.UUID) error {
	var gc groupCreation
	err := b.database.Where("id = ?", groupID).First(&gc).Error
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

	addresses := b.getGroupWithInvitesScope(&gc, false)
	if len(addresses) == 0 {
		log.WithFields(log.Fields{
			"group_id": groupID,
		}).Error("cannot create custom scope for group that has no addresses")
		return errors.New("no addresses in scope")
	}

	cs := &customScope{
		ID:        groupID,
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

func (b *Bounce) createCustomScopeForSync() uuid.UUID {
	addresses := b.getSyncScope(nil, false)

	cs := &customScope{
		ID:        uuid.New(),
		Addresses: strings.Join(addresses, ","),
	}

	err := b.database.Create(cs).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error creating custom scope")
	}

	return cs.ID
}

// This function is called when we've been re-added to a group we were previously removed from and we want to
// undo the custom scoping of past frames
func (b *Bounce) rescopeIfReAddedToGroup(groupID uuid.UUID) error {
	// Remove custom scopes from updateGroups for this group
	err := b.database.Model(&updateGroup{}).Where("custom_scope = ?", groupID).Updates(map[string]interface{}{"custom_scope": uuid.Nil, "applied": false}).Error
	if err != nil {
		return err
	}

	// Remove custom scopes from confirmations for this group
	err = b.database.Model(&confirmation{}).Where("custom_scope = ?", groupID).Updates(map[string]interface{}{"custom_scope": uuid.Nil}).Error
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
