package chat

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
)

var updateUserMutex sync.Mutex

var updateUserTypeUpdateName = uint16(0)
var updateUserTypeUpdateImage = uint16(1)

var errInvalidUserName = errors.New("invalid name")
var errUnsupportedUpdateUserType = errors.New("unsupported update user type")

type updateUser struct {
	ID              uuid.UUID `gorm:"type:uuid;primary_key;"`
	Target          uuid.UUID
	Type            uint16
	Data            []byte
	PreviousData    []byte `msgpack:"-"` // Used to store the old name during a name change
	Timestamp       int64
	Seen            bool   `msgpack:"-"`
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
	case updateUserTypeUpdateImage:
		_, err := uuid.FromBytes(uu.Data)
		return err
	}

	return nil
}

func (b *Bounce) handleUpdateUser(peer string, payload []byte, catchUp bool) broadcastable {
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

	// Make sure the signing device was not revoked before creating this
	var signerDevice device
	err = b.database.Select("revoked_at").Where("address = ?", uu.Signer).First(&signerDevice).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"address": uu.Signer,
			}).Error("signer device not found for update user")
			return nil
		} else {
			log.WithFields(log.Fields{
				"address": uu.Signer,
				"error":   err.Error(),
			}).Fatal("database error looking up signing device")
		}
	}
	if signerDevice.RevokedAt != 0 && signerDevice.RevokedAt < uu.Timestamp {
		log.WithFields(log.Fields{
			"id":     uu.ID,
			"signer": uu.Signer,
		}).Warn("ignoring update user signed by revoked device")
		go b.sendAck(peer, typeUpdateUser, uu.ID)
		return nil
	}

	// Ignore anything from a blocked user
	if blockedAuthor(&uu) {
		log.WithFields(log.Fields{
			"id":     uu.ID,
			"author": uu.getAuthor(),
		}).Warn("ignoring update user from blocked user")

		if peerDev, ok := b.getDeviceFromAddress(peer); ok {
			if !blockedUser(peerDev.UserID) {
				go b.sendAck(peer, typeUpdateUser, uu.ID)
			}
		}
		return nil
	}

	// Make sure this update was signed by the user who it applies to
	if !b.signedByUser(sc, uu.Target) {
		log.WithFields(log.Fields{
			"target": uu.Target,
			"signer": uu.Signer,
			"peer":   peer,
		}).Warn("ignoring update user not signed by the user it targets")
		return nil
	}

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

func (b *Bounce) saveAndDisplayUpdateUser(uu updateUser) error {
	// Make sure this update has a valid payload
	err := uu.validPayload()
	if err != nil {
		return err
	}

	// Store the last used name on the update if this update changes the name
	if uu.Type == updateUserTypeUpdateName {
		oldName, err := b.previousName(uu)
		if err != nil {
			return err
		}
		uu.PreviousData = []byte(oldName)
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
	case updateUserTypeUpdateImage:
		b.informUIUpdateUserUpdateImage(uu)
	default:
		log.WithFields(log.Fields{
			"id":   uu.ID,
			"type": uu.Type,
		}).Warn("ignoring unsupported update user type")
		return errUnsupportedUpdateUserType
	}

	return nil
}

func (b *Bounce) previousName(uu updateUser) (string, error) {
	// Find the newest update name that isn't this one
	var previousUU updateUser
	err := b.database.Select("data", "MAX(timestamp)").Where("target = ? AND type = ? AND timestamp < ?", uu.Target, updateUserTypeUpdateName, uu.Timestamp).First(&previousUU).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// This user has no earlier name updates, have the current user name be the old name
			var u user
			err = b.database.Select("name").Where("id = ?", uu.Target).First(&u).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					log.WithFields(log.Fields{
						"user_id": uu.Target,
						"error":   err.Error(),
					}).Error("user not found when attempting to determine previous name for user name change")
					return "", err
				} else {
					log.WithFields(log.Fields{
						"user_id": uu.Target,
						"error":   err.Error(),
					}).Fatal("database error looking up user")
				}
			}
			return u.Name, nil
		} else {
			log.WithFields(log.Fields{
				"user_id": uu.Target,
				"error":   err.Error(),
			}).Fatal("database error looking up update user")
		}
	}

	return string(previousUU.Data), nil
}

func (b *Bounce) informUIUpdateUserUpdateName(uu updateUser) {
	go b.ui.UserNameUpdated(UpdateUserUpdateName{
		ID:        uu.ID,
		User:      uu.Target,
		Name:      string(uu.Data),
		OldName:   string(uu.PreviousData),
		Timestamp: uu.Timestamp,
	})
}

func (b *Bounce) informUIUpdateUserUpdateImage(uu updateUser) {
	go b.ui.UserImageUpdated(UpdateUserUpdateImage{
		ID:        uu.ID,
		User:      uu.Target,
		Timestamp: uu.Timestamp,
	})
}

func (b *Bounce) updateUserState(userID uuid.UUID) {
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
	images := []string{}

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
	imageIDs := []uuid.UUID{}
	for _, uu := range uus {
		switch uu.Type {
		case updateUserTypeUpdateName:
			newName = string(uu.Data)
		case updateUserTypeUpdateImage:
			imageID, err := uuid.FromBytes(uu.Data)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("update user container image with invalid UUID")
			} else {
				images = append(images, imageID.String())
				imageIDs = append(imageIDs, imageID)
			}
		default:
			log.WithFields(log.Fields{
				"id":      uu.ID,
				"type":    uu.Type,
				"user_id": uu.Target,
			}).Warn("unsupported update user type")
		}
	}

	if u.Name != newName {
		err := b.database.Table("users").Where("id = ?", userID).Updates(map[string]interface{}{"name": newName}).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":   err.Error(),
				"user_id": userID,
			}).Fatal("database error updating user name")
		}

		u.Name = newName
	}

	newImages := strings.Join(images, ",")
	if u.Images != newImages {
		err := b.database.Table("users").Where("id = ?", userID).Updates(map[string]interface{}{"images": newImages}).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":   err.Error(),
				"user_id": userID,
			}).Fatal("database error updating user images")
		}

		u.Images = newImages
	}

	go b.ui.SetUserState(User{
		ID:     u.ID,
		Name:   u.Name,
		Images: imageIDs,
		// TODO: missing fields?
	})
}

func (b *Bounce) UpdateProfileImage(newImage []byte) error {
	currentUser, ok := b.currentUser()
	if !ok {
		return errUserNotFound
	}

	if !validImage(newImage) {
		return errInvalidImage
	}

	newImageID := uuid.New()
	err := b.embedFile(newImageID, newImage, scopeGlobal, currentUser.ID, fileTypeUserImage, currentUser.ID)
	if err != nil {
		return err
	}

	return b.applyAndBroadcastUpdateUser(updateUser{
		ID:        uuid.New(),
		Target:    b.currentUserID(),
		Timestamp: time.Now().Unix(),
		Type:      updateUserTypeUpdateImage,
		Data:      newImageID[:],
	})
}

func (b *Bounce) UpdateProfileName(newName string) error {
	currentUser, ok := b.currentUser()
	if !ok {
		return errUserNotFound
	}
	if currentUser.Name == newName {
		return nil
	}

	return b.applyAndBroadcastUpdateUser(updateUser{
		ID:        uuid.New(),
		Target:    b.currentUserID(),
		Timestamp: time.Now().Unix(),
		Type:      updateUserTypeUpdateName,
		Data:      []byte(newName),
	})
}

func (b *Bounce) applyAndBroadcastUpdateUser(uu updateUser) error {
	// Create the signed container for this update
	var err error
	uu.OriginalPayload, err = msgpack.Marshal(&uu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling update user")
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
