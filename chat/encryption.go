package chat

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	insecurerand "math/rand"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var encryptedDeviceCache = map[string][]uuid.UUID{}
var encryptedDeviceCacheMutex sync.Mutex

const maximumRecipients = 15

type encryptedFrame struct {
	ID         uuid.UUID
	Type       uint16
	Payload    []byte
	DeleteAt   int64
	Recipients []recipient
}

type recipient struct {
	FrameID      uuid.UUID `msgpack:"-"`
	PublicKey    []byte
	EncryptedDEK []byte
}

type encryptedSend struct {
	Frame        []byte
	Client       []byte
	Signature    []byte
	payload      []byte
	payloadMutex sync.Mutex
}

func (es encryptedSend) getType() uint16 {
	return typeEncryptedSend
}

func (es encryptedSend) getPayload() []byte {
	es.payloadMutex.Lock()
	defer es.payloadMutex.Unlock()

	if len(es.payload) == 0 {
		bytes, err := msgpack.Marshal(&es)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal encrypted send")
		}
		es.payload = bytes
	}
	return es.payload
	return []byte{}
}

type encryptedSyncDevice struct {
	ID        uuid.UUID `gorm:"type:uuid;primary_key;"`
	Address   string    `gorm:"uniqueIndex"`
	Name      string
	CreatedAt int64
	LastSeen  int64
}

func (b *Bounce) generateKEK(counterpartyPublicKeyBytes []byte) ([]byte, error) {
	curve := ecdh.X25519()

	counterpartyPublicKey, err := curve.NewPublicKey(counterpartyPublicKeyBytes)
	if err != nil {
		return []byte{}, err
	}

	currentUser, ok := b.currentUser()
	if !ok {
		return []byte{}, errUserNotFound
	}

	myPrivateKey, err := curve.NewPrivateKey(currentUser.PrivateECDHKey)
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
	currentUser, ok := b.currentUser()
	if !ok {
		log.Error("cannot send frames to encrypted device before current user exists")
		return
	}

	// Get the users that are in scope
	users := b.getUsersInScope(br)

	// Check if any of them have access to an encrypted device that is online
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
		mustHave, ok := deviceUsers[addr]
		if !ok {
			log.WithFields(log.Fields{
				"address": addr,
			}).Error("unable to determine user ID for encrypted device address")
			continue
		}

		// Create recipients for each user in scope, up to a limit
		recipients := []recipient{}
		for _, u := range b.pruneEncryptedRecipients(mustHave, users) {
			block, err := aes.NewCipher(u.KeyEncryptionKey)
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
				PublicKey:    u.PublicECDSAKey,
				EncryptedDEK: gcm.Seal(nil, []byte{}, dek, nil),
			})
		}

		// Sign the encrypted frame and recipients, send to the encrypted device
		ef := encryptedFrame{
			ID:         br.getID(),
			Type:       br.getType(),
			Payload:    ciphertext,
			DeleteAt:   getDeleteAt(br),
			Recipients: recipients,
		}
		encodedEncryptedFrame, err := msgpack.Marshal(&ef)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error encoding encrypted frame")
			return
		}
		es := encryptedSend{
			Frame:     encodedEncryptedFrame,
			Client:    currentUser.PublicECDSAKey,
			Signature: ed25519.Sign(ed25519.NewKeyFromSeed(currentUser.PrivateECDSAKey), encodedEncryptedFrame),
		}
		rd.messages <- es
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
		err := b.database.Where("id = ? OR id = ?", b.currentUserID(), br.getDestination(b.currentUserID())).Find(&users).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up users")
		}
	} else if scope == scopeGroup {
		var destinationGroup group
		err := b.database.Preload(clause.Associations).First(&destinationGroup, "id = ?", br.getDestination(b.currentUserID())).Error
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
		err := b.database.Select("id", "key_encryption_key", "public_ecdsa_key", "encrypted_devices").Find(&users).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error getting all users")
		}
	} else {
		b.database.
			Distinct("users.id").
			Select("users.id", "users.key_encryption_key", "users.public_ecdsa_key", "users.encrypted_devices").
			Joins("JOIN group_users ON group_users.user_id = users.id").
			Where(
				"group_users.group_id IN (?)",
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
		if b.isDeliveredTo(br, addr) {
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
	err = b.database.Select("id", "key_encryption_key", "public_ecdsa_key", "encrypted_devices").Where("id IN (?)", userList).Find(&users).Error
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
	err := b.database.Preload(clause.Associations).First(&destinationGroup, "id = ?", br.getDestination(b.currentUserID())).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up group")
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
