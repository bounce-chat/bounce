package chat

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"sync"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/zeebo/blake3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Group keys
//
// Every group has a chain of symmetric keys that its members share and that nobody else, including the encrypted
// devices that store the group's frames, can derive.  The chain is a ratchet: the group creation establishes the
// first key from a random secret, and every frame that shrinks the membership (an admin removing a user or revoking
// an invite) or that explicitly rotates the key carries a fresh random secret, wrapped once for every remaining
// member and invitee under their user ECDH key.  The next key is derived from the previous key and that secret, so
// a removed user, who received the frame (that is how they learn they were removed) but has no wrap in it, is
// missing a link they can never recover, no matter how many later secrets they can unwrap.
//
// Chaining is what makes this safe under eventual consistency.  If two admins remove two users concurrently, each
// wraps for their own view of the membership, so each removed user may be able to unwrap the other admin's secret;
// but whichever order consensus settles on, every removed user is missing at least one link.  The chain is a pure
// function of the canonical stack, so it is recomputed from the stack whenever consensus is written, and rollbacks
// are handled for free.  Members keep every key they have ever derived so that frames sealed under old keys stay
// readable.
//
// A user who leaves on their own cannot be trusted to rotate for the others, so a self removal, a rejected invite,
// or a block marks the chain stale; any member rotates it before the next time they need to use it.  Two members
// may do that concurrently, which is fine: both secrets end up in the chain, and a frame sealed under one member's
// intermediate key is still readable because every member can reconstruct the few intermediate states (see
// groupKeyByID).
//
// Each frame that carries a secret also carries its hash, so a member whose unwrapped secret does not match knows
// the author gave different members different secrets, rather than silently ending up in a forked group.

const groupKeySize = 32

const groupKeyInitialInfo = "bounce-group-key-initial"
const groupKeyRatchetInfo = "bounce-group-key-ratchet"
const groupKeyWrapInfo = "bounce-group-key-wrap"

// Domains for the additional data that binds each wrap to its purpose and recipient
const groupKeyDomainSecret = "bounce-group-key-secret"
const groupKeyDomainChain = "bounce-group-key-chain"
const groupKeyDomainResponse = "bounce-group-key-response"

var errGroupKeyUnavailable = errors.New("group key not available")
var errGroupKeyNotStale = errors.New("group key does not need rotation")
var errGroupKeyRecipientMissingKey = errors.New("group member has no ECDH public key")

// A wrap of key material for one user.  The material is encrypted with a key agreed between a per frame ephemeral
// X25519 key and the user's long term ECDH key, so all of a user's devices can open it and nobody else can.
type groupKeyWrap struct {
	UserID     uuid.UUID
	Ciphertext []byte
}

// A key in a group's chain, persisted so that frames sealed under older keys remain readable and so that the chain
// does not have to be re-derived from scratch on every consensus write.  Epoch is the ID of the frame that
// established the key, or the group ID for the initial key.  Secret is the secret that produced this key from the
// previous one, kept so that non canonical intermediate keys (from concurrent rotations) can be reconstructed.
type groupKey struct {
	ID      uuid.UUID `gorm:"type:uuid;primary_key;"`
	GroupID uuid.UUID `gorm:"uniqueIndex:idx_group_keys_group_epoch,priority:1"`
	Epoch   uuid.UUID `gorm:"uniqueIndex:idx_group_keys_group_epoch,priority:2"`
	Index   int
	Key     []byte
	KeyID   []byte `gorm:"index"`
	Secret  []byte
	SavedAt int64
}

func (gk *groupKey) BeforeCreate(tx *gorm.DB) error {
	if gk.ID == uuid.Nil {
		gk.ID = uuid.New()
	}
	gk.SavedAt = time.Now().Unix()
	return nil
}

// What a member hands to a new member (in an invite) or to a member who is missing keys (in a key response)
type groupKeyExport struct {
	Epoch uuid.UUID
	Index int
	Key   []byte
}

type groupKeyChainExport struct {
	Keys []groupKeyExport
}

// groupKeyIDFor names a key without revealing it, so sealed frames can say which key they used
func groupKeyIDFor(key []byte) []byte {
	sum := blake3.Sum256(key)
	return sum[:16]
}

func initialGroupKey(secret []byte) []byte {
	key, err := hkdf.Key(sha256.New, secret, nil, groupKeyInitialInfo, groupKeySize)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error deriving initial group key")
	}
	return key
}

func ratchetGroupKey(previous []byte, secret []byte) []byte {
	key, err := hkdf.Key(sha256.New, previous, secret, groupKeyRatchetInfo, groupKeySize)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error ratcheting group key")
	}
	return key
}

// newGroupKeySecret returns a fresh secret and its commitment hash
func newGroupKeySecret() ([]byte, []byte) {
	secret := make([]byte, groupKeySize)
	_, err := rand.Read(secret)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error generating group key secret")
	}
	hash := blake3.Sum256(secret)
	return secret, hash[:]
}

func groupKeySecretMatchesHash(secret []byte, hash []byte) bool {
	sum := blake3.Sum256(secret)
	return bytes.Equal(sum[:], hash)
}

// carriesGroupKeySecret reports whether this update carries a key rotation that members can ratchet on
func (ug *updateGroup) carriesGroupKeySecret() bool {
	return len(ug.KeyEphemeralKey) == 32 && len(ug.KeySecretHash) == 32 && len(ug.KeyWraps) > 0
}

func groupKeyWrapAdditionalData(domain string, userID uuid.UUID) []byte {
	return append([]byte(domain), userID[:]...)
}

// wrapGroupKeyMaterial encrypts the same plaintext once for each recipient under a key agreed between a fresh
// ephemeral key and that recipient's user ECDH key.  It returns the ephemeral public key the recipients need and
// the wraps.  Recipients without an ECDH public key are skipped, since nothing can be wrapped for them.
func wrapGroupKeyMaterial(plaintext []byte, domain string, recipients []user) ([]byte, []groupKeyWrap, error) {
	curve := ecdh.X25519()
	ephemeral, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}

	wraps := []groupKeyWrap{}
	for _, recipient := range recipients {
		if len(recipient.PublicECDHKey) == 0 {
			log.WithFields(log.Fields{
				"user_id": recipient.ID,
			}).Warn("cannot wrap group key material for user without an ECDH key")
			continue
		}
		recipientKey, err := curve.NewPublicKey(recipient.PublicECDHKey)
		if err != nil {
			log.WithFields(log.Fields{
				"user_id": recipient.ID,
				"error":   err.Error(),
			}).Warn("cannot wrap group key material for user with invalid ECDH key")
			continue
		}
		shared, err := ephemeral.ECDH(recipientKey)
		if err != nil {
			return nil, nil, err
		}
		kek, err := hkdf.Key(sha256.New, shared, nil, groupKeyWrapInfo, groupKeySize)
		if err != nil {
			return nil, nil, err
		}
		block, err := aes.NewCipher(kek)
		if err != nil {
			return nil, nil, err
		}
		gcm, err := cipher.NewGCMWithRandomNonce(block)
		if err != nil {
			return nil, nil, err
		}
		wraps = append(wraps, groupKeyWrap{
			UserID:     recipient.ID,
			Ciphertext: gcm.Seal(nil, []byte{}, plaintext, groupKeyWrapAdditionalData(domain, recipient.ID)),
		})
	}

	return ephemeral.PublicKey().Bytes(), wraps, nil
}

// unwrapGroupKeyMaterial finds and opens the wrap addressed to the given user, using the given user private key
func unwrapGroupKeyMaterial(ephemeralPublicKey []byte, wraps []groupKeyWrap, domain string, userID uuid.UUID, privateKey []byte) ([]byte, bool) {
	var mine *groupKeyWrap
	for i := range wraps {
		if wraps[i].UserID == userID {
			mine = &wraps[i]
			break
		}
	}
	if mine == nil {
		return nil, false
	}

	curve := ecdh.X25519()
	ephemeral, err := curve.NewPublicKey(ephemeralPublicKey)
	if err != nil {
		return nil, false
	}
	private, err := curve.NewPrivateKey(privateKey)
	if err != nil {
		return nil, false
	}
	shared, err := private.ECDH(ephemeral)
	if err != nil {
		return nil, false
	}
	kek, err := hkdf.Key(sha256.New, shared, nil, groupKeyWrapInfo, groupKeySize)
	if err != nil {
		return nil, false
	}
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, false
	}
	gcm, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, false
	}
	plaintext, err := gcm.Open(nil, []byte{}, mine.Ciphertext, groupKeyWrapAdditionalData(domain, userID))
	if err != nil {
		return nil, false
	}
	return plaintext, true
}

// groupKeyRecipients returns the users who should be able to derive the group's next key: the members and invitees
// of the given state, minus the user being removed
func (b *Bounce) groupKeyRecipients(gs groupState, exclude uuid.UUID) []user {
	ids := []uuid.UUID{}
	for _, userID := range gs.users {
		if userID != exclude {
			ids = append(ids, userID)
		}
	}
	for _, userID := range gs.invites {
		if userID != exclude {
			ids = append(ids, userID)
		}
	}
	if len(ids) == 0 {
		return []user{}
	}

	var users []user
	err := b.database.Select("id", "public_ecdh_key").Where("id IN ?", ids).Find(&users).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up group key recipients")
	}
	return users
}

// attachInitialGroupKey establishes a new group's first key: a fresh secret wrapped for the creator, who is the
// only member at creation.  All of the creator's devices share the user ECDH key, so they can all open it.  Must be
// called before the creation is signed.
func (b *Bounce) attachInitialGroupKey(gc *groupCreation, creator user) error {
	secret, hash := newGroupKeySecret()
	ephemeral, wraps, err := wrapGroupKeyMaterial(secret, groupKeyDomainSecret, []user{creator})
	if err != nil {
		return err
	}
	gc.KeyEphemeralKey = ephemeral
	gc.KeyWraps = wraps
	gc.KeySecretHash = hash
	return nil
}

// attachGroupKeySecret puts a fresh key rotation on an update that shrinks the membership or explicitly rotates,
// wrapped for everyone who should still be able to follow the chain
func (b *Bounce) attachGroupKeySecret(ug *updateGroup, gs groupState, exclude uuid.UUID) error {
	secret, hash := newGroupKeySecret()
	recipients := b.groupKeyRecipients(gs, exclude)
	ephemeral, wraps, err := wrapGroupKeyMaterial(secret, groupKeyDomainSecret, recipients)
	if err != nil {
		return err
	}
	ug.KeyEphemeralKey = ephemeral
	ug.KeyWraps = wraps
	ug.KeySecretHash = hash
	return nil
}

// attachGroupKeyChain puts our whole view of the group's key chain on an invite, wrapped for the invitee, so that
// they can read everything in the group that is stored on encrypted devices.  New members receive the group's
// history over direct connections anyway, so handing them the keys to the same history on encrypted devices
// reveals nothing extra.
func (b *Bounce) attachGroupKeyChain(ug *updateGroup, invitee user) error {
	chain := b.groupKeyChain(ug.Target)
	if len(chain) == 0 {
		// We do not hold the keys ourselves, the invitee will ask another member for them
		return nil
	}
	export := groupKeyChainExport{}
	for _, gk := range chain {
		export.Keys = append(export.Keys, groupKeyExport{Epoch: gk.Epoch, Index: gk.Index, Key: gk.Key})
	}
	plaintext, err := msgpack.Marshal(export)
	if err != nil {
		return err
	}
	ephemeral, wraps, err := wrapGroupKeyMaterial(plaintext, groupKeyDomainChain, []user{invitee})
	if err != nil {
		return err
	}
	ug.KeyEphemeralKey = ephemeral
	ug.KeyWraps = wraps
	return nil
}

// groupKeyChain returns every key we hold for a group, oldest first
func (b *Bounce) groupKeyChain(groupID uuid.UUID) []groupKey {
	var keys []groupKey
	err := b.database.Where("group_id = ?", groupID).Order("`index` asc").Find(&keys).Error
	if err != nil {
		log.WithFields(log.Fields{
			"group_id": groupID,
			"error":    err.Error(),
		}).Fatal("database error loading group keys")
	}
	return keys
}

// currentGroupKey returns the newest key we hold for a group
func (b *Bounce) currentGroupKey(groupID uuid.UUID) (groupKey, bool) {
	var gk groupKey
	err := b.database.Where("group_id = ?", groupID).Order("`index` desc").Take(&gk).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"group_id": groupID,
				"error":    err.Error(),
			}).Fatal("database error loading current group key")
		}
		return groupKey{}, false
	}
	return gk, true
}

// groupKeyByID finds the key a frame was sealed under.  Usually it is one of the keys in our chain.  When two
// members rotated concurrently, a frame may have been sealed under an intermediate key that is not on the canonical
// chain: the sender had applied one rotation but not the other.  Every member can still reconstruct those states,
// because every secret is wrapped for every member, so on a miss we try ratcheting each key we hold with each
// secret we hold, one and two steps deep.  Concurrent rotations are rare, so this is a handful of KDFs.
func (b *Bounce) groupKeyByID(groupID uuid.UUID, keyID []byte) ([]byte, bool) {
	var gk groupKey
	err := b.database.Where("group_id = ? AND key_id = ?", groupID, keyID).Take(&gk).Error
	if err == nil {
		return gk.Key, true
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"group_id": groupID,
			"error":    err.Error(),
		}).Fatal("database error looking up group key by id")
	}

	chain := b.groupKeyChain(groupID)
	secrets := [][]byte{}
	for _, k := range chain {
		if len(k.Secret) > 0 {
			secrets = append(secrets, k.Secret)
		}
	}
	for _, base := range chain {
		for i, first := range secrets {
			candidate := ratchetGroupKey(base.Key, first)
			if bytes.Equal(groupKeyIDFor(candidate), keyID) {
				return candidate, true
			}
			for j, second := range secrets {
				if i == j {
					continue
				}
				deeper := ratchetGroupKey(candidate, second)
				if bytes.Equal(groupKeyIDFor(deeper), keyID) {
					return deeper, true
				}
			}
		}
	}

	return nil, false
}

// storeGroupKeys saves derived keys, replacing any existing key for the same epoch
func (b *Bounce) storeGroupKeys(groupID uuid.UUID, keys []groupKey) {
	for i := range keys {
		keys[i].GroupID = groupID
		keys[i].KeyID = groupKeyIDFor(keys[i].Key)
		err := b.database.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "group_id"}, {Name: "epoch"}},
			DoUpdates: clause.AssignmentColumns([]string{"index", "key", "key_id", "secret"}),
		}).Create(&keys[i]).Error
		if err != nil {
			log.WithFields(log.Fields{
				"group_id": groupID,
				"error":    err.Error(),
			}).Fatal("database error saving group key")
		}
	}
}

// groupKeyRequestTimes tracks when we last asked other members for a group's keys, so that we ask once rather than
// on every consensus write.  Keyed by our own address as well as the group, since tests run several engines in one
// process.
var groupKeyRequestTimes = map[string]int64{}
var groupKeyRequestMutex sync.Mutex

const groupKeyRequestInterval = int64(60)

// updateGroupKeys derives the chain of keys for a group from its canonical history and stores them.  It is called
// with the consensus store locked, so it must not take that lock again.  Our entry point into the chain is either
// the group creation (if it wrapped the initial secret for us) or the most recent invite of us (which wrapped the
// chain as the inviter held it); from there we ratchet through every epoch in the final state.  Any epoch we cannot
// unwrap stops the derivation and triggers a request to the other members.
func (b *Bounce) updateGroupKeys(groupID uuid.UUID, cs *canonicalStack) {
	me, ok := b.currentUser()
	if !ok || len(me.PrivateECDHKey) == 0 {
		return
	}
	final, err := cs.top()
	if err != nil {
		return
	}
	if !final.isMember(me.ID) && !final.isInvited(me.ID) {
		return
	}

	// Collect the frames in history by ID
	framesByID := map[uuid.UUID]updateGroup{}
	for _, gs := range cs.history[1:] {
		framesByID[gs.ug.ID] = gs.ug
	}

	// Start from whatever we already hold
	held := map[uuid.UUID]groupKey{}
	for _, gk := range b.groupKeyChain(groupID) {
		held[gk.Epoch] = gk
	}

	// Find our entry point if we hold nothing yet
	if _, ok := held[groupID]; !ok {
		var gc groupCreation
		err := b.database.Where("id = ?", groupID).First(&gc).Error
		if err == nil {
			if secret, ok := unwrapGroupKeyMaterial(gc.KeyEphemeralKey, gc.KeyWraps, groupKeyDomainSecret, me.ID, me.PrivateECDHKey); ok {
				if len(gc.KeySecretHash) == 0 || groupKeySecretMatchesHash(secret, gc.KeySecretHash) {
					held[groupID] = groupKey{Epoch: groupID, Index: 0, Key: initialGroupKey(secret), Secret: nil}
				}
			}
		}
	}
	if len(held) == 0 {
		// Look for the most recent invite of us that carried the chain
		for i := len(cs.history) - 1; i >= 1; i-- {
			ug := cs.history[i].ug
			if ug.Type != updateGroupTypeInviteUser || len(ug.KeyWraps) == 0 {
				continue
			}
			var invited user
			if err := msgpack.Unmarshal(ug.Data, &invited); err != nil || invited.ID != me.ID {
				continue
			}
			plaintext, ok := unwrapGroupKeyMaterial(ug.KeyEphemeralKey, ug.KeyWraps, groupKeyDomainChain, me.ID, me.PrivateECDHKey)
			if !ok {
				continue
			}
			var export groupKeyChainExport
			if err := msgpack.Unmarshal(plaintext, &export); err != nil {
				continue
			}
			for _, k := range export.Keys {
				if len(k.Key) == groupKeySize {
					held[k.Epoch] = groupKey{Epoch: k.Epoch, Index: k.Index, Key: k.Key}
				}
			}
			break
		}
	}
	if len(held) == 0 {
		b.requestGroupKey(groupID)
		return
	}

	// Walk the epochs of the final state in order, deriving anything we do not hold yet
	derived := []groupKey{}
	previous, ok := held[groupID]
	if !ok {
		// Our export did not include the initial key; start from the earliest key we were given
		lowest := -1
		for _, gk := range held {
			if lowest == -1 || gk.Index < lowest {
				lowest = gk.Index
				previous = gk
			}
		}
	}
	derived = append(derived, previous)
	complete := true
	for index, epoch := range final.keyEpochs {
		// Prefer to recompute from the secret: the canonical order of the epochs before this one may have changed
		// since we last derived (a concurrent rotation sorted in front), which changes every key after it.  A key
		// we were handed whole, without its secret, is trusted as it is.
		secret := []byte{}
		if gk, ok := held[epoch]; ok && len(gk.Secret) > 0 {
			secret = gk.Secret
		} else if ug, ok := framesByID[epoch]; ok && ug.carriesGroupKeySecret() {
			unwrapped, ok := unwrapGroupKeyMaterial(ug.KeyEphemeralKey, ug.KeyWraps, groupKeyDomainSecret, me.ID, me.PrivateECDHKey)
			if ok {
				if !groupKeySecretMatchesHash(unwrapped, ug.KeySecretHash) {
					log.WithFields(log.Fields{
						"group_id": groupID,
						"frame_id": ug.ID,
						"actor":    ug.Actor,
					}).Error("group key secret does not match its commitment, the author gave different members different secrets")
					complete = false
					break
				}
				secret = unwrapped
			}
		}

		if len(secret) > 0 {
			if previous.Index != index {
				// We are missing an earlier link, nothing after it can be derived
				complete = false
				break
			}
			previous = groupKey{Epoch: epoch, Index: index + 1, Key: ratchetGroupKey(previous.Key, secret), Secret: secret}
			derived = append(derived, previous)
			continue
		}

		if gk, ok := held[epoch]; ok {
			gk.Index = index + 1
			previous = gk
			derived = append(derived, gk)
			continue
		}

		complete = false
		break
	}

	b.storeGroupKeys(groupID, derived)

	if !complete {
		b.requestGroupKey(groupID)
	}
}

// deriveGroupKeys recomputes a group's keys from its current canonical stack, for callers that are not already
// inside a consensus write
func (b *Bounce) deriveGroupKeys(groupID uuid.UUID) {
	stack, err := b.currentGroupStack(groupID)
	if err != nil {
		return
	}
	b.consensusStore.Lock()
	defer b.consensusStore.Unlock()
	b.updateGroupKeys(groupID, stack)
}

// ensureGroupKeyFresh rotates the group key if the membership shrank without a trusted rotation, which happens when
// a user leaves on their own.  Honest members call this before sealing anything new for the group.
func (b *Bounce) ensureGroupKeyFresh(groupID uuid.UUID) error {
	gs, err := b.currentGroupState(groupID)
	if err != nil {
		return err
	}
	if !gs.keyStale {
		return nil
	}
	if !gs.isMember(b.currentUserID()) {
		return errUserNotInGroup
	}
	return b.rotateGroupKey(groupID)
}

// rotateGroupKey adds a fresh secret to the chain, wrapped for every current member and invitee
func (b *Bounce) rotateGroupKey(groupID uuid.UUID) error {
	gs, err := b.currentGroupState(groupID)
	if err != nil {
		return err
	}
	// The rotation must sort after the update it answers (a departure in the same second would otherwise be
	// ordered after it by consensus and leave the chain stale)
	timestamp := time.Now().Unix()
	if gs.ug.Timestamp >= timestamp {
		timestamp = gs.ug.Timestamp + 1
	}
	ug := &updateGroup{
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: timestamp,
		Type:      updateGroupTypeRotateKey,
	}
	err = b.attachGroupKeySecret(ug, gs, uuid.Nil)
	if err != nil {
		return err
	}
	return b.applyAndBroadcastUpdateGroup(ug)
}

// Key recovery
//
// A member can find themselves without the keys they need: they were omitted from a rotation by an author with a
// stale view of the membership, they were invited by a member who did not hold the chain, or they are catching up
// through an encrypted device that only has ciphertext.  They ask the other members, and any member who is caught
// up answers with the chain wrapped for them.

type groupKeyRequest struct {
	GroupID   uuid.UUID
	Timestamp int64
}

func (gkr *groupKeyRequest) getType() uint16 {
	return typeGroupKeyRequest
}

func (gkr *groupKeyRequest) getPayload() []byte {
	payload, err := msgpack.Marshal(gkr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal group key request")
	}
	return payload
}

type groupKeyResponse struct {
	GroupID         uuid.UUID
	KeyEphemeralKey []byte
	Wrap            groupKeyWrap
}

func (gkr *groupKeyResponse) getType() uint16 {
	return typeGroupKeyResponse
}

func (gkr *groupKeyResponse) getPayload() []byte {
	payload, err := msgpack.Marshal(gkr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal group key response")
	}
	return payload
}

// requestGroupKey asks the online members of a group for its keys, at most once a minute per group
func (b *Bounce) requestGroupKey(groupID uuid.UUID) {
	requestKey := b.network.Address() + groupID.String()
	groupKeyRequestMutex.Lock()
	last, ok := groupKeyRequestTimes[requestKey]
	now := time.Now().Unix()
	if ok && now-last < groupKeyRequestInterval {
		groupKeyRequestMutex.Unlock()
		return
	}
	groupKeyRequestTimes[requestKey] = now
	groupKeyRequestMutex.Unlock()

	// The caller may hold the consensus store lock, so finish asynchronously
	go func() {
		gs, err := b.currentGroupState(groupID)
		if err != nil {
			return
		}
		request := &groupKeyRequest{GroupID: groupID, Timestamp: now}
		for _, userID := range gs.users {
			if userID == b.currentUserID() {
				continue
			}
			for _, addr := range gs.devices[userID] {
				if b.getRemoteDevice(addr).connectedSockets() > 0 {
					b.sendDirect(addr, request)
				}
			}
		}
	}()
}

func (b *Bounce) handleGroupKeyRequest(peer string, payload []byte, _ bool) (broadcastable, bool) {
	var request groupKeyRequest
	err := msgpack.Unmarshal(payload, &request)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling group key request")
		return nil, false
	}

	// The requester must be a current member or invitee of the group, according to our consensus
	peerDevice, ok := b.getDeviceFromAddress(peer)
	if !ok {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("ignoring group key request from unknown device")
		return nil, false
	}
	gs, err := b.currentGroupState(request.GroupID)
	if err != nil {
		return nil, false
	}
	if !gs.isMember(peerDevice.UserID) && !gs.isInvited(peerDevice.UserID) {
		log.WithFields(log.Fields{
			"peer":     peer,
			"group_id": request.GroupID,
		}).Warn("ignoring group key request from a user who is not in the group")
		return nil, false
	}
	if !gs.isMember(b.currentUserID()) {
		return nil, false
	}

	chain := b.groupKeyChain(request.GroupID)
	if len(chain) == 0 {
		return nil, false
	}
	var requester user
	err = b.database.Select("id", "public_ecdh_key").First(&requester, "id = ?", peerDevice.UserID).Error
	if err != nil {
		return nil, false
	}

	export := groupKeyChainExport{}
	for _, gk := range chain {
		export.Keys = append(export.Keys, groupKeyExport{Epoch: gk.Epoch, Index: gk.Index, Key: gk.Key})
	}
	plaintext, err := msgpack.Marshal(export)
	if err != nil {
		return nil, false
	}
	ephemeral, wraps, err := wrapGroupKeyMaterial(plaintext, groupKeyDomainResponse, []user{requester})
	if err != nil || len(wraps) != 1 {
		return nil, false
	}

	b.sendDirect(peer, &groupKeyResponse{
		GroupID:         request.GroupID,
		KeyEphemeralKey: ephemeral,
		Wrap:            wraps[0],
	})
	return nil, false
}

func (b *Bounce) handleGroupKeyResponse(peer string, payload []byte, _ bool) (broadcastable, bool) {
	var response groupKeyResponse
	err := msgpack.Unmarshal(payload, &response)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling group key response")
		return nil, false
	}

	// Only accept keys from a member of the group
	peerDevice, ok := b.getDeviceFromAddress(peer)
	if !ok {
		return nil, false
	}
	gs, err := b.currentGroupState(response.GroupID)
	if err != nil {
		return nil, false
	}
	if !gs.isMember(peerDevice.UserID) {
		log.WithFields(log.Fields{
			"peer":     peer,
			"group_id": response.GroupID,
		}).Warn("ignoring group key response from a user who is not in the group")
		return nil, false
	}

	me, ok := b.currentUser()
	if !ok {
		return nil, false
	}
	plaintext, ok := unwrapGroupKeyMaterial(response.KeyEphemeralKey, []groupKeyWrap{response.Wrap}, groupKeyDomainResponse, me.ID, me.PrivateECDHKey)
	if !ok {
		log.WithFields(log.Fields{
			"peer":     peer,
			"group_id": response.GroupID,
		}).Warn("could not open group key response")
		return nil, false
	}
	var export groupKeyChainExport
	err = msgpack.Unmarshal(plaintext, &export)
	if err != nil {
		return nil, false
	}

	keys := []groupKey{}
	for _, k := range export.Keys {
		if len(k.Key) != groupKeySize {
			continue
		}
		keys = append(keys, groupKey{Epoch: k.Epoch, Index: k.Index, Key: k.Key})
	}
	b.storeGroupKeys(response.GroupID, keys)

	// Now that we have an entry point, derive anything newer from the stack
	b.deriveGroupKeys(response.GroupID)

	return nil, false
}
