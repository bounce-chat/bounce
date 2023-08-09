package chat

import (
	"errors"
	"strings"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type customScope struct {
	ID        uuid.UUID
	Addresses string
}

//TODO: before create

func (cs *customScope) addresses() []string {
	// TODO: split
	return []string{}
}

func (b *bounce) createCustomScopeFromGroup(groupID uuid.UUID) (uuid.UUID, error) {
	var g group
	err := b.database.Preload("Devices").Preload(clause.Associations).Where("id = ?", groupID).First(&g).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"group_id": groupID,
			}).Error("cannot create custom scope for unknown group")
			return uuid.Nil, err
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
		ID:        uuid.New(),
		Addresses: strings.Join(addresses, ","),
	}

	err = b.database.Create(cs).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error creating custom scope")
	}

	return cs.ID, nil
}
