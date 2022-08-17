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
	ID                     uuid.UUID `gorm:"type:uuid;primary_key;"`
	Name                   string
	Image                  []byte
	CreatedBy              uuid.UUID
	Retention              int64
	Users                  []user `gorm:"many2many:group_users;"` // TODO: pointer needed?  I don't think so
	Admins                 string
	RestrictUserManagement bool
	RestrictGroupEdits     bool
	payload                []byte
	payloadMutex           sync.Mutex
}

func (g *group) BeforeCreate(tx *gorm.DB) error {
	//if g.ID == uuid.Nil {
	//	log.Fatal("attempt to create group with nil ID, group ID must be set before creation")
	//}

	return nil
}

// TODO: after delete, cascade delete of all group updates and messages

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
		userIDs := []uuid.UUID{}
		err = b.database.Transaction(func(tx *gorm.DB) error {
			for _, u := range g.Users {
				// TODO: if the users are new, shouldn't we ack them as well?
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
				userIDs = append(userIDs, u.ID)
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

		b.userInterface.NewGroupChat(Group{
			ID:      g.ID,
			Name:    g.Name,
			UserIDs: userIDs,
		})

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

func (b *bounce) createGroup(name string, userIDs []uuid.UUID) error {
	if name == "" {
		return errors.New("group must be named")
	}

	users := []user{}
	userListContainsProfile := false
	for _, userID := range userIDs {
		var u user
		err := b.database.Where("id = ?", userID).First(&u).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"user_id": userID,
				}).Error("attempt to create a group with user ID that doesn't exist in the database")
				return errors.New("cannot create group with unknown user")
			} else {
				log.WithFields(log.Fields{
					"user_id": userID,
					"error":   err.Error(),
				}).Fatal("error looking up user")
			}
		}
		users = append(users, u)
		if u.ID == b.currentUserID() {
			userListContainsProfile = true
		}
	}
	if !userListContainsProfile {
		profile, exists := b.currentUser()
		if !exists {
			log.Fatal("cannot create new group when no profile exists")
		}
		users = append(users, profile)
		userIDs = append(userIDs, profile.ID)
	}

	g := group{
		ID:        uuid.New(),
		Name:      name,
		Retention: 60 * 60 * 24 * 7, // TODO: have the default be a user setting
		Users:     users,
	}
	err := b.database.Omit("Users.*").Create(&g).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving new group")
	}

	go b.broadcast(&g)

	b.userInterface.OpenNewGroupChat(Group{
		ID:      g.ID,
		Name:    g.Name,
		UserIDs: userIDs,
	})

	return nil
}
