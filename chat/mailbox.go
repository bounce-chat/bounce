package chat

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/zeebo/blake3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Mailboxes
//
// An encrypted device stores frames it cannot read.  Before this, each stored frame named every recipient by their
// long term public key, so the host could see who talks to whom, who wrote what, which conversation a frame belongs
// to, and when.  Now a frame on a host is addressed to mailboxes instead.  A mailbox label is a pseudorandom value
// derived from a secret that only the participants of a conversation hold - a group's key chain, the ECDH agreement
// between two users for their DM, a user's own key for their sync devices - together with an epoch, so labels rotate
// every week.  The host indexes frames by label and serves a frame to any peer that asks for that label; presenting
// an unguessable label is the proof of membership.  The host learns that some connection fetched some labels, and
// how many frames each label holds, and nothing about who or what.
//
// Each stored frame carries, for every mailbox it is in, the data encryption key wrapped under a key derived from the
// same secret and epoch.  A group of five hundred members costs one mailbox entry, because every member derives the
// same group key, which is what removes the old cap on recipients.
//
// On the host, a frame's ID is a random value chosen by the sender for that copy, its type is hidden inside the
// ciphertext, and there is no timestamp or conversation identifier: the host orders frames by when it received them.
// Because the ID is per copy, a frame that two devices both relay to the same host would be stored twice; each
// mailbox entry therefore carries a tag derived from the mailbox secret and the frame's real ID, which lets the
// host recognise a copy of a frame it already holds without being able to tell which frame that is.

const mailboxEpochSeconds = int64(7 * 24 * 60 * 60)

// How many epochs back a device looks when asking a host for its mailboxes.  Frames older than this on a host are
// out of reach of the devices, which makes it the host's effective retention.
const mailboxLookbackEpochs = 13

const mailboxLabelSize = 32
const mailboxTagSize = 16

const mailboxLabelInfo = "bounce-mailbox-label"
const mailboxWrapInfo = "bounce-mailbox-wrap"
const mailboxTagInfo = "bounce-mailbox-tag"
const dmThreadInfo = "bounce-dm-thread"
const syncThreadInfo = "bounce-sync-thread"
const deviceThreadInfo = "bounce-device-thread"

var errNoMailboxForLabel = errors.New("no mailbox secret for label")
var errNothingToSealTo = errors.New("no mailboxes to seal the frame to")

const (
	mailboxKindSync = iota
	mailboxKindDM
	mailboxKindGroup
	mailboxKindDevice
)

// A mailbox the local device can read: the secret and epoch its label derives from
type mailbox struct {
	Label  []byte
	Secret []byte
	Epoch  int64
	Kind   int
	Thread uuid.UUID // the counterparty user, the group, or nil
}

type mailboxIndex struct {
	byLabel map[string]mailbox
	all     []mailbox
}

func (mi *mailboxIndex) labels() [][]byte {
	labels := make([][]byte, 0, len(mi.all))
	for _, mb := range mi.all {
		labels = append(labels, mb.Label)
	}
	return labels
}

func (mi *mailboxIndex) lookup(label []byte) (mailbox, bool) {
	mb, ok := mi.byLabel[string(label)]
	return mb, ok
}

func mailboxEpoch(timestamp int64) int64 {
	return timestamp / mailboxEpochSeconds
}

// mailboxClock is what the current epoch is computed from; a var so tests can move time
var mailboxClock = func() int64 { return time.Now().Unix() }

func currentMailboxEpoch() int64 {
	return mailboxEpoch(mailboxClock())
}

func epochBytes(epoch int64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(epoch))
	return buf
}

func mailboxLabel(secret []byte, epoch int64) []byte {
	label, err := hkdf.Key(sha256.New, secret, epochBytes(epoch), mailboxLabelInfo, mailboxLabelSize)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error deriving mailbox label")
	}
	return label
}

func mailboxWrapKey(secret []byte, epoch int64) []byte {
	key, err := hkdf.Key(sha256.New, secret, epochBytes(epoch), mailboxWrapInfo, 32)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error deriving mailbox wrap key")
	}
	return key
}

// mailboxTag identifies one frame within one mailbox across epochs and copies, to anyone holding the secret
func mailboxTag(secret []byte, frameID uuid.UUID) []byte {
	tag, err := hkdf.Key(sha256.New, secret, frameID[:], mailboxTagInfo, mailboxTagSize)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error deriving mailbox tag")
	}
	return tag
}

func deriveThreadSecret(shared []byte, info string) []byte {
	secret, err := hkdf.Key(sha256.New, shared, nil, info, 32)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error deriving thread secret")
	}
	return secret
}

// syncThreadSecret is the secret shared by all of one user's devices
func syncThreadSecret(privateECDHKey []byte) []byte {
	return deriveThreadSecret(privateECDHKey, syncThreadInfo)
}

// dmThreadSecret is the secret shared by two users, derived the same way from either side
func (b *Bounce) dmThreadSecret(privateECDHKey []byte, counterpartyPublicKey []byte) ([]byte, error) {
	shared, err := b.generateKEKFromPrivateKey(privateECDHKey, counterpartyPublicKey)
	if err != nil {
		return nil, err
	}
	return deriveThreadSecret(shared, dmThreadInfo), nil
}

// deviceThreadSecret is the secret shared by two devices of the same user, used for re-key frames that must not be
// readable with the user key that is being replaced
func (b *Bounce) deviceThreadSecret(myDevicePrivateKey []byte, otherDevicePublicKey []byte) ([]byte, error) {
	shared, err := b.generateKEKFromPrivateKey(myDevicePrivateKey, otherDevicePublicKey)
	if err != nil {
		return nil, err
	}
	return deriveThreadSecret(shared, deviceThreadInfo), nil
}

// groupThreadSecrets returns every key under which a group's frames might be sealed: the canonical chain plus the
// intermediate keys concurrent rotations can produce, so that a frame sealed by a member who had applied one
// rotation but not another is still found and opened
func (b *Bounce) groupThreadSecrets(groupID uuid.UUID) [][]byte {
	chain := b.groupKeyChain(groupID)
	secrets := [][]byte{}
	seen := map[string]bool{}
	add := func(key []byte) {
		if !seen[string(key)] {
			seen[string(key)] = true
			secrets = append(secrets, key)
		}
	}
	rotationSecrets := [][]byte{}
	for _, gk := range chain {
		add(gk.Key)
		if len(gk.Secret) > 0 {
			rotationSecrets = append(rotationSecrets, gk.Secret)
		}
	}
	for _, gk := range chain {
		for i, first := range rotationSecrets {
			candidate := ratchetGroupKey(gk.Key, first)
			add(candidate)
			for j, second := range rotationSecrets {
				if i != j {
					add(ratchetGroupKey(candidate, second))
				}
			}
		}
	}
	return secrets
}

// A secret a frame can be sealed to, and whose conversation it is: a group's key (no user), the secret shared with
// one other user for their DM, or our own sync secret (our user)
type threadSecret struct {
	secret []byte
	user   uuid.UUID
}

// threadSecretsFor returns every secret a frame could be sealed to, one per conversation it belongs to, according
// to its scope.  Group frames use the group's current key (rotating it first if the membership shrank without a
// rotation); frames to other users use the secret shared with each of them.  Our own devices derive every one of
// those secrets too, so the sync secret is only used for frames that have no conversation at all.
func (b *Bounce) threadSecretsFor(br broadcastable) ([]threadSecret, error) {
	me, ok := b.currentUser()
	if !ok {
		return nil, errUserNotFound
	}

	secrets := []threadSecret{}
	addUsers := func(users []user) {
		for _, u := range users {
			if u.ID == me.ID || len(u.PublicECDHKey) == 0 {
				continue
			}
			secret, err := b.dmThreadSecret(me.PrivateECDHKey, u.PublicECDHKey)
			if err != nil {
				log.WithFields(log.Fields{
					"user_id": u.ID,
					"error":   err.Error(),
				}).Warn("cannot derive DM thread secret for user")
				continue
			}
			secrets = append(secrets, threadSecret{secret: secret, user: u.ID})
		}
	}

	scope := br.getScope(me.ID)
	switch scope {
	case scopeSync:
		secrets = append(secrets, threadSecret{secret: syncThreadSecret(me.PrivateECDHKey), user: me.ID})
	case scopeUser, scopeGlobal, scopeCustom:
		addUsers(b.getUsersInScope(br))
	case scopeGroup, scopeGroupWithInvites:
		groupID := br.getDestination(me.ID)
		if err := b.ensureGroupKeyFresh(groupID); err != nil && !errors.Is(err, errUserNotInGroup) {
			log.WithFields(log.Fields{
				"group_id": groupID,
				"error":    err.Error(),
			}).Warn("could not rotate stale group key before sealing")
		}
		gk, ok := b.currentGroupKey(groupID)
		if !ok {
			return nil, errGroupKeyUnavailable
		}
		secrets = append(secrets, threadSecret{secret: gk.Key})
	default:
		return nil, errors.New("unknown scope for sealing")
	}

	if len(secrets) == 0 {
		secrets = append(secrets, threadSecret{secret: syncThreadSecret(me.PrivateECDHKey), user: me.ID})
	}
	return secrets, nil
}

// encryptedDeviceOwner returns the user an encrypted device belongs to, or nil if we do not know
func (b *Bounce) encryptedDeviceOwner(host string) uuid.UUID {
	encryptedDeviceCacheMutex.Lock()
	owner, ok := encryptedDeviceCache[host]
	encryptedDeviceCacheMutex.Unlock()
	if ok {
		return owner
	}
	var esd encryptedSyncDevice
	if b.database.Select("id").First(&esd, "address = ?", host).Error == nil {
		return b.currentUserID()
	}
	return uuid.Nil
}

// secretsForOwner picks, from everything a frame could be sealed to, the mailboxes to use on a host that belongs
// to owner.  Our own hosts get every mailbox, so our contacts can fetch from them while we are offline.  Another
// user's host only gets the mailboxes that user can read: the groups we share and the DM between us.  That keeps a
// host from holding entries for conversations its owner is not part of, from learning how many conversations a
// frame fans out to, and lets its owner clear a conversation without leaving copies behind in mailboxes it cannot
// name.
func (b *Bounce) secretsForOwner(secrets []threadSecret, owner uuid.UUID) [][]byte {
	chosen := [][]byte{}
	ours := owner == uuid.Nil || owner == b.currentUserID()
	for _, ts := range secrets {
		if ours || ts.user == uuid.Nil || ts.user == owner {
			chosen = append(chosen, ts.secret)
		}
	}
	return chosen
}

// hostSecretsFor returns the secrets to seal a frame to for one encrypted device
func (b *Bounce) hostSecretsFor(br broadcastable, host string) ([][]byte, error) {
	secrets, err := b.threadSecretsFor(br)
	if err != nil {
		return nil, err
	}
	chosen := b.secretsForOwner(secrets, b.encryptedDeviceOwner(host))
	if len(chosen) == 0 {
		return nil, errNothingToSealTo
	}
	return chosen, nil
}

// currentLabels derives the current epoch's mailbox labels for a set of secrets
func currentLabels(secrets [][]byte) [][]byte {
	epoch := currentMailboxEpoch()
	labels := make([][]byte, 0, len(secrets))
	for _, secret := range secrets {
		labels = append(labels, mailboxLabel(secret, epoch))
	}
	return labels
}

// mailboxIndex builds every mailbox this device can read for the recent epochs: its own sync mailbox, a DM mailbox
// per contact, a mailbox per key of every group it is in, and a device mailbox per sibling device, each for the
// current epoch, the next one, and the lookback window.  This is what a device asks a host for, and what it uses
// to open what comes back.
func (b *Bounce) mailboxIndex() *mailboxIndex {
	index := &mailboxIndex{byLabel: map[string]mailbox{}}
	me, ok := b.currentUser()
	if !ok || len(me.PrivateECDHKey) == 0 {
		return index
	}

	// Our current key and any we have replaced, so frames sealed to a previous key still open
	privateKeys := [][]byte{me.PrivateECDHKey}
	for _, old := range b.encryptionKeyHistory() {
		if !bytes.Equal(old, me.PrivateECDHKey) {
			privateKeys = append(privateKeys, old)
		}
	}

	type source struct {
		secret []byte
		kind   int
		thread uuid.UUID
	}
	sources := []source{}
	for _, priv := range privateKeys {
		sources = append(sources, source{secret: syncThreadSecret(priv), kind: mailboxKindSync})
	}

	var contacts []user
	err := b.database.Select("id", "public_ecdh_key").Where("profile = ? AND length(public_ecdh_key) > 0", false).Find(&contacts).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error loading contacts for mailbox index")
	}
	for _, contact := range contacts {
		for _, priv := range privateKeys {
			secret, err := b.dmThreadSecret(priv, contact.PublicECDHKey)
			if err != nil {
				continue
			}
			sources = append(sources, source{secret: secret, kind: mailboxKindDM, thread: contact.ID})
		}
	}

	var groupIDs []uuid.UUID
	err = b.database.Model(&groupKey{}).Distinct("group_id").Pluck("group_id", &groupIDs).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error loading group ids for mailbox index")
	}
	for _, groupID := range groupIDs {
		for _, secret := range b.groupThreadSecrets(groupID) {
			sources = append(sources, source{secret: secret, kind: mailboxKindGroup, thread: groupID})
		}
	}

	if myDevice, ok := b.getDeviceFromAddress(b.network.Address()); ok && len(myDevice.ECDHPrivateKey) > 0 {
		var siblings []device
		err = b.database.Where("user_id = ? AND address != ?", me.ID, b.network.Address()).Find(&siblings).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error loading sibling devices for mailbox index")
		}
		for _, sibling := range siblings {
			if len(sibling.ECDHPublicKey) == 0 {
				continue
			}
			secret, err := b.deviceThreadSecret(myDevice.ECDHPrivateKey, sibling.ECDHPublicKey)
			if err != nil {
				continue
			}
			sources = append(sources, source{secret: secret, kind: mailboxKindDevice, thread: sibling.ID})
		}
	}

	current := currentMailboxEpoch()
	for _, src := range sources {
		for epoch := current + 1; epoch >= current-mailboxLookbackEpochs && epoch >= 0; epoch-- {
			mb := mailbox{Label: mailboxLabel(src.secret, epoch), Secret: src.secret, Epoch: epoch, Kind: src.kind, Thread: src.thread}
			if _, exists := index.byLabel[string(mb.Label)]; exists {
				continue
			}
			index.byLabel[string(mb.Label)] = mb
			index.all = append(index.all, mb)
		}
	}

	return index
}

// clearLabelsForThread returns the labels of every completed epoch before a timestamp for a conversation, so that
// clearing a thread's history can be expressed to a host as "delete these mailboxes" without naming the thread.
// The thread is a DM counterparty (the xor of the two user IDs) or a group.
func (b *Bounce) clearLabelsForThread(thread uuid.UUID, before int64) [][]byte {
	me, ok := b.currentUser()
	if !ok {
		return nil
	}
	secrets := [][]byte{}
	if thread == uuid.Nil {
		secrets = append(secrets, syncThreadSecret(me.PrivateECDHKey))
	} else if len(b.groupKeyChain(thread)) > 0 {
		secrets = b.groupThreadSecrets(thread)
	} else {
		var counterparty user
		err := b.database.Select("id", "public_ecdh_key").First(&counterparty, "id = ?", xor(thread, me.ID)).Error
		if err != nil {
			return nil
		}
		secret, err := b.dmThreadSecret(me.PrivateECDHKey, counterparty.PublicECDHKey)
		if err != nil {
			return nil
		}
		secrets = append(secrets, secret)
	}

	// Only epochs that ended before the timestamp
	lastComplete := mailboxEpoch(before) - 1
	labels := [][]byte{}
	for _, secret := range secrets {
		for epoch := lastComplete; epoch >= lastComplete-mailboxLookbackEpochs && epoch >= 0; epoch-- {
			labels = append(labels, mailboxLabel(secret, epoch))
		}
	}
	return labels
}

// What is inside a sealed frame: everything a recipient needs to handle it, and nothing the host needs to see
type sealedFrame struct {
	Type    uint16
	ID      uuid.UUID
	Payload []byte
}

func sealedFrameAdditionalData(hostID uuid.UUID) []byte {
	return append([]byte("bounce-sealed-frame"), hostID[:]...)
}

func mailboxEntryAdditionalData(label []byte) []byte {
	return append([]byte("bounce-mailbox-entry"), label...)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}

// Slots
//
// Every copy on a host occupies a slot, and a host keeps at most one owner frame per slot: a newer version replaces
// an older one, and an older one arriving late is dropped.  Drafts are the frames that need this.  A device saves a
// new draft frame every time the text changes, keeps only the newest per thread, and clears a thread with an empty
// draft; without slots a host would accumulate every version and hand all of them to a device catching up.  A
// draft's slot is derived from the user's sync secret and the thread, so every device of the user derives the same
// slot, the host cannot tell which thread it is, and nobody else can derive it at all.  The version is the draft's
// own timestamp, which is what devices already compare, so two devices that come online in either order converge
// on the newest draft without knowing of each other's copies.
//
// Every other frame carries sixteen random bytes as its slot.  That slot never matches another, so the rule never
// fires for them; it exists so that drafts are not the only frames with a slot, which would let the host tell which
// of its frames are drafts and when they were typed.  A random slot is an unused field, deliberately.

const slotSize = 16
const draftSlotInfo = "bounce-draft-slot"

// What a sealed copy carries besides its mailboxes
type copySettings struct {
	DeleteAt int64
	Slot     []byte
	Version  int64
}

func draftSlot(syncSecret []byte, thread uuid.UUID) []byte {
	slot, err := hkdf.Key(sha256.New, syncSecret, thread[:], draftSlotInfo, slotSize)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error deriving draft slot")
	}
	return slot
}

func randomSlot() []byte {
	slot := make([]byte, slotSize)
	if _, err := rand.Read(slot); err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error generating random slot")
	}
	return slot
}

// sealFrame produces the envelope a host stores for one copy of a frame: a fresh host ID, the frame sealed under a
// fresh DEK, and that DEK wrapped once per mailbox for the current epoch
func sealFrame(br broadcastable, secrets [][]byte, settings copySettings) (*encryptedFrame, error) {
	hostID := uuid.New()
	plaintext, err := msgpack.Marshal(sealedFrame{Type: br.getType(), ID: br.getID(), Payload: br.getPayload()})
	if err != nil {
		return nil, err
	}

	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		return nil, err
	}
	dekGCM, err := newGCM(dek)
	if err != nil {
		return nil, err
	}
	ciphertext := dekGCM.Seal(nil, []byte{}, plaintext, sealedFrameAdditionalData(hostID))

	epoch := currentMailboxEpoch()
	entries := []mailboxEntry{}
	seen := map[string]bool{}
	for _, secret := range secrets {
		label := mailboxLabel(secret, epoch)
		if seen[string(label)] {
			continue
		}
		seen[string(label)] = true
		wrapGCM, err := newGCM(mailboxWrapKey(secret, epoch))
		if err != nil {
			return nil, err
		}
		entries = append(entries, mailboxEntry{
			Label:      label,
			Tag:        mailboxTag(secret, br.getID()),
			WrappedDEK: wrapGCM.Seal(nil, []byte{}, dek, mailboxEntryAdditionalData(label)),
		})
	}
	if len(entries) == 0 {
		return nil, errNothingToSealTo
	}

	slot := settings.Slot
	if len(slot) != slotSize {
		slot = randomSlot()
	}
	return &encryptedFrame{
		ID:        hostID,
		Payload:   ciphertext,
		DeleteAt:  settings.DeleteAt,
		Slot:      slot,
		Version:   settings.Version,
		Mailboxes: entries,
	}, nil
}

// open recovers a frame from what a host sends, using the mailbox the label belongs to
func (mi *mailboxIndex) open(er encryptedReceive) (frame, error) {
	var empty frame
	mb, ok := mi.lookup(er.Label)
	if !ok {
		return empty, errNoMailboxForLabel
	}
	wrapGCM, err := newGCM(mailboxWrapKey(mb.Secret, mb.Epoch))
	if err != nil {
		return empty, err
	}
	dek, err := wrapGCM.Open(nil, []byte{}, er.WrappedDEK, mailboxEntryAdditionalData(er.Label))
	if err != nil {
		return empty, err
	}
	dekGCM, err := newGCM(dek)
	if err != nil {
		return empty, err
	}
	plaintext, err := dekGCM.Open(nil, []byte{}, er.Payload, sealedFrameAdditionalData(er.ID))
	if err != nil {
		return empty, err
	}
	var sf sealedFrame
	err = msgpack.Unmarshal(plaintext, &sf)
	if err != nil {
		return empty, err
	}
	return frame{ID: sf.ID, Type: sf.Type, Payload: sf.Payload}, nil
}

// Sealed copies
//
// A host acks a stored copy by the random ID we gave that copy, so the sender remembers which frame each copy was
// for long enough to turn the host's ack into delivery tracking for the real frame.
type sealedCopy struct {
	reference frameReference
	sealedAt  int64
}

var sealedCopies = map[uuid.UUID]sealedCopy{}
var sealedCopiesMutex sync.Mutex

const sealedCopyMemorySeconds = int64(60 * 60)

func recordSealedCopy(hostID uuid.UUID, br broadcastable) {
	sealedCopiesMutex.Lock()
	defer sealedCopiesMutex.Unlock()
	now := time.Now().Unix()
	if len(sealedCopies) > 10000 {
		for id, copy := range sealedCopies {
			if now-copy.sealedAt > sealedCopyMemorySeconds {
				delete(sealedCopies, id)
			}
		}
	}
	sealedCopies[hostID] = sealedCopy{reference: frameReference{FrameID: br.getID(), Type: br.getType()}, sealedAt: now}
}

func sealedCopyReference(hostID uuid.UUID) (frameReference, bool) {
	sealedCopiesMutex.Lock()
	defer sealedCopiesMutex.Unlock()
	copy, ok := sealedCopies[hostID]
	if ok {
		delete(sealedCopies, hostID)
	}
	return copy.reference, ok
}

// The host side

// A mailbox request is how a device tells a host which mailboxes it can read.  The host answers with a reference
// offer of the frames in those mailboxes it has not delivered to this device, and pushes new ones as they arrive.
type mailboxRequest struct {
	Labels [][]byte
}

func (mr *mailboxRequest) getType() uint16 {
	return typeMailboxRequest
}

func (mr *mailboxRequest) getPayload() []byte {
	payload, err := msgpack.Marshal(mr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal mailbox request")
	}
	return payload
}

// The labels each connected peer has asked for, so the host knows what to offer and push
var peerMailboxLabels = map[string]map[string]bool{}
var peerMailboxLabelsMutex sync.Mutex

// A label the owner of this encrypted device can read.  Frames from peers that have not proven they are the owner
// are only stored if they are addressed to one of these, so the host stays a store for its owner and not a dead
// drop for anyone.  The owner registers them every time it connects, for the current and next epoch.
type ownerMailbox struct {
	ID      uuid.UUID `gorm:"type:uuid;primary_key;"`
	Label   []byte    `gorm:"uniqueIndex"`
	SavedAt int64
}

func (b *Bounce) peerLabels(peer string) map[string]bool {
	peerMailboxLabelsMutex.Lock()
	defer peerMailboxLabelsMutex.Unlock()
	labels := map[string]bool{}
	for label := range peerMailboxLabels[peer] {
		labels[label] = true
	}
	return labels
}

func (b *Bounce) peerCanReadLabel(peer string, label []byte) bool {
	peerMailboxLabelsMutex.Lock()
	defer peerMailboxLabelsMutex.Unlock()
	return peerMailboxLabels[peer][string(label)]
}

func (b *Bounce) ownerCanReadAnyLabel(labels [][]byte) bool {
	if len(labels) == 0 {
		return false
	}
	var count int64
	err := b.database.Model(&ownerMailbox{}).Where("label IN ?", labels).Count(&count).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error checking owner mailboxes")
	}
	return count > 0
}

// A digest of the labels we last sent each encrypted device, so that the request is re-sent whenever the set of
// mailboxes we can read changes: a new contact, a new group or group key, a sibling device, or a new epoch.  Keyed
// by our own address as well as the peer, since tests run several engines in one process.
var mailboxRequestDigests = map[string][32]byte{}
var mailboxRequestDigestsMutex sync.Mutex

func (mi *mailboxIndex) digest() [32]byte {
	labels := mi.labels()
	sort.Slice(labels, func(i, j int) bool { return bytes.Compare(labels[i], labels[j]) < 0 })
	hasher := blake3.New()
	for _, label := range labels {
		hasher.Write(label)
	}
	var digest [32]byte
	copy(digest[:], hasher.Sum(nil))
	return digest
}

// sendMailboxRequest tells an encrypted device which mailboxes we can read
func (b *Bounce) sendMailboxRequest(peer string) {
	b.sendMailboxRequestFrom(peer, b.mailboxIndex())
}

func (b *Bounce) sendMailboxRequestFrom(peer string, index *mailboxIndex) {
	if len(index.all) == 0 {
		return
	}
	mailboxRequestDigestsMutex.Lock()
	mailboxRequestDigests[b.network.Address()+peer] = index.digest()
	mailboxRequestDigestsMutex.Unlock()
	b.sendDirect(peer, &mailboxRequest{Labels: index.labels()})
}

// refreshMailboxRequests re-sends our mailboxes to every connected encrypted device whose view of them is stale.
// It runs with every peer audit, which follows adding users, joining groups and catching up, so a host learns about
// a new conversation shortly after we do rather than at the next connection.
func (b *Bounce) refreshMailboxRequests() {
	if b.encrypted {
		return
	}
	encryptedDeviceCacheMutex.Lock()
	addresses := []string{}
	for addr := range encryptedDeviceCache {
		addresses = append(addresses, addr)
	}
	encryptedDeviceCacheMutex.Unlock()

	var index *mailboxIndex
	for _, addr := range addresses {
		if b.getRemoteDevice(addr).connectedSockets() == 0 {
			continue
		}
		if index == nil {
			index = b.mailboxIndex()
		}
		mailboxRequestDigestsMutex.Lock()
		last, ok := mailboxRequestDigests[b.network.Address()+addr]
		mailboxRequestDigestsMutex.Unlock()
		if ok && last == index.digest() {
			continue
		}
		go b.sendMailboxRequestFrom(addr, index)
	}
}

// registerOwnerMailboxes records the labels a peer has asked for as the owner's, once the peer has proven it is the
// owner.  The mailbox request and the proof race on a new connection, so this runs after either arrives.
func (b *Bounce) registerOwnerMailboxes(peer string) {
	if !b.peerIsOwner(peer) {
		return
	}
	for label := range b.peerLabels(peer) {
		err := b.database.Clauses(clause.OnConflict{DoNothing: true}).Create(&ownerMailbox{ID: uuid.New(), Label: []byte(label), SavedAt: time.Now().Unix()}).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error saving owner mailbox")
		}
	}
}

func (b *Bounce) handleMailboxRequest(peer string, payload []byte, _ bool) (broadcastable, bool) {
	var request mailboxRequest
	err := msgpack.Unmarshal(payload, &request)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling mailbox request")
		return nil, false
	}

	labels := map[string]bool{}
	for _, label := range request.Labels {
		if len(label) == mailboxLabelSize {
			labels[string(label)] = true
		}
	}
	peerMailboxLabelsMutex.Lock()
	peerMailboxLabels[peer] = labels
	peerMailboxLabelsMutex.Unlock()

	// If this is the owner, remember these labels as the owner's, so frames addressed to them can be accepted
	// from anyone
	b.registerOwnerMailboxes(peer)

	b.sendDirect(peer, b.hostOfferFor(peer))
	b.sendEncryptedStorageReferenceOffer(peer)
	return nil, false
}

// peerIsOwner reports whether a peer has proven, through the reference offer challenge, that it holds the key of
// this encrypted device's owner
func (b *Bounce) peerIsOwner(peer string) bool {
	peerUserKeyMutex.Lock()
	key, ok := peerUserKeys[peer]
	peerUserKeyMutex.Unlock()
	if !ok {
		return false
	}
	var au authorizedUser
	err := b.database.Take(&au, "public_key = ?", key).Error
	return err == nil
}

// hostOfferFor builds the reference offer of every frame in the peer's mailboxes that we have not delivered to it
func (b *Bounce) hostOfferFor(peer string) *referenceOffer {
	labels := b.peerLabels(peer)
	offer := &referenceOffer{ID: uuid.New(), References: []frameReference{}}
	if len(labels) == 0 {
		return offer
	}
	labelList := make([][]byte, 0, len(labels))
	for label := range labels {
		labelList = append(labelList, []byte(label))
	}

	var ids []uuid.UUID
	err := b.database.
		Model(&mailboxEntry{}).
		Distinct("mailbox_entries.encrypted_frame_id").
		Joins("LEFT JOIN delivery_records ON delivery_records.frame_id = mailbox_entries.encrypted_frame_id AND delivery_records.destination = ? AND delivery_records.frame_type = ?", peer, typeEncryptedFrame).
		Where("mailbox_entries.label IN ? AND delivery_records.id IS NULL", labelList).
		Pluck("mailbox_entries.encrypted_frame_id", &ids).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error building mailbox offer")
	}
	for _, id := range ids {
		offer.References = append(offer.References, frameReference{FrameID: id, Type: typeEncryptedFrame})
	}
	return offer
}

// mailboxEntryFor finds the entry of a stored frame that the peer can read, if any
func (b *Bounce) mailboxEntryFor(peer string, ef encryptedFrame) (mailboxEntry, bool) {
	for _, entry := range ef.Mailboxes {
		if b.peerCanReadLabel(peer, entry.Label) {
			return entry, true
		}
	}
	return mailboxEntry{}, false
}

// pruneOwnerMailboxes forgets owner labels older than the lookback window
func (b *Bounce) pruneOwnerMailboxes() {
	cutoff := time.Now().Unix() - (mailboxLookbackEpochs+2)*mailboxEpochSeconds
	err := b.database.Where("saved_at < ?", cutoff).Delete(&ownerMailbox{}).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error pruning owner mailboxes")
	}
}
