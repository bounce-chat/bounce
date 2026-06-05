package chat

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	insecurerand "math/rand"
	"strings"
	"sync"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var encryptedDeviceCache = map[string]uuid.UUID{}
var encryptedDeviceCacheMutex sync.Mutex

const maximumRecipients = 15

type encryptedSyncDevice struct {
	ID        uuid.UUID `gorm:"type:uuid;primary_key;"`
	Address   string    `gorm:"uniqueIndex"`
	Name      string
	CreatedAt int64
	LastSeen  int64
}

func (b *Bounce) generateKEK(counterpartyPublicKeyBytes []byte) ([]byte, error) {
	currentUser, ok := b.currentUser()
	if !ok {
		return []byte{}, errUserNotFound
	}

	return b.generateKEKFromPrivateKey(currentUser.PrivateECDHKey, counterpartyPublicKeyBytes)
}

func (b *Bounce) generateKEKFromPrivateKey(privateKey, counterpartyPublicKeyBytes []byte) ([]byte, error) {
	curve := ecdh.X25519()

	counterpartyPublicKey, err := curve.NewPublicKey(counterpartyPublicKeyBytes)
	if err != nil {
		return []byte{}, err
	}

	myPrivateKey, err := curve.NewPrivateKey(privateKey)
	if err != nil {
		return []byte{}, err
	}

	kek, err := myPrivateKey.ECDH(counterpartyPublicKey)
	if err != nil {
		return []byte{}, err
	}

	return kek, nil
}

func (b *Bounce) sendToEncryptedDevices(br broadcastable) {
	// Encrypt re-key frames to the specific devices that should be able to decrypt them
	if br.getType() == typeUpdateUser {
		sc, err := b.unpackSignedContainer(br.getPayload())
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error unpacking signed container for update user")
			return
		}
		var uu updateUser
		err = msgpack.Unmarshal(sc.Payload, &uu)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error unmarshalling updateUser for encryption")
		}
		if uu.Type == updateUserTypeReplaceKeys {
			b.sendEncryptReKeyFrames(br)
			return
		}
	}

	currentUser, ok := b.currentUser()
	if !ok {
		log.Error("cannot send to encrypted devices before user exists")
		return
	}

	// Get the users that are in scope
	users := b.getUsersInScope(br)

	// Collect all the encrypted devices and their owners
	allEncryptedDevices := map[string]bool{}
	deviceUsers := map[string]uuid.UUID{}
	for _, u := range users {
		if len(u.EncryptedDevices) > 0 {
			for _, addr := range strings.Split(u.EncryptedDevices, ",") {
				allEncryptedDevices[addr] = true
				deviceUsers[addr] = u.ID
			}
		}
	}
	availableEncryptedDevices := map[string]*remoteDevice{}
	for addr, _ := range allEncryptedDevices {
		rd := b.getRemoteDevice(addr)
		if rd.connectedSockets.Load() > 0 {
			availableEncryptedDevices[addr] = rd
		}
	}
	if len(availableEncryptedDevices) == 0 {
		return
	}

	// Encrypt the frame with a random key
	dek := make([]byte, 32)
	rand.Read(dek)

	dekBlock, err := aes.NewCipher(dek)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error encrypting frame")
		return
	}
	dekGCM, err := cipher.NewGCMWithRandomNonce(dekBlock)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error encrypting frame")
		return
	}
	ciphertext := dekGCM.Seal(nil, []byte{}, br.getPayload(), nil)

	for addr, rd := range availableEncryptedDevices {
		// Get the user that owns this device, since they must be one of the recipients
		owner, ok := deviceUsers[addr]
		if !ok {
			log.WithFields(log.Fields{
				"address": addr,
			}).Error("no users own this device")
		}

		// Create recipients for each user in scope, up to a limit
		recipients := []recipient{}
		for _, u := range b.pruneEncryptedRecipients(owner, users) {
			kek, err := b.generateKEK(u.PublicECDHKey)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error generating kek")
				return
			}

			block, err := aes.NewCipher(kek)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error encrypting frame")
				return
			}
			gcm, err := cipher.NewGCMWithRandomNonce(block)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error encrypting frame")
				return
			}

			recipients = append(recipients, recipient{
				EncrypterKey: currentUser.PublicECDHKey,
				PublicKey:    u.PublicECDHKey,
				EncryptedDEK: gcm.Seal(nil, []byte{}, dek, nil),
			})
		}

		// Sign the encrypted frame and recipients, send to the encrypted device
		ef := encryptedFrame{
			ID:         br.getID(),
			Type:       br.getType(),
			Payload:    ciphertext,
			Timestamp:  br.getTimestamp(),
			DeleteAt:   getDeleteAt(br),
			Recipients: recipients,
		}
		rd.messages <- ef
	}
}

func (b *Bounce) encryptFrameForDevice(br broadcastable, addr string) *encryptedFrame {
	// Encrypt re-key frames to the specific devices that should be able to decrypt them
	if br.getType() == typeUpdateUser {
		var uu updateUser
		err := msgpack.Unmarshal(br.getPayload(), &uu)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error unmarshalling updateUser for encryption")
		}
		if uu.Type == updateUserTypeReplaceKeys {
			return b.encryptReKeyFrame(br)
		}
	}

	currentUser, ok := b.currentUser()
	if !ok {
		log.Error("cannot send to encrypted devices before user exists")
		return nil
	}

	// Get the users that are in scope
	users := b.getUsersInScope(br)

	// Collect all the encrypted devices and their owners
	allEncryptedDevices := map[string]bool{}
	deviceUsers := map[string]uuid.UUID{}
	for _, u := range users {
		if len(u.EncryptedDevices) > 0 {
			for _, addr := range strings.Split(u.EncryptedDevices, ",") {
				allEncryptedDevices[addr] = true
				deviceUsers[addr] = u.ID
			}
		}
	}

	// Encrypt the frame with a random key
	dek := make([]byte, 32)
	rand.Read(dek)

	dekBlock, err := aes.NewCipher(dek)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error encrypting frame")
		return nil
	}
	dekGCM, err := cipher.NewGCMWithRandomNonce(dekBlock)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error encrypting frame")
		return nil
	}
	ciphertext := dekGCM.Seal(nil, []byte{}, br.getPayload(), nil)

	// Get the user that owns this device, since they must be one of the recipients
	owner, ok := deviceUsers[addr]
	if !ok {
		log.WithFields(log.Fields{
			"address": addr,
		}).Error("no users own this device")
		return nil
	}

	// Create recipients for each user in scope, up to a limit
	recipients := []recipient{}
	for _, u := range b.pruneEncryptedRecipients(owner, users) {
		kek, err := b.generateKEK(u.PublicECDHKey)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error generating kek")
			return nil
		}

		block, err := aes.NewCipher(kek)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error encrypting frame")
			return nil
		}
		gcm, err := cipher.NewGCMWithRandomNonce(block)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error encrypting frame")
			return nil
		}

		recipients = append(recipients, recipient{
			EncrypterKey: currentUser.PublicECDHKey,
			PublicKey:    u.PublicECDHKey,
			EncryptedDEK: gcm.Seal(nil, []byte{}, dek, nil),
		})
	}

	// Sign the encrypted frame and recipients, send to the encrypted device
	return &encryptedFrame{
		ID:         br.getID(),
		Type:       br.getType(),
		Payload:    ciphertext,
		Timestamp:  br.getTimestamp(),
		DeleteAt:   getDeleteAt(br),
		Recipients: recipients,
	}
}

func (b *Bounce) sendEncryptReKeyFrames(br broadcastable) {
	ef := b.encryptReKeyFrame(br)
	if ef == nil {
		log.Error("failed to encrypt re-key frame")
		return
	}

	var allESDs []encryptedSyncDevice
	err := b.database.Find(&allESDs).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error loading all encrypted sync devices")
	}

	for _, esd := range allESDs {
		rd := b.getRemoteDevice(esd.Address)
		if rd.connectedSockets.Load() < 1 {
			continue
		}

		rd.messages <- ef
	}
}

func (b *Bounce) encryptReKeyFrame(br broadcastable) *encryptedFrame {
	dek := make([]byte, 32)
	rand.Read(dek)

	dekBlock, err := aes.NewCipher(dek)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error encrypting frame")
		return nil
	}
	dekGCM, err := cipher.NewGCMWithRandomNonce(dekBlock)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error encrypting frame")
		return nil
	}
	ciphertext := dekGCM.Seal(nil, []byte{}, br.getPayload(), nil)

	deviceRecipients := []deviceRecipient{}
	addrs := b.getBroadcastScope(br, true)
	myDevice, ok := b.getDeviceFromAddress(b.network.Address())
	if !ok {
		log.Error("cannot encrypt re-key frames with no current device")
		return nil
	}
	for _, addr := range addrs {
		recipientDevice, ok := b.getDeviceFromAddress(addr)
		if !ok {
			log.WithFields(log.Fields{
				"address": addr,
			}).Warn("device in broadcast scope not found for re-key encryption")
			continue
		}

		if recipientDevice.UserID != myDevice.UserID {
			log.Error("refusing to create device recipient for re-key frame for a device that isn't mine")
			continue
		}

		kek, err := b.generateKEKFromPrivateKey(myDevice.ECDHPrivateKey, recipientDevice.ECDHPublicKey)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error creating kek for device recipient")
			continue
		}

		block, err := aes.NewCipher(kek)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error encrypting frame")
			return nil
		}
		gcm, err := cipher.NewGCMWithRandomNonce(block)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error encrypting frame")
			return nil
		}

		deviceRecipients = append(deviceRecipients, deviceRecipient{
			RecipientAddress: addr,
			Counterparty:     b.network.Address(),
			EncryptedDEK:     gcm.Seal(nil, []byte{}, dek, nil),
		})
	}

	return &encryptedFrame{
		ID:               br.getID(),
		Type:             br.getType(),
		Payload:          ciphertext,
		Timestamp:        br.getTimestamp(),
		DeleteAt:         getDeleteAt(br),
		DeviceRecipients: deviceRecipients,
	}
}

func getDeleteAt(br broadcastable) int64 {
	switch item := br.(type) {
	case *directMessage:
		return item.DeleteAt
	case *groupMessage:
		return item.DeleteAt
	}
	return 0
}

func (b *Bounce) getUsersInScope(br broadcastable) []user {
	// Get all of the users that should receive this frame
	var users []user
	scope := br.getScope(b.currentUserID())
	if scope == scopeSync {
		currentUser, ok := b.currentUser()
		if !ok {
			log.Error("cannot get users in scope before profile exists")
			return users
		}
		users = append(users, currentUser)
	} else if scope == scopeUser {
		err := b.database.Preload(clause.Associations).Where("id = ? OR id = ?", b.currentUserID(), br.getDestination(b.currentUserID())).Find(&users).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up users")
		}
	} else if scope == scopeGroup {
		var destinationGroup group
		err := b.database.Preload("Users.Devices").Preload(clause.Associations).First(&destinationGroup, "id = ?", br.getDestination(b.currentUserID())).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up group")
		}
		for _, u := range destinationGroup.Users {
			users = append(users, u)
		}
	} else if scope == scopeGlobal {
		users = b.getUsersInGlobalScope(br)
	} else if scope == scopeCustom {
		users = b.getUsersInCustomScope(br)
		profileIncluded := false
		for _, u := range users {
			if u.ID == b.currentUserID() {
				profileIncluded = true
			}
		}
		if !profileIncluded {
			currentUser, ok := b.currentUser()
			if !ok {
				log.Error("cannot get users in scope before profile exists")
				return users
			}
			users = append(users, currentUser)
		}
	} else if scope == scopeGroupWithInvites {
		users = b.getUsersInGroupWithInvitesScope(br)
	} else {
		log.WithFields(log.Fields{
			"destination": br.getDestination(b.currentUserID()),
			"type":        br.getType(),
			"scope":       scope,
		}).Fatal("unknown broadcast scope for encrypted frame")
	}

	return users
}

func (b *Bounce) getUsersInGlobalScope(br broadcastable) []user {
	var users []user
	if br.getAuthor() == b.currentUserID() {
		// TODO: select most active users, as opposed to random?
		err := b.database.Clauses(clause.OrderBy{
			Expression: clause.Expr{SQL: "RANDOM()"},
		}).Limit(maximumRecipients).Find(&users).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error getting all users")
		}
	} else {
		b.database.
			Joins("LEFT JOIN group_users ON group_users.user_id = users.id").
			Where(
				"(group_users.user_id IS NULL AND (users.id = ? OR users.id = ?)) OR group_users.group_id IN (?)",
				br.getAuthor(),
				b.currentUserID(),
				b.database.
					Model(&group{}).
					Distinct().
					Select("groups.id").
					Joins("JOIN group_users ON group_users.group_id = groups.id").
					Where("user_id = ?", br.getAuthor()),
			).
			Find(&users)
	}
	return users
}

func (b *Bounce) getUsersInCustomScope(br broadcastable) []user {
	var users = []user{}

	var cs customScope
	err := b.database.Where("id = ?", br.getDestination(b.currentUserID())).First(&cs).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"frame_id": br.getID(),
				"type":     br.getType(),
				"id":       br.getID(),
				"scope":    br.getDestination(b.currentUserID()),
			}).Error("cannot broadcast to unknown custom scope")
			return users

		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up custom scope")
		}
	}

	userIDs := map[uuid.UUID]bool{}
	for _, addr := range cs.addresses() {
		if _, revoked := b.devicePool.revokedDevices[addr]; revoked {
			continue
		}
		dev, ok := b.getDeviceFromAddress(addr)
		if ok {
			userIDs[dev.UserID] = true
		}
	}

	userList := []uuid.UUID{}
	for userID, _ := range userIDs {
		userList = append(userList, userID)
	}
	err = b.database.Where("id IN (?)", userList).Find(&users).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up users")
	}

	return users
}

func (b *Bounce) getUsersInGroupWithInvitesScope(br broadcastable) []user {
	var users []user

	var destinationGroup group
	err := b.database.Preload("Users.Devices").Preload(clause.Associations).First(&destinationGroup, "id = ?", br.getDestination(b.currentUserID())).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("group not found when trying to get users in group with invite scope")
			return users
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up group")
		}
	}
	for _, u := range destinationGroup.Users {
		users = append(users, u)
	}
	invites := []uuid.UUID{}
	if len(destinationGroup.Invites) > 0 {
		for _, inviteIDString := range strings.Split(destinationGroup.Invites, ",") {
			inviteID, err := uuid.Parse(inviteIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":    err.Error(),
					"group_id": destinationGroup.ID,
					"invites":  destinationGroup.Invites,
				}).Fatal("invalid UUID in group invite list")
			}

			invites = append(invites, inviteID)
		}
	}
	for _, userID := range invites {
		var u user
		err := b.database.Preload(clause.Associations).First(&u, "id = ?", userID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"frame_id":    br.getID(),
					"destination": br.getDestination(b.currentUserID()),
					"type":        br.getType(),
				}).Error("user not found when adding invitees to group scope")
				continue
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("error loading user from database")
			}
		}
		users = append(users, u)
	}

	return users
}

func (b *Bounce) pruneEncryptedRecipients(mustHave uuid.UUID, users []user) []user {
	if len(users) <= maximumRecipients {
		return users
	}

	// Determine which user we must have
	var mustHaveUser user
	otherUsers := []user{}
	for i, _ := range users {
		if users[i].ID == mustHave {
			mustHaveUser = users[i]
		} else {
			otherUsers = append(otherUsers, users[i])
		}
	}

	// TODO: prioritize based on which users are most likely to be online?  Choose random ones for now
	otherUsersSelection := chooseNUsers(otherUsers, maximumRecipients-1)
	return append(otherUsersSelection, mustHaveUser)
}

func chooseNUsers(set []user, n int) []user {
	if n < 0 {
		return []user{}
	}
	if len(set) < n {
		return set
	}
	r := insecurerand.New(insecurerand.NewSource(time.Now().UnixNano()))
	order := r.Perm(len(set))
	picks := order[0:n]
	results := []user{}
	for _, pick := range picks {
		results = append(results, set[pick])
	}
	return results
}
