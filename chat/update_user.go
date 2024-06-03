package chat

import (
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
)

var updateUserMutex sync.Mutex

var updateUserTypeUpdateName = uint16(0)

var errInvalidUserName = errors.New("invalid name")
var errUnsupportedUpdateUserType = errors.New("unsupported update user type")

type updateUser struct {
	ID              uuid.UUID `gorm:"type:uuid;primary_key;"`
	Target          uuid.UUID
	Type            uint16
	Data            []byte
	Timestamp       int64
	Signer          string `msgpack:"-" gorm:"not null"`
	OriginalPayload []byte `msgpack:"-" gorm:"not null"`
	Signature       []byte `msgpack:"-" gorm:"not null"`
	payload         []byte
	payloadMutex    sync.Mutex
}

func (uu *updateUser) BeforeCreate(tx *gorm.DB) error {
	if uu.ID == uuid.Nil {
		return errors.New("update user ID must be set before creation")
	}

	return nil
}

func (uu *updateUser) getID() uuid.UUID {
	return uu.ID
}

func (uu *updateUser) getScope(myID uuid.UUID) int {
	return scopeGlobal
}

func (uu *updateUser) getDestination(myID uuid.UUID) uuid.UUID {
	return uu.Target
}

func (uu *updateUser) getType() uint16 {
	return typeUpdateUser
}

func (uu *updateUser) getPayload() []byte {
	uu.payloadMutex.Lock()
	defer uu.payloadMutex.Unlock()

	if len(uu.payload) == 0 {
		bytes, err := msgpack.Marshal(signedContainer{
			Payload:   uu.OriginalPayload,
			Signature: uu.Signature,
			Signer:    uu.Signer,
		})
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error marshalling update user's signed container")
		}
		uu.payload = bytes
	}
	return uu.payload
}

func (uu *updateUser) getAuthor() uuid.UUID {
	return uu.Target
}

func (uu *updateUser) getTimestamp() int64 {
	return uu.Timestamp
}

func (uu *updateUser) validPayload() error {
	switch uu.Type {
	case updateUserTypeUpdateName:
		if !validUserName(string(uu.Data)) {
			return errInvalidUserName
		}
	}

	return nil
}

func (b *bounce) handleUpdateUser(peer string, payload []byte, catchUp bool) broadcastable {
	updateUserMutex.Lock()
	defer updateUserMutex.Unlock()

	// Unpack the signed container
	sc, err := b.unpackSignedContainer(payload)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unpacking signed container for update user")
		return nil
	}
	var uu updateUser
	err = msgpack.Unmarshal(sc.Payload, &uu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling update user")
		return nil
	}
	uu.OriginalPayload = sc.Payload
	uu.Signature = sc.Signature
	uu.Signer = sc.Signer

	// If we already have this update, we just mark that this peer has it too and return
	var existingUU updateUser
	err = b.database.Where("id = ?", uu.ID).First(&existingUU).Error
	if err == nil {
		return &existingUU
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up update user")
	}

	// Save this update, apply the change to the database, and inform the UI
	err = b.saveAndDisplayUpdateUser(uu)
	if err != nil {
		log.WithFields(log.Fields{
			"error":   err.Error(),
			"user_id": uu.Target,
		}).Error("error saving and applying update user")
		return nil
	}

	// If we're not in a catchup, set the state now
	if !catchUp {
		b.updateUserState(uu.Target)
	}

	return &uu
}

func (b *bounce) saveAndDisplayUpdateUser(uu updateUser) error {
	// Make sure this update has a valid payload
	err := uu.validPayload()
	if err != nil {
		return err
	}

	// Save this update
	err = b.database.Create(&uu).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving update user")
	}

	// Display this update in the UI history
	switch uu.Type {
	case updateUserTypeUpdateName:
		b.informUIUpdateUserUpdateName(uu)
	default:
		log.WithFields(log.Fields{
			"id":   uu.ID,
			"type": uu.Type,
		}).Warn("ignoring unsupported update user type")
		return errUnsupportedUpdateUserType
	}

	return nil
}

func (b *bounce) informUIUpdateUserUpdateName(uu updateUser) {
	b.userInterface.UserNameUpdated(UpdateUserUpdateName{
		ID:        uu.ID,
		User:      uu.Target,
		Name:      string(uu.Data),
		Timestamp: uu.Timestamp,
	})
}

func (b *bounce) updateUserState(userID uuid.UUID) {
	// Get the user and assign default states
	var u user
	err := b.database.Where("id = ?", userID).First(&u).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"user_id": userID,
				"error":   err.Error(),
			}).Error("user not found when updating user state")
			return
		} else {
			log.WithFields(log.Fields{
				"user_id": userID,
				"error":   err.Error(),
			}).Fatal("database error looking up user")
		}
	}
	newName := u.Name

	// Get all update users for this user
	var uus []updateUser
	err = b.database.Where("target = ?", userID).Find(&uus).Error
	if err != nil {
		log.WithFields(log.Fields{
			"user_id": userID,
			"error":   err.Error(),
		}).Fatal("database error looking up update users")
	}

	// Iterate through them to get the final user state
	for _, uu := range uus {
		switch uu.Type {
		case updateUserTypeUpdateName:
			newName = string(uu.Data)
		default:
			log.WithFields(log.Fields{
				"id":      uu.ID,
				"type":    uu.Type,
				"user_id": uu.Target,
			}).Warn("unsupported update user type")
		}
	}

	if u.Name != newName {
		// Update the database field for the name
		err := b.database.Where("id = ?", userID).Updates(map[string]interface{}{"name": newName}).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":   err.Error(),
				"user_id": userID,
			}).Fatal("database error updating user name")
		}

		// Inform the UI
		b.userInterface.SetUserName(userID, newName) // TODO: set the whole user state in one call once images are supported?
	}
}

func (b *bounce) updateProfileName(newName string) error {
	return b.applyAndBroadcastUpdateUser(updateUser{
		ID:        uuid.New(),
		Target:    b.currentUserID(),
		Timestamp: time.Now().Unix(),
		Type:      updateUserTypeUpdateName,
		Data:      []byte(newName),
	})
}

func (b *bounce) applyAndBroadcastUpdateUser(uu updateUser) error {
	// Create the signed container for this update
	var err error
	uu.OriginalPayload, err = msgpack.Marshal(&uu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling group update")
	}
	sc := b.createSignedContainer(uu.OriginalPayload)
	uu.Signature = sc.Signature
	uu.Signer = sc.Signer

	err = b.saveAndDisplayUpdateUser(uu)
	if err != nil {
		log.WithFields(log.Fields{
			"id":    uu.ID,
			"error": err.Error(),
		}).Error("error applying update user")
		return err
	}

	b.updateUserState(uu.Target)

	b.broadcast(&uu)

	return nil
}
