package chat

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

var updateUserMutex sync.Mutex

var updateUserTypeUpdateName = uint16(0)
var updateUserTypeUpdateImage = uint16(1)
var updateUserTypeAddEncryptedDevice = uint16(2)
var updateUserTypeRemoveEncryptedDevice = uint16(3)
var updateUserTypeSetEncryptedDeviceName = uint16(4)
var updateUserTypeReplaceKeys = uint16(5)
var updateUserTypeReplaceECDHPublicKey = uint16(6)

var errInvalidUserName = errors.New("invalid name")
var errUnsupportedUpdateUserType = errors.New("unsupported update user type")
var errAddressTooShort = errors.New("address is too short")
var errPayloadTooShort = errors.New("payload is too short")

type keySet struct {
	PrivateECDSAKey []byte
	PublicECDSAKey  []byte
	PrivateECDHKey  []byte
	PublicECDHKey   []byte
	Kek             []byte
}

type updateUser struct {
	SignedFrame
	cachedEncoding
	ID           uuid.UUID `gorm:"type:uuid;primary_key;"`
	Target       uuid.UUID
	Type         uint16
	Data         []byte
	PreviousData []byte `msgpack:"-"` // Used to store the old name during a name change
	Timestamp    int64
	Seen         bool `msgpack:"-"`
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
	if uu.Type == updateUserTypeSetEncryptedDeviceName || uu.Type == updateUserTypeReplaceKeys {
		return scopeSync
	}

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
	case updateUserTypeAddEncryptedDevice:
		if len(uu.Data) == 0 {
			return errAddressTooShort
		}
	case updateUserTypeRemoveEncryptedDevice:
		if len(uu.Data) == 0 {
			return errAddressTooShort
		}
	case updateUserTypeSetEncryptedDeviceName:
		// Minimum payload length can't be determined here
	}

	return nil
}

func (b *Bounce) handleUpdateUser(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	updateUserMutex.Lock()
	defer updateUserMutex.Unlock()

	// Unpack the signed container
	sc, err := b.unpackSignedContainer(payload)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unpacking signed container for update user")
		return nil, false
	}
	var uu updateUser
	err = msgpack.Unmarshal(sc.Payload, &uu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling update user")
		return nil, false
	}
	uu.OriginalPayload = sc.Payload
	uu.Signature = sc.Signature
	uu.Signer = sc.Signer

	// Ignore anything from a blocked user
	if blockedUser(uu.getAuthor()) {
		log.WithFields(log.Fields{
			"id":     uu.ID,
			"author": uu.getAuthor(),
		}).Warn("ignoring update user from blocked user")

		if peerDev, ok := b.getDeviceFromAddress(peer); ok {
			if !blockedUser(peerDev.UserID) {
				go b.sendAck(peer, typeUpdateUser, uu.ID)
			}
		}
		return nil, false
	}

	// Make sure the signing device was not revoked before creating this
	var signerDevice device
	err = b.database.Select("revoked_at").Where("address = ?", uu.Signer).First(&signerDevice).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"address": uu.Signer,
			}).Error("signer device not found for update user")
			return nil, false
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
		return nil, false
	}

	// Make sure this update was signed by the user who it applies to
	if !b.signedByUser(sc, uu.Target) {
		log.WithFields(log.Fields{
			"target": uu.Target,
			"signer": uu.Signer,
			"peer":   peer,
		}).Warn("ignoring update user not signed by the user it targets")
		return nil, false
	}

	// If we already have this update, we just mark that this peer has it too and return
	var existingUU updateUser
	err = b.database.Where("id = ?", uu.ID).First(&existingUU).Error
	if err == nil {
		return &existingUU, false
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
		return nil, false
	}

	// If we're not in a catchup, set the state now
	if !catchUp {
		b.updateUserState(uu.Target)
	}

	return &uu, true
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
	case updateUserTypeAddEncryptedDevice:
		// Encrypted devices do not create status changes
	case updateUserTypeRemoveEncryptedDevice:
		// Encrypted devices do not create status changes
	case updateUserTypeSetEncryptedDeviceName:
		// Setting encrypted device name doesn't create status change
	case updateUserTypeReplaceKeys:
		// Changing keys does not create a status change
	case updateUserTypeReplaceECDHPublicKey:
		// Changing keys does not create a status change
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
	encryptedDevices := map[string]bool{}
	encryptedDeviceNames := map[string]string{}
	privateECDSA := u.PrivateECDSAKey
	publicECDSA := u.PublicECDSAKey
	privateECDH := u.PrivateECDHKey
	publicECDH := u.PublicECDHKey

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
		if b.deviceWasRevokedAt(uu.Signer, uu.Timestamp) {
			continue
		}

		switch uu.Type {
		case updateUserTypeUpdateName:
			newName = string(uu.Data)
		case updateUserTypeUpdateImage:
			imageID, err := uuid.FromBytes(uu.Data)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("update user contains image with invalid UUID")
			} else {
				images = append(images, imageID.String())
				imageIDs = append(imageIDs, imageID)
			}
		case updateUserTypeAddEncryptedDevice:
			address := string(uu.Data)
			encryptedDevices[address] = true

			encryptedDeviceCacheMutex.Lock()
			currentUser, ok := encryptedDeviceCache[address]
			if ok && currentUser != userID {
				log.WithFields(log.Fields{
					"address":     address,
					"currentUser": currentUser,
					"newUser":     userID,
				}).Error("refusing to add encrypted device that already belongs to another user")
			} else {
				encryptedDeviceCache[address] = userID
			}
			encryptedDeviceCacheMutex.Unlock()
		case updateUserTypeRemoveEncryptedDevice:
			address := string(uu.Data)
			delete(encryptedDevices, address)

			encryptedDeviceCacheMutex.Lock()
			currentUser, ok := encryptedDeviceCache[address]
			if ok && currentUser == userID {
				delete(encryptedDeviceCache, address)
			} else if ok {
				log.WithFields(log.Fields{
					"address":      address,
					"currentUser":  currentUser,
					"removingUser": userID,
				}).Error("refusing to remove encrypted device from cache that doesn't belong to user doing the removal")
			}
			encryptedDeviceCacheMutex.Unlock()
		case updateUserTypeSetEncryptedDeviceName:
			addressLength := len(b.network.Address())
			if len(uu.Data) < addressLength+2 {
				log.WithFields(log.Fields{
					"length": len(uu.Data),
				}).Error("update user data length to short to update encrypted device name")
				continue
			}
			address := string(uu.Data[:addressLength])
			name := string(uu.Data[addressLength+1:])
			encryptedDeviceNames[address] = string(name)
		case updateUserTypeReplaceKeys:
			var ks keySet
			err = msgpack.Unmarshal(uu.Data, &ks)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("invlaid key set in update user")
				continue
			}

			publicECDSA = ks.PublicECDSAKey
			privateECDSA = ks.PrivateECDSAKey
			publicECDH = ks.PublicECDHKey
			privateECDH = ks.PrivateECDHKey
		case updateUserTypeReplaceECDHPublicKey:
			publicECDH = uu.Data
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

	encryptedDeviceList := []string{}
	for encryptedDevice, _ := range encryptedDevices {
		encryptedDeviceList = append(encryptedDeviceList, encryptedDevice)
	}
	encryptedDeviceField := strings.Join(encryptedDeviceList, ",")
	if u.EncryptedDevices != encryptedDeviceField {
		err := b.database.Table("users").Where("id = ?", userID).Updates(map[string]interface{}{"encrypted_devices": encryptedDeviceField}).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":   err.Error(),
				"user_id": userID,
			}).Fatal("database error updating user encryptedDevices")
		}

		u.EncryptedDevices = encryptedDeviceField
	}

	if userID == b.currentUserID() {
		b.createOrDeleteEncryptedSyncDevices(encryptedDeviceList)

		var allEncryptedSyncDevices []encryptedSyncDevice
		err := b.database.Find(&allEncryptedSyncDevices).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error getting all encrypted sync devices")
		}
		for _, esd := range allEncryptedSyncDevices {
			desiredDeviceName, ok := encryptedDeviceNames[esd.Address]
			if !ok {
				continue
			}
			if esd.Name != desiredDeviceName {
				err := b.database.Table("encrypted_sync_devices").Where("id = ?", esd.ID).Updates(map[string]interface{}{"name": desiredDeviceName}).Error
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error renaming encrypted sync device")
					continue
				}

				go b.ui.DeviceRenamed(esd.ID, desiredDeviceName)
			}
		}
	}

	if !bytes.Equal(privateECDSA, u.PrivateECDSAKey) {
		err := b.database.Table("users").Where("id = ?", userID).Updates(map[string]interface{}{"private_ecdsa_key": privateECDSA}).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":   err.Error(),
				"user_id": userID,
			}).Fatal("database error updating user")
		}
	}

	if !bytes.Equal(publicECDSA, u.PublicECDSAKey) {
		err := b.database.Table("users").Where("id = ?", userID).Updates(map[string]interface{}{"public_ecdsa_key": publicECDSA}).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":   err.Error(),
				"user_id": userID,
			}).Fatal("database error updating user")
		}
	}

	if !bytes.Equal(privateECDH, u.PrivateECDHKey) {
		err := b.database.Table("users").Where("id = ?", userID).Updates(map[string]interface{}{"private_ecdh_key": privateECDH}).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":   err.Error(),
				"user_id": userID,
			}).Fatal("database error updating user")
		}
	}

	if !bytes.Equal(publicECDH, u.PublicECDHKey) {
		err := b.database.Table("users").Where("id = ?", userID).Updates(map[string]interface{}{"public_ecdh_key": publicECDH}).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error":   err.Error(),
				"user_id": userID,
			}).Fatal("database error updating user")
		}
	}

	go b.ui.SetUserState(User{
		ID:               u.ID,
		Name:             u.Name,
		Alias:            u.Alias,
		Notes:            u.Notes,
		Images:           imageIDs,
		Blocked:          u.Blocked,
		IntroductionTime: u.IntroductionTime,
	})
}

func (b *Bounce) createOrDeleteEncryptedSyncDevices(addresses []string) {
	shouldExist := map[string]bool{}
	for _, addr := range addresses {
		shouldExist[addr] = true

		var existingEncryptedSyncDevice encryptedSyncDevice
		err := b.database.Where("address = ?", addr).First(&existingEncryptedSyncDevice).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				esd := encryptedSyncDevice{
					ID:        uuid.New(),
					Address:   addr,
					Name:      "",
					CreatedAt: time.Now().Unix(),
				}
				err = b.database.Create(&esd).Error
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("database error creating encrypted sync device")
				} else {
					b.ui.DeviceAdded(Device{
						ID:        esd.ID,
						Address:   addr,
						CreatedAt: esd.CreatedAt,
						Local:     false,
						Encrypted: true,
						Online:    false,
					})
				}
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error looking up encrypted sync device")
			}
		}
	}

	var allEncryptedSyncDevices []encryptedSyncDevice
	err := b.database.Find(&allEncryptedSyncDevices).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error getting all encrypted sync devices")
	}
	for _, esd := range allEncryptedSyncDevices {
		if _, ok := shouldExist[esd.Address]; !ok {
			err = b.database.Delete(&esd).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("database error deleting encrypted sync device")
			} else {
				go b.ui.DeviceRevoked(esd.ID)
			}
		}
	}
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

func (b *Bounce) addEncryptedDevice(address string) error {
	return b.applyAndBroadcastUpdateUser(updateUser{
		ID:        uuid.New(),
		Target:    b.currentUserID(),
		Timestamp: time.Now().Unix(),
		Type:      updateUserTypeAddEncryptedDevice,
		Data:      []byte(address),
	})
}

func (b *Bounce) rollKeys() error {
	// Cache the current signing key that is about to be repalced
	cu, ok := b.currentUser()
	if !ok {
		return errors.New("cannot roll keys without existing current user")
	}
	oldECDSA := cu.PrivateECDSAKey

	// Generate new user keys
	curve := ecdh.X25519()
	privateECDHKey, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error generating x25519 private key")
	}
	publicECDHKey := privateECDHKey.PublicKey()
	publicECDSAKey, privateECDSAKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error generating ECDSA keypair")
	}
	kek := make([]byte, 32)
	rand.Read(kek)

	ks := keySet{
		PublicECDSAKey:  []byte(publicECDSAKey),
		PrivateECDSAKey: privateECDSAKey,
		PublicECDHKey:   publicECDHKey.Bytes(),
		PrivateECDHKey:  privateECDHKey.Bytes(),
		Kek:             kek,
	}
	keySetData, err := msgpack.Marshal(&ks)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("failed to marshal key set")
		return err
	}

	// Update any encrypted devices that are currently online
	var allESDs []encryptedSyncDevice
	err = b.database.Find(&allESDs).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error loading all encrypted sync devices")
	}
	for _, esd := range allESDs {
		rd := b.getRemoteDevice(esd.Address)
		if rd.connectedSockets.Load() > 0 {
			esdks := keySet{
				PublicECDSAKey: ks.PublicECDSAKey,
				PublicECDHKey:  ks.PublicECDHKey,
			}
			esdKeySetData, err := msgpack.Marshal(&esdks)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("failed to marshal key set")
				continue
			}

			edma := encryptedDeviceManagementAction{
				ActionType: actionTypeChangeManagementKey,
				Data:       esdKeySetData,
			}
			encoded, err := msgpack.Marshal(&edma)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error encoding encrypted device management action")
				continue
			}
			signature := ed25519.Sign(oldECDSA, encoded)

			med := manageEncryptedDevice{
				ID:        uuid.New(),
				Action:    encoded,
				Signature: signature,
			}

			b.sendDirect(esd.Address, &med)
		}
	}

	// Inform sync devices about new keys, and all other users about public ECDH key
	err = b.applyAndBroadcastUpdateUser(updateUser{
		ID:        uuid.New(),
		Target:    b.currentUserID(),
		Timestamp: time.Now().Unix(),
		Type:      updateUserTypeReplaceKeys,
		Data:      keySetData,
	})
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("failed to apply and broadcast new key set")
		return err
	}
	err = b.applyAndBroadcastUpdateUser(updateUser{
		ID:        uuid.New(),
		Target:    b.currentUserID(),
		Timestamp: time.Now().Unix(),
		Type:      updateUserTypeReplaceECDHPublicKey,
		Data:      publicECDHKey.Bytes(),
	})
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("failed to apply and broadcast new ECDH key")
		return err
	}

	return nil
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
