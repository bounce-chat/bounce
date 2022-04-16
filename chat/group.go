package chat

import (
	"errors"
	"sync"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var groupMutex sync.Mutex

type group struct {
	ID           uuid.UUID
	Name         string
	Image        []byte
	Retention    int64
	Users        []user `gorm:"many2many:group_users;"` // TODO: pointer needed?  I don't think so
	payload      []byte
	payloadMutex sync.Mutex
}

func (g *group) BeforeCreate(tx *gorm.DB) error {
	//if g.ID == uuid.Nil {
	//	log.Fatal("attempt to create group with nil ID, group ID must be set before creation")
	//}

	return nil
}

func (g *group) getID() uuid.UUID {
	return g.ID
}

func (g *group) getScope(_ uuid.UUID) int {
	return scopeGroup
}

func (g *group) getDestination(_ uuid.UUID) uuid.UUID {
	return g.ID
}

func (g *group) getType() uint16 {
	return typeGroup
}

func (g *group) getPayload() []byte {
	g.payloadMutex.Lock()
	defer g.payloadMutex.Unlock()

	if len(g.payload) == 0 {
		bytes, err := msgpack.Marshal(g)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal group")
		}
		g.payload = bytes
	}
	return g.payload
}

func (g *group) getTimestamp() int64 {
	return 0
}

func (b *bounce) handleGroup(peer string, payload []byte) {
	groupMutex.Lock()
	defer groupMutex.Unlock()

	// Look up the device that sent this group
	srcDevice, exists := b.getDeviceFromAddress(peer)
	if !exists {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("ignoring group sent from an unknown device")
		return
	}

	// Unmarshall the group
	var g group
	err := msgpack.Unmarshal(payload, &g)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling group")
		return
	}

	// If we already know about this group, ack it and return
	var existingGroup group
	err = b.database.Where("id = ?", g.ID).First(&existingGroup).Error // TODO: use count or only select ID or something?
	if err == nil {
		// We already know about this group, just mark it as delivered to the peer
		b.markDeliveredTo(&g, peer)
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		// This is a new group and we want to save it
		// Check that each user has a valid device group
		for _, u := range g.Users {
			if !b.hasValidDeviceGroup(u) {
				log.WithFields(log.Fields{
					"peer":    peer,
					"user_id": u.ID,
				}).Warn("ignoring group that contains user with invalid device group")
				return
			}
		}

		// Save all of the structures in this group, creating any new users or devices as needed
		err = b.database.Transaction(func(tx *gorm.DB) error {
			for _, u := range g.Users {
				for _, dev := range u.Devices {
					err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&dev).Error
					if err != nil {
						log.WithFields(log.Fields{
							"error": err.Error(),
						}).Error("error saving device that is part of a group")
						return err
					}
				}
				err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&u).Error
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error saving user that is part of a group")
					return err
				}
			}
			err := tx.Create(&g).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error saving new group")
				return err
			}

			return nil
		})
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error in transaction for saving a new group")
		}

		// TODO: notify the device pool to connect right away.  groupConnectionDesired()?

		go b.broadcast(&g)

		//b.userInterface.NewGroup()

	} else {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error looking up group")
	}

	// Ack the group
	go b.broadcast(&ack{
		destination: srcDevice.ID,
		Groups:      g.ID.String(),
	})
}

func (b *bounce) CreateGroup(users []uuid.UUID) {

}
