package chat

import (
	"crypto/ecdh"
	"errors"
	"strings"
	"sync"

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

// sendToEncryptedDevices stores a frame, sealed, on every online encrypted device of every user in scope.  Each
// copy is addressed to the mailboxes of the conversation it belongs to (see mailbox.go), so the host learns nothing
// about who is in scope.
func (b *Bounce) sendToEncryptedDevices(br broadcastable) {
	// Re-key frames are sealed to specific devices rather than to the user key that is being replaced
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

	availableEncryptedDevices := b.onlineEncryptedDevicesOf(b.getUsersInScope(br))
	if len(availableEncryptedDevices) == 0 {
		return
	}

	// Each host gets its own copy, sealed to the mailboxes its owner can read
	for addr, rd := range availableEncryptedDevices {
		secrets, err := b.hostSecretsFor(br, addr)
		if err != nil {
			log.WithFields(log.Fields{
				"id":    br.getID(),
				"type":  br.getType(),
				"host":  addr,
				"error": err.Error(),
			}).Debug("not sealing frame for encrypted device")
			continue
		}
		ef, err := sealFrame(br, secrets, b.copySettingsFor(br))
		if err != nil {
			log.WithFields(log.Fields{
				"id":    br.getID(),
				"error": err.Error(),
			}).Error("error sealing frame for encrypted device")
			return
		}
		recordSealedCopy(ef.ID, br)
		rd.send(*ef)
	}
}

// onlineEncryptedDevicesOf returns the connected encrypted devices belonging to any of the given users
func (b *Bounce) onlineEncryptedDevicesOf(users []user) map[string]*remoteDevice {
	available := map[string]*remoteDevice{}
	for _, u := range users {
		if len(u.EncryptedDevices) == 0 {
			continue
		}
		for _, addr := range strings.Split(u.EncryptedDevices, ",") {
			rd := b.getRemoteDevice(addr)
			if rd.connectedSockets() > 0 {
				available[addr] = rd
			}
		}
	}
	return available
}

// encryptFrameForDevice seals one copy of a frame for one encrypted device, for catch ups
func (b *Bounce) encryptFrameForDevice(br broadcastable, addr string) *encryptedFrame {
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

	secrets, err := b.hostSecretsFor(br, addr)
	if err != nil {
		log.WithFields(log.Fields{
			"id":    br.getID(),
			"type":  br.getType(),
			"host":  addr,
			"error": err.Error(),
		}).Debug("not sealing frame for encrypted device")
		return nil
	}
	ef, err := sealFrame(br, secrets, b.copySettingsFor(br))
	if err != nil {
		log.WithFields(log.Fields{
			"id":    br.getID(),
			"error": err.Error(),
		}).Error("error sealing frame for encrypted device")
		return nil
	}
	recordSealedCopy(ef.ID, br)
	return ef
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
		rd.send(*ef)
	}
}

// encryptReKeyFrame seals a frame that replaces our user keys to the device mailboxes of our other devices, which
// derive from device keys rather than the user key being replaced
func (b *Bounce) encryptReKeyFrame(br broadcastable) *encryptedFrame {
	myDevice, ok := b.getDeviceFromAddress(b.network.Address())
	if !ok {
		log.Error("cannot encrypt re-key frames with no current device")
		return nil
	}

	secrets := [][]byte{}
	for _, addr := range b.getBroadcastScope(br, true) {
		recipientDevice, ok := b.getDeviceFromAddress(addr)
		if !ok {
			log.WithFields(log.Fields{
				"address": addr,
			}).Warn("device in broadcast scope not found for re-key encryption")
			continue
		}
		if recipientDevice.UserID != myDevice.UserID {
			log.Error("refusing to seal a re-key frame for a device that isn't mine")
			continue
		}
		secret, err := b.deviceThreadSecret(myDevice.ECDHPrivateKey, recipientDevice.ECDHPublicKey)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error deriving device thread secret for re-key frame")
			continue
		}
		secrets = append(secrets, secret)
	}
	if len(secrets) == 0 {
		return nil
	}

	ef, err := sealFrame(br, secrets, b.copySettingsFor(br))
	if err != nil {
		log.WithFields(log.Fields{
			"id":    br.getID(),
			"error": err.Error(),
		}).Error("error sealing re-key frame")
		return nil
	}
	recordSealedCopy(ef.ID, br)
	return ef
}

// copySettingsFor decides, per frame type, how a copy on an encrypted device behaves beyond its mailboxes: when the
// host should delete it on its own, and which slot it occupies (see mailbox.go).  Drafts take the slot of their
// thread so the host keeps only the newest, including an empty one that clears the thread, which stays until the
// next draft replaces it so that a device offline for a long time still learns the draft was cleared.  Everything
// else takes a random slot.
func (b *Bounce) copySettingsFor(br broadcastable) copySettings {
	settings := copySettings{Slot: randomSlot()}
	switch item := br.(type) {
	case *directMessage:
		settings.DeleteAt = item.DeleteAt
	case *groupMessage:
		settings.DeleteAt = item.DeleteAt
	case *draft:
		if me, ok := b.currentUser(); ok {
			settings.Slot = draftSlot(syncThreadSecret(me.PrivateECDHKey), item.Thread)
			settings.Version = item.Timestamp
		}
	}
	return settings
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
		if err == nil {
			for _, u := range destinationGroup.Users {
				users = append(users, u)
			}
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("database error looking up group while getting users in group scope for encryption, using sync scope")
			currentUser, ok := b.currentUser()
			if !ok {
				log.Error("cannot get users in scope before profile exists")
				return users
			}
			users = append(users, currentUser)
		}
	} else if scope == scopeGlobal {
		users = b.getUsersInGlobalScope(br.getAuthor())
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
		}).Error("unknown broadcast scope for encrypted frame")
	}

	var unblockedUsers []user
	for _, u := range users {
		if !blockedUser(u.ID) {
			unblockedUsers = append(unblockedUsers, u)
		}
	}

	return unblockedUsers
}

func (b *Bounce) getUsersInGlobalScope(userID uuid.UUID) []user {
	var users []user
	if userID == b.currentUserID() {
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
		userMap := map[uuid.UUID]user{}

		// Find all the groups this user is in
		var groups []group
		err := b.database.
			Distinct().
			Select("groups.id").
			Joins("JOIN group_users ON group_users.group_id = groups.id").
			Where("user_id = ?", userID).
			Find(&groups).
			Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up groups user is in")
		}

		// Collect all of the devices belonging to all of the users and invites in these groups
		for _, g := range groups {
			state, err := b.currentGroupState(g.ID)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error getting consensus state for group during overlap scope calculation")
				continue
			}

			for _, userID := range state.users {
				var u user
				err := b.database.Preload(clause.Associations).First(&u, "id = ?", userID).Error
				if err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						log.WithFields(log.Fields{
							"user_id": userID,
						}).Error("group contains user not found in database")
						continue
					} else {
						log.WithFields(log.Fields{
							"error": err.Error(),
						}).Fatal("database error looking up user")
					}
				}
				userMap[u.ID] = u
			}

			for _, userID := range state.invites {
				var u user
				err := b.database.Preload(clause.Associations).First(&u, "id = ?", userID).Error
				if err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						log.WithFields(log.Fields{
							"user_id": userID,
						}).Error("group contains invited user not found in database")
						continue
					} else {
						log.WithFields(log.Fields{
							"error": err.Error(),
						}).Fatal("database error looking up user")
					}
				}
				userMap[u.ID] = u
			}
		}

		for _, u := range userMap {
			users = append(users, u)
		}
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
		if b.devicePool.isRevoked(addr) {
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
			}).Warn("group not found when trying to get users in group with invite scope")

			currentUser, ok := b.currentUser()
			if !ok {
				log.Error("cannot get users in scope before profile exists")
				return users
			}
			users = append(users, currentUser)
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
