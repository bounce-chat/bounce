package chat

import (
	"errors"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"github.com/zeebo/blake3"
	"gorm.io/gorm"
)

var groupMutex sync.Mutex

type group struct {
	ID                     uuid.UUID `gorm:"type:uuid;primary_key;"`
	Name                   string
	Image                  []byte
	CreatedBy              uuid.UUID
	CreatedAt              int64
	Retention              int64
	ClearBefore            int64
	MutedUntil             int64
	Users                  []user `gorm:"many2many:group_users;"` // TODO: pointer needed?  I don't think so
	Admins                 string
	RestrictUserManagement bool
	RestrictGroupEdits     bool
	payload                []byte
	payloadMutex           sync.Mutex
}

func (g *group) BeforeCreate(tx *gorm.DB) error {
	if g.ID == uuid.Nil {
		log.Fatal("attempt to create group with nil ID, group ID must be set before creation")
	}

	return nil
}

// TODO: after delete, cascade delete of all group updates and messages
// could also use db.Select(clause.Associations).Delete(&group)
// https://gorm.io/docs/associations.html#Delete-with-Select

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

	creationTime := time.Now().Unix()
	g := group{
		ID:        uuid.Nil,
		Name:      name,
		CreatedAt: creationTime,
		Retention: 60 * 60 * 24 * 7, // TODO: have the default be a user setting
		Users:     users,
	}

	groupData, err := msgpack.Marshal(g)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal group")
	}

	// Create a hash of the group without an ID to use as the group creation ID,
	// and the group ID upon handling, to ensure that the original group state
	// is not modified during replication
	hasher := blake3.New()
	written, _ := hasher.Write(groupData)
	if written != len(groupData) {
		log.WithFields(log.Fields{
			"length":  len(groupData),
			"written": written,
		}).Fatal("failed to write all group data into hasher")
	}
	digest := hasher.Digest()
	groupHash := make([]byte, 16)
	digest.Read(groupHash)
	groupID, err := uuid.FromBytes(groupHash)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot create UUID from hash of group data")
	}

	gc := groupCreation{
		ID:        groupID,
		Timestamp: creationTime,
		Data:      groupData,
	}
	gc.Payload, err = msgpack.Marshal(gc)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling group creation")
	}
	sc := b.createSignedContainer(gc.Payload)
	gc.Signature = sc.Signature
	gc.Signer = sc.Signer

	err = b.database.Transaction(func(tx *gorm.DB) error {
		err = tx.Omit("Users.*").Create(&g).Error
		if err != nil {
			return err
		}
		err = tx.Create(&gc).Error
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving new group and group creation")
	}

	go b.broadcast(&gc)

	b.userInterface.OpenNewGroupChat(Group{
		ID:      g.ID,
		Name:    g.Name,
		UserIDs: userIDs,
	})

	return nil
}

func (b *bounce) getGroupRetention(groupID uuid.UUID) int64 {
	var g group
	err := b.database.Select("retention").First(&g, "id = ?", groupID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"id":    groupID,
				"error": err.Error(),
			}).Error("error selecting message retention for unknown group")
			// TODO: return error?
			return 0
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error selecting message retention from group")
		}
	}
	return g.Retention
}

func (b *bounce) getGroupMutedUntil(groupID uuid.UUID) (int64, error) {
	var g group
	err := b.database.Select("muted_until").First(&g, "id = ?", groupID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"id":    groupID,
				"error": err.Error(),
			}).Error("error selecting muted until for unknown group")
			return 0, err
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error selecting muted until from group")
		}
	}
	return g.MutedUntil, nil
}

func (b *bounce) userIsInGroup(userID, groupID uuid.UUID) bool {
	var exists bool
	err := b.database.Table("group_users").
		Select("count(*) = 1").
		Where("user_id = ? AND group_id = ?", userID, groupID).
		Find(&exists).
		Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error checking if user is in group")
	}
	return exists
}

func (b *bounce) validGroupName(name string) bool {
	return utf8.ValidString(name) && utf8.RuneCountInString(name) <= 512
}

func (b *bounce) groupMessageWrittenBeforeHistoryCleared(groupID uuid.UUID, messageWrittenAt int64) bool {
	var g group
	err := b.database.Select("clear_before").First(&g, "id = ?", groupID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"id":    groupID,
				"error": err.Error(),
			}).Error("error selecting clear before for unknown group")
			return false
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error selecting clear before from group")
		}
	}

	return messageWrittenAt < g.ClearBefore
}
