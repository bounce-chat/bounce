package chat

import (
	"errors"
	"sync"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// A reference offer names at most this many frames.  A peer whose history is larger offers it in parts, flagging each
// part Incomplete; the receiver asks for the next part once it has drained the current one (see handleCatchUp).  The
// same number bounds how many of one peer's references the reference engine keeps, so it is also the memory a single
// peer can tie up there.  A variable so tests can shrink it.
var maximumReferencesPerOffer = 100000

// The reference engine holds at most this many references across every peer, however many peers there are
var maximumReferences = 1000000

// Unknown peers (devices with no known owner yet) get a smaller per-peer and combined ceiling so throwaway
// identities cannot crowd out contacts or our own sync devices.  Variables so tests can shrink them.
var maximumUnknownReferencesPerPeer = 1000
var maximumUnknownReferences = 10000
var maximumUnknownReferenceAttempts = 3
var unknownReferenceLifetime = 5 * time.Minute

type pendingOfferKey struct {
	peer    string
	offerID ID
}

type referenceState struct {
	referenceOfferStatesMutex sync.Mutex
	referenceOfferStates      map[string]*referenceOfferState
	incompleteOffersMutex     sync.Mutex
	incompleteOffers          map[string]bool
	pendingOfferAcksMutex     sync.Mutex
	pendingOfferAcks          map[pendingOfferKey]bool
}

func newReferenceState() *referenceState {
	return &referenceState{
		referenceOfferStates: map[string]*referenceOfferState{},
		incompleteOffers:     map[string]bool{},
		pendingOfferAcks:     map[pendingOfferKey]bool{},
	}
}

func (b *Bounce) registerPendingReferenceOffer(peer string, offerID ID) {
	b.referenceState.pendingOfferAcksMutex.Lock()
	b.referenceState.pendingOfferAcks[pendingOfferKey{peer: peer, offerID: offerID}] = false
	b.referenceState.pendingOfferAcksMutex.Unlock()
}

func (b *Bounce) ackPendingReferenceOffer(peer string, offerID ID) {
	key := pendingOfferKey{peer: peer, offerID: offerID}
	b.referenceState.pendingOfferAcksMutex.Lock()
	if _, pending := b.referenceState.pendingOfferAcks[key]; pending {
		b.referenceState.pendingOfferAcks[key] = true
	} else {
		log.WithFields(log.Fields{
			"peer": peer,
			"id":   offerID,
		}).Debug("ignoring ack for reference offer not in flight")
	}
	b.referenceState.pendingOfferAcksMutex.Unlock()
}

func (b *Bounce) consumePendingReferenceOfferAck(peer string, offerID ID) bool {
	key := pendingOfferKey{peer: peer, offerID: offerID}
	b.referenceState.pendingOfferAcksMutex.Lock()
	acked := b.referenceState.pendingOfferAcks[key]
	if acked {
		delete(b.referenceState.pendingOfferAcks, key)
	}
	b.referenceState.pendingOfferAcksMutex.Unlock()
	return acked
}

func (b *Bounce) clearPendingReferenceOffer(peer string, offerID ID) {
	b.referenceState.pendingOfferAcksMutex.Lock()
	delete(b.referenceState.pendingOfferAcks, pendingOfferKey{peer: peer, offerID: offerID})
	b.referenceState.pendingOfferAcksMutex.Unlock()
}

func (b *Bounce) isKnownReferencePeer(peer string) bool {
	if b.encrypted {
		peerUserKeyMutex.Lock()
		_, known := peerUserKeys[peer]
		peerUserKeyMutex.Unlock()
		return known
	}
	_, known := b.peerOwner(peer)
	return known
}

func (b *Bounce) incompleteOfferFrom(peer string) bool {
	b.referenceState.incompleteOffersMutex.Lock()
	defer b.referenceState.incompleteOffersMutex.Unlock()
	return b.referenceState.incompleteOffers[peer]
}

func (b *Bounce) setIncompleteOfferFrom(peer string, incomplete bool) {
	b.referenceState.incompleteOffersMutex.Lock()
	defer b.referenceState.incompleteOffersMutex.Unlock()
	if incomplete {
		b.referenceState.incompleteOffers[peer] = true
	} else {
		delete(b.referenceState.incompleteOffers, peer)
	}
}

type referenceOfferState struct {
	sync.Mutex
	running bool
	pending bool
}

// A reference offer is a message sent to a newly connected device that provides the IDs of any frames that device
// should have, but that we didn't deliver to it.  Reference offers are delivery tracked using acks in order to
// support re-send logic, so they do have an ID even though they are only ever sent directly.
type referenceOffer struct {
	ID         ID
	References []frameReference
	Incomplete bool // more frames than fit in one offer; ask again once these are handled
}

// A requestReferenceOffer asks a peer for its next reference offer.  It is sent once an Incomplete offer has been
// drained, and the peer answers with a fresh offer that naturally leaves out everything delivered since.
type requestReferenceOffer struct{}

func (rro requestReferenceOffer) getType() uint16 {
	return typeRequestReferenceOffer
}

func (rro requestReferenceOffer) getPayload() []byte {
	return []byte{}
}

func (b *Bounce) handleRequestReferenceOffer(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	if b.encrypted {
		// An encrypted device's offers are of what it holds in the mailboxes the peer asked for
		go b.sendDirect(peer, b.hostOfferFor(peer))
		return nil, false
	}
	go b.sendReferences(peer)
	return nil, false
}

func (ro *referenceOffer) getType() uint16 {
	return typeReferenceOffer
}

func (ro *referenceOffer) getPayload() []byte {
	bytes, err := msgpack.Marshal(ro)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal reference offer")
	}
	return bytes
}

func (b *Bounce) getReferenceOfferStateForPeer(peer string) *referenceOfferState {
	b.referenceState.referenceOfferStatesMutex.Lock()
	defer b.referenceState.referenceOfferStatesMutex.Unlock()

	state, ok := b.referenceState.referenceOfferStates[peer]
	if !ok {
		state = &referenceOfferState{}
		b.referenceState.referenceOfferStates[peer] = state
	}

	return state
}

// sendReferences coalesces every trigger into one active run and at most one pending follow-up per peer. Calls made
// while a run is active return immediately instead of accumulating behind its retry loop.
func (b *Bounce) sendReferences(peer string) {
	state := b.getReferenceOfferStateForPeer(peer)
	state.Lock()
	if state.running {
		state.pending = true
		state.Unlock()
		return
	}
	state.running = true
	state.Unlock()

	for {
		b.sendReferenceOffer(peer)

		state.Lock()
		if state.pending {
			state.pending = false
			state.Unlock()
		} else {
			state.running = false
			state.Unlock()
			return
		}
	}
}

func (b *Bounce) sendReferenceOffer(peer string) {
	if _, exists := b.currentUser(); !exists {
		// Our profile hasn't been setup yet, we have nothing to offer
		return
	}

	owner, known := b.peerOwner(peer)
	if !known {
		// We have nothing to offer a device that we don't know about
		return
	}
	if blockedUser(owner) {
		// Offer nothing to a blocked user's devices, encrypted ones included
		return
	}

	_, exists := b.getDeviceFromAddress(b.network.Address())
	if !exists {
		// Our own device doesn't exist yet, we're probably going to be added as a new sync
		// device now.  In the meantime there's nothing to do.
		return
	}

	rd := b.getRemoteDevice(peer)
	if rd.connectedSockets() < 1 {
		// Can't send references to a device we're not connected to
		return
	}

	// Reference offers are often sent when connections are interrupted and sockets might be disconnected
	// but not yet reporting errors.  It's important to ensure references are delivered, so we send them
	// until they are ack'd, then delete the delivery record since this isn't a stored frame.
	giveUpTime := time.Now().Add(1 * time.Minute)
	for {
		// Generate a reference offer, and stop here if there's nothing to offer
		referenceOffer := b.getReferenceOfferFor(peer)
		if len(referenceOffer.References) == 0 {
			return
		}

		// Register the offer ID as in flight before sending so only its ack can complete it
		b.registerPendingReferenceOffer(peer, referenceOffer.ID)
		b.sendDirect(peer, referenceOffer)

		// Wait for an ack before checking if it was delivered
		time.Sleep(referenceRetryInterval())

		if b.consumePendingReferenceOfferAck(peer, referenceOffer.ID) {
			return
		}
		b.clearPendingReferenceOffer(peer, referenceOffer.ID)

		// Stop attempting to deliver an offer after a timeout
		if time.Now().After(giveUpTime) {
			log.WithFields(log.Fields{
				"destination": peer,
			}).Warn("gave up attempting to deliver reference offer")
			return
		}
	}
}

func (b *Bounce) hasAnyReferencesFor(address string) bool {
	userID, ok := b.peerOwner(address)
	if !ok {
		log.WithFields(log.Fields{
			"address": address,
		}).Warn("cannot generate reference offer for unknown device")
		return false
	}
	return b.hasAnyReferencesForUser(address, userID)
}

func (b *Bounce) hasAnyReferencesForUser(address string, userID ID) bool {
	// Make sure we don't generate references while we're in the middle of handling a catch up,
	// in order to not generate incomplete references of update group histories
	catchUpMutex.Lock()
	defer catchUpMutex.Unlock()

	if b.encrypted {
		log.Fatal("cannot generate reference offer from encrypted device")
	}

	for _, spec := range referencedFrames {
		if len(spec.offer(b, address, userID)) > 0 {
			return true
		}
	}

	return false
}

func (b *Bounce) getReferenceOfferFor(address string) *referenceOffer {
	// Make sure we don't generate references while we're in the middle of handling a catch up,
	// in order to not generate incomplete references of update group histories
	catchUpMutex.Lock()
	defer catchUpMutex.Unlock()

	if b.encrypted {
		log.Fatal("cannot generate reference offer from encrypted device")
	}

	userID, ok := b.peerOwner(address)
	if !ok {
		log.WithFields(log.Fields{
			"address": address,
		}).Warn("cannot generate reference offer for unknown device")
		return &referenceOffer{}
	}

	references := []frameReference{}
	for _, rf := range referencedFrames {
		references = append(references, rf.offer(b, address, userID)...)
	}

	// The registry order puts what everything else depends on first, so the first part of a large history is the
	// one worth having first
	incomplete := false
	if len(references) > maximumReferencesPerOffer {
		references = references[:maximumReferencesPerOffer]
		incomplete = true
	}
	return &referenceOffer{
		ID:         NewID(),
		References: references,
		Incomplete: incomplete,
	}
}

func (b *Bounce) getDirectMessagesToOffer(address string, userID ID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	// All DMs we have sent to or received from this user in the past week
	var dms []directMessage
	if userID == b.currentUserID() {
		err := b.database.
			Select("direct_messages.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == direct_messages.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeDirectMessage).
			Where("delivery_records.id IS NULL").
			Find(&dms).Error
		if err != nil {
			log.WithFields(log.Fields{
				"peer":  address,
				"error": err.Error(),
			}).Fatal("error loading DMs for reference offer")
		}
	} else {
		err := b.database.
			Select("direct_messages.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == direct_messages.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeDirectMessage).
			Where(
				"delivery_records.id IS NULL AND direct_messages.written_at >= ? AND direct_messages.xor = ?",
				time.Now().Add(-undeliverableAfter).Unix(),
				xor(userID, b.currentUserID()),
			).
			Find(&dms).Error
		if err != nil {
			log.WithFields(log.Fields{
				"peer":  address,
				"error": err.Error(),
			}).Fatal("error loading DMs for reference offer")
		}
	}

	// Collect IDs from the DMs
	references := []frameReference{}
	for _, dm := range dms {
		references = append(references, frameReference{FrameID: dm.ID, Type: typeDirectMessage})
	}
	return references
}

func (b *Bounce) getGroupMessagesToOffer(address string, userID ID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	oldestAllowedMessage := int64(0)
	if userID != b.currentUserID() {
		oldestAllowedMessage = time.Now().Add(-undeliverableAfter).Unix()
	}

	// All group messages that this device's user is a part of that are less than a week old
	var gms []groupMessage
	err := b.database.
		Select("group_messages.*").
		Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == group_messages.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeGroupMessage).
		Where(
			"delivery_records.id IS NULL AND group_messages.written_at >= ? AND group_messages.destination IN (?)",
			oldestAllowedMessage,
			b.database.
				Model(&group{}).
				Distinct().
				Select("groups.id").
				Joins("JOIN group_users ON groups.id = group_users.group_id").
				Where("group_users.user_id = ?", userID),
		).
		Find(&gms).Error
	if err != nil {
		log.WithFields(log.Fields{
			"peer":  address,
			"error": err.Error(),
		}).Fatal("error loading group messages for reference offer")
	}

	references := []frameReference{}
	for _, gm := range gms {
		references = append(references, frameReference{FrameID: gm.ID, Type: typeGroupMessage})
	}
	return references
}

func (b *Bounce) getUpdateDMsToOffer(address string, userID ID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	var unsentUpdateDMs []updateDM

	if userID == b.currentUserID() {
		// Select all update DMs from any DM
		err := b.database.
			Select("update_dms.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_dms.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeUpdateDM).
			Where("delivery_records.id IS NULL").
			Find(&unsentUpdateDMs).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error selecting update DM for reference offer")
		}
	} else {
		// Select updateDMs related to this user
		err := b.database.
			Select("update_dms.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_dms.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeUpdateDM).
			Where("update_dms.target = ? AND delivery_records.id IS NULL", xor(userID, b.currentUserID())).
			Find(&unsentUpdateDMs).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error selecting update DM for reference offer")
		}
	}

	references := []frameReference{}
	for _, ud := range unsentUpdateDMs {
		// Ignore sync scoped update DMs unless this is a sync device
		if ud.getScope(b.currentUserID()) == scopeSync && userID != b.currentUserID() {
			continue
		}
		references = append(references, frameReference{FrameID: ud.ID, Type: typeUpdateDM})
	}

	return references
}

func (b *Bounce) getDevicesToOffer(address string, userID ID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	var unsentDevices []device

	if userID == b.currentUserID() {
		// Sync devices can learn about any device we know about
		err := b.database.
			Select("devices.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == devices.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeDevice).
			Where("delivery_records.id IS NULL").
			Find(&unsentDevices).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error selecting devices for reference offer")
		}
	} else {
		// Get any device that belongs to us or them, or any users that share a group with this user
		err := b.database.
			Distinct("devices.id").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == devices.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeDevice).
			Where(
				"devices.address != ? AND (devices.user_id = ? OR devices.user_id = ? OR devices.user_id IN (?)) AND delivery_records.id IS NULL",
				address,
				b.currentUserID(),
				userID,
				b.database.
					Model(&user{}).
					Distinct().
					Select("users.id").
					Joins("JOIN group_users ON group_users.user_id = users.id").
					Where(
						"group_users.group_id IN (?)",
						b.database.
							Model(&group{}).
							Distinct().
							Select("groups.id").
							Joins("JOIN group_users ON group_users.group_id = groups.id").
							Where("user_id = ?", userID),
					),
			).
			Find(&unsentDevices).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error selecting devices for reference offer")
		}
	}

	references := []frameReference{}
	for _, dev := range unsentDevices {
		references = append(references, frameReference{FrameID: dev.ID, Type: typeDevice})
	}

	return references
}

func (b *Bounce) getAddUsersToOffer(address string, userID ID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	var unsentAddUsers []addUser

	if userID == b.currentUserID() {
		err := b.database.
			Select("add_users.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == add_users.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeAddUser).
			Where("delivery_records.id IS NULL").
			Find(&unsentAddUsers).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error selecting users for reference offer")
		}
	} else {
		err := b.database.
			Select("add_users.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == add_users.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeAddUser).
			Where("delivery_records.id IS NULL AND add_users.xor = ?", xor(b.currentUserID(), userID)).
			Find(&unsentAddUsers).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error selecting users for reference offer")
		}
	}

	references := []frameReference{}
	for _, au := range unsentAddUsers {
		references = append(references, frameReference{FrameID: au.ID, Type: typeAddUser})
	}

	return references
}

func (b *Bounce) getGroupCreationsToOffer(address string, userID ID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	var unsentGroupCreations []groupCreation

	if userID == b.currentUserID() {
		// Our own devices should know everything we know, so they get every creation we still hold, including the
		// groups we have since left; the update history of those groups is already offered to them, and without the
		// creation it could not be verified
		err := b.database.
			Select("group_creations.id").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == group_creations.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeGroupCreation).
			Where("delivery_records.id IS NULL").
			Find(&unsentGroupCreations).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error selecting unsent group creations in reference offer")
		}
	} else {
		err := b.database.
			Preload(clause.Associations).
			Distinct().
			Select("group_creations.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == group_creations.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeGroupCreation).
			Joins("JOIN groups ON groups.id = group_creations.id").
			Joins("JOIN group_users ON groups.id = group_users.group_id JOIN users ON group_users.user_id = users.id").
			Where("delivery_records.id IS NULL AND (group_users.user_id = ? OR groups.invites LIKE ?)", userID, "%"+userID.String()+"%").
			Find(&unsentGroupCreations).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error selecting unsent group creations in reference offer")
		}
	}

	references := []frameReference{}
	for _, gc := range unsentGroupCreations {
		references = append(references, frameReference{FrameID: gc.ID, Type: typeGroupCreation})
	}

	return references
}

func (b *Bounce) getUpdateGroupsToOffer(address string, userID ID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	var unsentUpdateGroups []updateGroup

	if userID == b.currentUserID() {
		err := b.database.
			Preload(clause.Associations).
			Select("update_groups.id").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_groups.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeUpdateGroup).
			Where("delivery_records.id IS NULL").
			Find(&unsentUpdateGroups).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error selecting unsent update groups in reference offer")
		}
	} else {
		// Find all update groups for groups this user is in
		err := b.database.
			Preload(clause.Associations).
			Distinct().
			Select("update_groups.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_groups.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeUpdateGroup).
			Joins("LEFT JOIN groups ON groups.id = update_groups.target").
			Joins("LEFT JOIN group_users ON update_groups.target = group_users.group_id").
			Where(
				"delivery_records.id IS NULL AND (((group_users.user_id = ? OR groups.invites LIKE ?) AND update_groups.custom_scope == ?) OR update_groups.custom_scope IN (?))",
				userID,
				"%"+userID.String()+"%",
				NilID,
				b.database.
					Model(&customScope{}).
					Distinct().
					Select("id").
					Where("addresses LIKE ?", "%"+address+"%"),
			).
			Find(&unsentUpdateGroups).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error selecting unsent update groups in reference offer")
		}

		// Find all the groups this user has ever left, rejected, or been kicked from
		groupsUserLeft := []ID{}
		err = b.database.
			Model(&updateGroup{}).
			Distinct().
			Select("target").
			Where(
				"applied = true AND custom_scope = ? AND (((type = ? OR type = ?) AND data = ?) OR (type = ? AND data = ? AND actor = ?))",
				NilID,
				updateGroupTypeRemoveUser,
				updateGroupTypeRevokeInvite,
				userID[:],
				updateGroupTypeRespondToInvite,
				[]byte{rejectInvite},
				userID,
			).
			Find(&groupsUserLeft).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error finding groups a user has left")

		}
		for _, groupID := range groupsUserLeft {
			// Make sure they were not re-added to the group
			if b.userIsInGroup(groupID, userID) || b.isInvited(groupID, userID) {
				continue
			}

			// Find the user's latest applied departure from this group, including its local insertion order so a
			// future-dated departure cannot open the offer window to updates saved afterwards.  The departure rows
			// found a moment ago can be gone by now, for instance when the group was deleted in between, so not
			// finding one means there is nothing to offer rather than a broken database.
			var lastDeparture struct {
				Timestamp int64
				SavedAt   int64
				RowID     int64 `gorm:"column:rowid"`
			}
			err := b.database.
				Table("update_groups").
				Select("timestamp, saved_at, rowid").
				Where(
					"applied = true AND custom_scope = ? AND target = ? AND (((type = ? OR type = ?) AND data = ?) OR (type = ? AND data = ? AND actor = ?))",
					NilID,
					groupID,
					updateGroupTypeRemoveUser,
					updateGroupTypeRevokeInvite,
					userID[:],
					updateGroupTypeRespondToInvite,
					[]byte{rejectInvite},
					userID,
				).
				Order("timestamp desc, saved_at desc, rowid desc").
				Take(&lastDeparture).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("error looking up users last departure from group")
			}

			// Find all the undelivered applied update groups up until their last departure
			var ugs []updateGroup
			err = b.database.
				Select("update_groups.*").
				Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_groups.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeUpdateGroup).
				Where(
					"delivery_records.id IS NULL AND update_groups.applied = true AND update_groups.custom_scope = ? AND update_groups.target = ? AND update_groups.timestamp <= ? AND update_groups.saved_at <= ? AND update_groups.rowid <= ?",
					NilID,
					groupID,
					lastDeparture.Timestamp,
					lastDeparture.SavedAt,
					lastDeparture.RowID,
				).Find(&ugs).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("error getting update groups for departed user")
			}

			// Include these update groups in the reference offer
			for _, ug := range ugs {
				unsentUpdateGroups = append(unsentUpdateGroups, ug)
			}
		}

		// Find all the groups this user has blocked, where we haven't delivered the block to this device
		var ugs []updateGroup
		err = b.database.
			Select("update_groups.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_groups.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeUpdateGroup).
			Where(
				"delivery_records.id IS NULL AND update_groups.actor = ? AND update_groups.type = ?",
				userID,
				updateGroupTypeBlock,
			).Find(&ugs).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error getting update group blocks for user")
		}
		for _, ug := range ugs {
			unsentUpdateGroups = append(unsentUpdateGroups, ug)
		}
	}

	// Collect all these update groups into a set of references
	references := []frameReference{}
	included := map[ID]bool{}
	for _, ug := range unsentUpdateGroups {
		if _, ok := included[ug.ID]; ok {
			continue
		} else {
			included[ug.ID] = true
		}
		// Ignore sync scoped update groups unless this is a sync device
		if ug.getScope(b.currentUserID()) == scopeSync && userID != b.currentUserID() {
			continue
		}
		references = append(references, frameReference{FrameID: ug.ID, Type: typeUpdateGroup})
	}

	return references
}

func (b *Bounce) getConfirmationsToOffer(address string, userID ID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	var unsentConfirmations []confirmation
	if userID == b.currentUserID() {
		// Our own devices get every confirmation we hold, as they do every update group and creation
		err := b.database.
			Select("confirmations.id").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == confirmations.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeConfirmation).
			Where("delivery_records.id IS NULL").
			Find(&unsentConfirmations).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error selecting unsent confirmations in reference offer")
		}
	} else {
		err := b.database.
			Preload(clause.Associations).
			Select("confirmations.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == confirmations.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeConfirmation).
			Joins("JOIN update_groups ON update_groups.id == confirmations.update_group_id").
			Joins("LEFT JOIN group_users ON update_groups.target == group_users.group_id").
			Where(
				"delivery_records.id IS NULL AND ((group_users.user_id = ? AND confirmations.custom_scope == ?) OR confirmations.custom_scope IN (?))",
				userID,
				NilID,
				b.database.
					Model(&customScope{}).
					Distinct().
					Select("id").
					Where("addresses LIKE ?", "%"+address+"%"),
			).
			Find(&unsentConfirmations).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error selecting unsent confirmations in reference offer")
		}
	}

	references := []frameReference{}
	for _, c := range unsentConfirmations {
		references = append(references, frameReference{FrameID: c.ID, Type: typeConfirmation})
	}

	return references
}

func (b *Bounce) getUpdateUsersToOffer(address string, userID ID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	var unsentUpdateUsers []updateUser

	if userID == b.currentUserID() {
		err := b.database.
			Distinct("update_users.id").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_users.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeUpdateUser).
			Where("delivery_records.id IS NULL").
			Find(&unsentUpdateUsers).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error selecting unsent update users in reference offer")
		}
	} else {
		err := b.database.
			Distinct("update_users.id").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_users.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeUpdateUser).
			Where(
				"update_users.type NOT IN (?) AND (update_users.target = ? OR update_users.target = ? OR update_users.target IN (?)) AND delivery_records.id IS NULL",
				[]uint16{updateUserTypeSetEncryptedDeviceName, updateUserTypeReplaceKeys},
				userID,
				b.currentUserID(),
				b.database.
					Model(&user{}).
					Distinct().
					Select("users.id").
					Joins("JOIN group_users ON group_users.user_id = users.id").
					Where(
						"group_users.group_id IN (?)",
						b.database.
							Model(&group{}).
							Distinct().
							Select("groups.id").
							Joins("JOIN group_users ON group_users.group_id = groups.id").
							Where("user_id = ?", userID),
					),
			).
			Find(&unsentUpdateUsers).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error selecting unsent update users in reference offer")
		}
	}

	references := []frameReference{}
	for _, uu := range unsentUpdateUsers {
		references = append(references, frameReference{FrameID: uu.ID, Type: typeUpdateUser})
	}

	return references
}

func (b *Bounce) getUpdateDevicesToOffer(address string, userID ID) []frameReference {
	if b.devicePool.isRevoked(address) {
		dev, ok := b.getDeviceFromAddress(address)
		if ok {
			var revoke updateDevice
			err := b.database.Where("target = ? AND type = ?", dev.ID, updateDeviceTypeRevoke).First(&revoke).Error
			if err != nil {
				log.WithFields(log.Fields{
					"peer":  address,
					"error": err.Error(),
				}).Error("error finding revoke update for revoked device")
				return []frameReference{}
			}
			return []frameReference{frameReference{
				FrameID: revoke.ID,
				Type:    typeUpdateDevice,
			}}
		}
	}

	var unsentUpdateDevices []updateDevice

	if userID == b.currentUserID() {
		err := b.database.
			Distinct("update_devices.id").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_devices.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeUpdateDevice).
			Where("delivery_records.id IS NULL").
			Find(&unsentUpdateDevices).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error selecting unsent update devices in reference offer")
		}
	} else {
		// Only get updates that revoke a device, using overlap scope
		err := b.database.
			Distinct("update_devices.id").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_devices.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeUpdateDevice).
			Where(
				"update_devices.type = ? AND (update_devices.author = ? OR update_devices.author = ? OR update_devices.author IN (?)) AND delivery_records.id IS NULL",
				updateDeviceTypeRevoke,
				userID,
				b.currentUserID(),
				b.database.
					Model(&user{}).
					Distinct().
					Select("users.id").
					Joins("JOIN group_users ON group_users.user_id = users.id").
					Where(
						"group_users.group_id IN (?)",
						b.database.
							Model(&group{}).
							Distinct().
							Select("groups.id").
							Joins("JOIN group_users ON group_users.group_id = groups.id").
							Where("user_id = ?", userID),
					),
			).
			Find(&unsentUpdateDevices).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error selecting unsent update devices in reference offer")
		}
	}

	references := []frameReference{}
	for _, ud := range unsentUpdateDevices {
		references = append(references, frameReference{FrameID: ud.ID, Type: typeUpdateDevice})
	}

	return references
}

func (b *Bounce) getReadReceiptsToOffer(address string, userID ID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	var unsentReadReceipts []readReceipt

	if userID == b.currentUserID() {
		err := b.database.
			Distinct("read_receipts.id").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == read_receipts.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeReadReceipt).
			Where("read_receipts.destination != ? AND delivery_records.id IS NULL", NilID).
			Find(&unsentReadReceipts).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error selecting unsent read receipts in reference offer")
		}
	} else {
		// read receipts where
		//	target type is direct message and destination is this device's user
		//	target type is group message and destination is any group this user is in
		//	either way, scope is not sync
		err := b.database.
			Distinct("read_receipts.id").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == read_receipts.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeReadReceipt).
			Where(
				"((read_receipts.target_type == ? AND read_receipts.destination = ?) OR (read_receipts.target_type = ? AND read_receipts.destination IN (?))) AND read_receipts.scope != ? AND delivery_records.id IS NULL",
				typeDirectMessage,
				userID,
				typeGroupMessage,
				b.database.
					Model(&group{}).
					Distinct().
					Select("groups.id").
					Joins("JOIN group_users ON group_users.group_id = groups.id").
					Where("user_id = ?", userID),
				scopeSync,
			).
			Find(&unsentReadReceipts).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error selecting unsent read receipts in reference offer")
		}
	}

	references := []frameReference{}
	for _, rr := range unsentReadReceipts {
		references = append(references, frameReference{FrameID: rr.ID, Type: typeReadReceipt})
	}

	return references
}

func (b *Bounce) getUpdateSettingsToOffer(address string, userID ID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	if userID != b.currentUserID() {
		return []frameReference{}
	}

	// Select all update settings that have not been delivered
	var unsentUpdateSettings []updateSettings
	err := b.database.
		Select("update_settings.*").
		Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == update_settings.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeUpdateSettings).
		Where("delivery_records.id IS NULL").
		Find(&unsentUpdateSettings).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error selecting update settings for reference offer")
	}

	references := []frameReference{}
	for _, ud := range unsentUpdateSettings {
		references = append(references, frameReference{FrameID: ud.ID, Type: typeUpdateSettings})
	}

	return references
}

func (b *Bounce) getFilesToOffer(address string, userID ID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	var unsentFiles []file

	if userID == b.currentUserID() {
		// If this is a sync device, send all unsent files
		err := b.database.
			Select("files.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == files.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeFile).
			Where("delivery_records.id IS NULL").
			Find(&unsentFiles).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error selecting files for reference offer")
		}
	} else {
		// If this is not a sync device, get all files where
		// 	scope is not sync AND
		// 	scope is user and the destination is this device's user OR
		// 	scope is group and the destination is a group that this device's user is in OR
		//	scope is group with invites and the destination is a group that this device's user is in OR it is a group that this device's user has been invited to OR
		// 	scope is global and the author is me or this device's user OR
		// 	scope is global and the author is someone who shares a group with this device's user
		err := b.database.
			Distinct("files.id").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == files.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeFile).
			Where(
				`
					delivery_records.id IS NULL AND files.scope != ? AND
					(
						(
							files.scope == ? AND files.destination = ?
						) OR (
							files.scope = ? AND files.destination IN (?)
						) OR (
							files.scope = ? AND (files.destination IN (?) OR files.destination IN (?))
						) OR (
							files.scope == ? AND (
								files.author == ? OR
								files.author == ? OR
								files.author IN (?)
							)
						)
					)`,
				scopeSync,
				scopeUser,
				xor(b.currentUserID(), userID),
				scopeGroup,
				b.database.
					Model(&group{}).
					Distinct().
					Select("groups.id").
					Joins("JOIN group_users ON group_users.group_id = groups.id").
					Where("user_id = ?", userID),
				scopeGroupWithInvites,
				b.database.
					Model(&group{}).
					Distinct().
					Select("groups.id").
					Joins("JOIN group_users ON group_users.group_id = groups.id").
					Where("user_id = ?", userID),
				b.database.
					Model(&group{}).
					Distinct().
					Select("id").
					Where("invites LIKE ?", "%"+userID.String()+"%"),
				scopeGlobal,
				b.currentUserID(),
				userID,
				b.database.
					Model(&user{}).
					Distinct().
					Select("users.id").
					Joins("JOIN group_users ON group_users.user_id = users.id").
					Where(
						"group_users.group_id IN (?)",
						b.database.
							Model(&group{}).
							Distinct().
							Select("groups.id").
							Joins("JOIN group_users ON group_users.group_id = groups.id").
							Where("user_id = ?", userID),
					),
			).
			Find(&unsentFiles).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error selecting files for reference offer")
		}
	}

	references := []frameReference{}
	for _, f := range unsentFiles {
		references = append(references, frameReference{FrameID: f.ID, Type: typeFile})
	}

	return references
}

func (b *Bounce) getChunkOffersToOffer(address string, userID ID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	var unsentChunkOffers []chunkOffer

	if userID == b.currentUserID() {
		var deliveredIDs []ID
		b.database.Model(&deliveryRecord{}).
			Where("destination = ? AND frame_type = ?", address, typeChunkOffer).
			Pluck("frame_id", &deliveredIDs)

		// An empty NOT IN list renders as NOT IN (NULL), which matches nothing, so only add it when there is something to exclude
		query := b.database.Model(&chunkOffer{}).Distinct("id")
		if len(deliveredIDs) > 0 {
			query = query.Where("id NOT IN (?)", deliveredIDs)
		}
		query.Find(&unsentChunkOffers)
	} else {
		// Groups the user is in
		var groupIDs []ID
		b.database.Table("group_users").
			Where("user_id = ?", userID).
			Pluck("group_id", &groupIDs)

		// All the users in the groups this user is in
		var peerIDs []ID
		if len(groupIDs) > 0 {
			b.database.Table("group_users").
				Where("group_id IN ?", groupIDs).
				Pluck("user_id", &peerIDs)
		}

		// Groups that this user is invited to
		var groupInviteIDs []ID
		b.database.Model(&group{}).
			Where("invites LIKE ?", "%"+userID.String()+"%").
			Pluck("id", &groupInviteIDs)

		// All groups that the user has metadata access to
		allAccessibleGroupIDs := append(groupIDs, groupInviteIDs...)

		// All delivered chunk offers
		var deliveredIDs []ID
		b.database.Model(&deliveryRecord{}).
			Where("destination = ? AND frame_type = ?", address, typeChunkOffer).
			Pluck("frame_id", &deliveredIDs)

		var myDevices []string
		b.database.Model(&device{}).
			Where("user_id = ?", b.currentUserID()).
			Pluck("address", &myDevices)

		if len(deliveredIDs) == 0 {
			// Get all of the chunk offers for my profile images
			var globalProfileScoped []chunkOffer
			b.database.Model(&chunkOffer{}).
				Distinct("id").
				Where("location IN (?) AND scope = ? AND destination = ?", myDevices, scopeGlobal, b.currentUserID()).
				Find(&globalProfileScoped)
			unsentChunkOffers = append(unsentChunkOffers, globalProfileScoped...)

			// Get all the user scoped chunk offers for our xor
			var userScoped []chunkOffer
			b.database.Model(&chunkOffer{}).
				Distinct("id").
				Where("scope = ? AND destination = ?", scopeUser, xor(userID, b.currentUserID())).
				Find(&userScoped)
			unsentChunkOffers = append(unsentChunkOffers, userScoped...)

			// Get the group scoped chunks for groups the user is a member of, and the group-with-invites scoped
			// chunks for any group they have metadata access to; an invitee may see the icon but not the attachments
			var groupScoped []chunkOffer
			b.database.Model(&chunkOffer{}).
				Distinct("id").
				Where("scope = ? AND destination IN ?", scopeGroup, groupIDs).
				Find(&groupScoped)
			unsentChunkOffers = append(unsentChunkOffers, groupScoped...)
			var groupWithInvitesScoped []chunkOffer
			b.database.Model(&chunkOffer{}).
				Distinct("id").
				Where("scope = ? AND destination IN ?", scopeGroupWithInvites, allAccessibleGroupIDs).
				Find(&groupWithInvitesScoped)
			unsentChunkOffers = append(unsentChunkOffers, groupWithInvitesScoped...)

			// Get any overlap scoped offers
			var globalScoped []chunkOffer
			b.database.Model(&chunkOffer{}).
				Distinct("id").
				Where("scope = ? AND (author IN ? OR author = ?)", scopeGlobal, peerIDs, b.currentUserID()).
				Find(&globalScoped)
			unsentChunkOffers = append(unsentChunkOffers, globalScoped...)
		} else {
			// Get all of the chunk offers for my profile images
			var globalProfileScoped []chunkOffer
			b.database.Model(&chunkOffer{}).
				Distinct("id").
				Where("id NOT IN (?) AND location IN (?) AND scope = ? AND destination = ?", deliveredIDs, myDevices, scopeGlobal, b.currentUserID()).
				Find(&globalProfileScoped)
			unsentChunkOffers = append(unsentChunkOffers, globalProfileScoped...)

			// Get all the user scoped chunk offers for our xor
			var userScoped []chunkOffer
			b.database.Model(&chunkOffer{}).
				Distinct("id").
				Where("id NOT IN (?) AND scope = ? AND destination = ?", deliveredIDs, scopeUser, xor(userID, b.currentUserID())).
				Find(&userScoped)
			unsentChunkOffers = append(unsentChunkOffers, userScoped...)

			// Get the group scoped chunks for groups the user is a member of, and the group-with-invites scoped
			// chunks for any group they have metadata access to; an invitee may see the icon but not the attachments
			var groupScoped []chunkOffer
			b.database.Model(&chunkOffer{}).
				Distinct("id").
				Where("id NOT IN (?) AND scope = ? AND destination IN ?", deliveredIDs, scopeGroup, groupIDs).
				Find(&groupScoped)
			unsentChunkOffers = append(unsentChunkOffers, groupScoped...)
			var groupWithInvitesScoped []chunkOffer
			b.database.Model(&chunkOffer{}).
				Distinct("id").
				Where("id NOT IN (?) AND scope = ? AND destination IN ?", deliveredIDs, scopeGroupWithInvites, allAccessibleGroupIDs).
				Find(&groupWithInvitesScoped)
			unsentChunkOffers = append(unsentChunkOffers, groupWithInvitesScoped...)

			// Get any overlap scoped offers
			var globalScoped []chunkOffer
			b.database.Model(&chunkOffer{}).
				Distinct("id").
				Where("id NOT IN (?) AND scope = ? AND (author IN ? OR author = ?)", deliveredIDs, scopeGlobal, peerIDs, b.currentUserID()).
				Find(&globalScoped)
			unsentChunkOffers = append(unsentChunkOffers, globalScoped...)
		}
	}

	references := []frameReference{}
	alreadyIn := map[ID]bool{}
	for _, co := range unsentChunkOffers {
		if _, in := alreadyIn[co.ID]; in {
			continue
		}
		references = append(references, frameReference{FrameID: co.ID, Type: typeChunkOffer})
		alreadyIn[co.ID] = true
	}

	return references
}

func (b *Bounce) getDraftsToOffer(address string, userID ID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	if userID != b.currentUserID() {
		return []frameReference{}
	}

	// Select all drafts that have not been delivered
	var unsentDrafts []draft
	err := b.database.
		Select("drafts.*").
		Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == drafts.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeDraft).
		Where("delivery_records.id IS NULL").
		Find(&unsentDrafts).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error selecting drafts for reference offer")
	}

	references := []frameReference{}
	for _, ud := range unsentDrafts {
		references = append(references, frameReference{FrameID: ud.ID, Type: typeDraft})
	}

	return references
}

func (b *Bounce) getEncryptedChunkOffersToOffer(address string, userID ID) []frameReference {
	if b.devicePool.isRevoked(address) {
		return []frameReference{}
	}

	var unsentEncryptedChunkOffers []encryptedChunkOffer

	if userID == b.currentUserID() {
		var deliveredIDs []ID
		b.database.Model(&deliveryRecord{}).
			Where("destination = ? AND frame_type = ?", address, typeEncryptedChunkOffer).
			Pluck("frame_id", &deliveredIDs)

		// An empty NOT IN list renders as NOT IN (NULL), which matches nothing, so only add it when there is something to exclude
		query := b.database.Model(&encryptedChunkOffer{}).Distinct("id")
		if len(deliveredIDs) > 0 {
			query = query.Where("id NOT IN (?)", deliveredIDs)
		}
		query.Find(&unsentEncryptedChunkOffers)
	} else {
		// Groups the user is in
		var groupIDs []ID
		b.database.Table("group_users").
			Where("user_id = ?", userID).
			Pluck("group_id", &groupIDs)

		// All the users in the groups this user is in
		var peerIDs []ID
		if len(groupIDs) > 0 {
			b.database.Table("group_users").
				Where("group_id IN ?", groupIDs).
				Pluck("user_id", &peerIDs)
		}

		// Groups that this user is invited to
		var groupInviteIDs []ID
		b.database.Model(&group{}).
			Where("invites LIKE ?", "%"+userID.String()+"%").
			Pluck("id", &groupInviteIDs)

		// All groups that the user has metadata access to
		allAccessibleGroupIDs := append(groupIDs, groupInviteIDs...)

		// All delivered chunk offers
		var deliveredIDs []ID
		b.database.Model(&deliveryRecord{}).
			Where("destination = ? AND frame_type = ?", address, typeEncryptedChunkOffer).
			Pluck("frame_id", &deliveredIDs)

		if len(deliveredIDs) == 0 {
			// First, get all the user scoped chunk offers for our xor
			var userScoped []encryptedChunkOffer
			b.database.Model(&encryptedChunkOffer{}).
				Distinct("id").
				Where("scope = ? AND destination = ?", scopeUser, xor(userID, b.currentUserID())).
				Find(&userScoped)
			unsentEncryptedChunkOffers = append(unsentEncryptedChunkOffers, userScoped...)

			// Next, the group scoped chunks for groups the user is a member of, and the group-with-invites scoped
			// chunks for any group they have metadata access to
			var groupScoped []encryptedChunkOffer
			b.database.Model(&encryptedChunkOffer{}).
				Distinct("id").
				Where("scope = ? AND destination IN ?", scopeGroup, groupIDs).
				Find(&groupScoped)
			unsentEncryptedChunkOffers = append(unsentEncryptedChunkOffers, groupScoped...)
			var groupWithInvitesScoped []encryptedChunkOffer
			b.database.Model(&encryptedChunkOffer{}).
				Distinct("id").
				Where("scope = ? AND destination IN ?", scopeGroupWithInvites, allAccessibleGroupIDs).
				Find(&groupWithInvitesScoped)
			unsentEncryptedChunkOffers = append(unsentEncryptedChunkOffers, groupWithInvitesScoped...)

			// Lastly, get any overlap scoped offers
			var globalScoped []encryptedChunkOffer
			b.database.Model(&encryptedChunkOffer{}).
				Distinct("id").
				Where("scope = ? AND (author IN ? OR author = ?)", scopeGlobal, peerIDs, b.currentUserID()).
				Find(&globalScoped)
			unsentEncryptedChunkOffers = append(unsentEncryptedChunkOffers, globalScoped...)
		} else {
			// First, get all the user scoped chunk offers for our xor
			var userScoped []encryptedChunkOffer
			b.database.Model(&encryptedChunkOffer{}).
				Distinct("id").
				Where("id NOT IN (?) AND scope = ? AND destination = ?", deliveredIDs, scopeUser, xor(userID, b.currentUserID())).
				Find(&userScoped)
			unsentEncryptedChunkOffers = append(unsentEncryptedChunkOffers, userScoped...)

			// Next, the group scoped chunks for groups the user is a member of, and the group-with-invites scoped
			// chunks for any group they have metadata access to
			var groupScoped []encryptedChunkOffer
			b.database.Model(&encryptedChunkOffer{}).
				Distinct("id").
				Where("id NOT IN (?) AND scope = ? AND destination IN ?", deliveredIDs, scopeGroup, groupIDs).
				Find(&groupScoped)
			unsentEncryptedChunkOffers = append(unsentEncryptedChunkOffers, groupScoped...)
			var groupWithInvitesScoped []encryptedChunkOffer
			b.database.Model(&encryptedChunkOffer{}).
				Distinct("id").
				Where("id NOT IN (?) AND scope = ? AND destination IN ?", deliveredIDs, scopeGroupWithInvites, allAccessibleGroupIDs).
				Find(&groupWithInvitesScoped)
			unsentEncryptedChunkOffers = append(unsentEncryptedChunkOffers, groupWithInvitesScoped...)

			// Lastly, get any overlap scoped offers
			var globalScoped []encryptedChunkOffer
			b.database.Model(&encryptedChunkOffer{}).
				Distinct("id").
				Where("id NOT IN (?) AND scope = ? AND (author IN ? OR author = ?)", deliveredIDs, scopeGlobal, peerIDs, b.currentUserID()).
				Find(&globalScoped)
			unsentEncryptedChunkOffers = append(unsentEncryptedChunkOffers, globalScoped...)
		}
	}

	references := []frameReference{}
	alreadyIn := map[ID]bool{}
	for _, eco := range unsentEncryptedChunkOffers {
		if alreadyIn[eco.ID] {
			continue
		}
		references = append(references, frameReference{FrameID: eco.ID, Type: typeEncryptedChunkOffer})
		alreadyIn[eco.ID] = true
	}

	return references
}

func (b *Bounce) handleReferenceOffer(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	if b.devicePool.isRevoked(peer) {
		return nil, false
	}

	// Unpack the offer
	var ro referenceOffer
	err := msgpack.Unmarshal(payload, &ro)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling reference offer")
		return nil, false
	}

	encryptedDeviceCacheMutex.Lock()
	_, encryptedPeer := encryptedDeviceCache[peer]
	encryptedDeviceCacheMutex.Unlock()
	if len(ro.References) == 0 && !b.encrypted && encryptedPeer {
		// An encrypted device may send an empty reference offer in order to trigger the peer
		// to request the management key hash, so that the peer can check if the encrypted device needs
		// to be re-keyed.  This is the only case that an empty reference offer should ever be sent.
		// An encrypted device answers a mailbox request with an offer even when it has nothing for us, so this is
		// not answered with another mailbox request.
		go b.sendAck(peer, typeReferenceOffer, ro.ID)
		b.getManagementKeyHash(peer)
		go b.sendEncryptedClearBefores(peer)
		return nil, false
	}

	if len(ro.References) > maximumReferencesPerOffer {
		log.WithFields(log.Fields{
			"peer":       peer,
			"references": len(ro.References),
		}).Warn("reference offer names more frames than we will consider at once, ignoring the rest")
		ro.References = ro.References[:maximumReferencesPerOffer]
	}
	b.setIncompleteOfferFrom(peer, ro.Incomplete)

	// References to frames stored on an encrypted device are opaque copy IDs.  We cannot tell from the outside whether
	// we already hold the frame inside one, so we request every copy this host has not delivered to us before, and
	// ack the ones it has.  Only an encrypted device we know of is taken up on such an offer.
	hostReferences := []frameReference{}
	otherReferences := []frameReference{}
	for _, reference := range ro.References {
		if reference.Type == typeEncryptedFrame {
			hostReferences = append(hostReferences, reference)
		} else {
			otherReferences = append(otherReferences, reference)
		}
	}

	references, acks := b.processReferenceOffer(peer, otherReferences)
	if encryptedPeer && !b.encrypted {
		for _, reference := range hostReferences {
			if b.frameIsDeliveredTo(reference.FrameID, typeEncryptedFrame, peer) {
				acks = append(acks, reference)
			} else {
				references = append(references, reference)
			}
		}
	}

	// Inform the reference engine that this peer has these frames that we don't know about
	dropped := 0
	if len(references) > 0 {
		dropped = b.loadReferenceOffer(peer, references)
	}

	// Ack the offer unless we had to drop part of a known peer's offer for lack of room, in which case
	// leaving it unacked lets the sender's retry loop re-offer once capacity frees up.  Unknown peers
	// are always acked so they cannot probe table fullness.
	if dropped == 0 || !b.isKnownReferencePeer(peer) {
		go b.sendAck(peer, typeReferenceOffer, ro.ID)
	}

	// Send an ack for everything we already have in the database and the peer may have
	if len(acks) > 0 {
		go b.sendDirect(peer, &ack{
			References: acks,
		})
	}

	// If we already had everything in this offer request the next one now, don't wait for a catch up since we aren't
	// going to make one
	if ro.Incomplete && len(ro.References) > 0 && len(references) == 0 {
		b.setIncompleteOfferFrom(peer, false)
		go b.sendDirect(peer, requestReferenceOffer{})
	}

	return nil, false
}

func (b *Bounce) processReferenceOffer(peer string, offered []frameReference) ([]frameReference, []frameReference) {
	requests := []frameReference{}
	acks := []frameReference{}
	typesToIDs := referencedIDs(offered)

	catchUpMutex.Lock()

	// An encrypted device stores copies under IDs their senders chose, so it cannot recognise an offered frame as
	// one it holds and asks for everything, the same as for a peer we do not know.  A copy of a frame it already
	// has is recognised by its mailbox tags when it arrives (see handleEncryptedFrame).
	peerIsKnown := false
	if !b.encrypted {
		peerIsKnown = b.isKnownReferencePeer(peer)
	}
	if !peerIsKnown {
		for _, frameType := range referencedTypes {
			for _, id := range typesToIDs[frameType] {
				// If the device is unknown we request everything so that a random device isn't able
				// to determine what we already have
				requests = append(requests, frameReference{FrameID: id, Type: frameType})
			}
		}
		catchUpMutex.Unlock()
		return requests, acks
	}

	alreadyHave := []frameReference{}
	for _, frameType := range referencedTypes {
		missing, have := b.determineFramesAlreadyReceived(typesToIDs[frameType], frameType)
		requests = append(requests, missing...)
		alreadyHave = append(alreadyHave, have...)
	}
	catchUpMutex.Unlock()
	if len(alreadyHave) == 0 {
		// If we have nothing that is being offered, there's nothing to ack and we can make the request now
		return requests, acks
	}

	// Make sure that anything we ack is something this peer was allowed to offer us.  If not, they might be
	// trying to figure out what we have
	allowed := map[uint16]map[ID]bool{}
	offerable := b.getReferenceOfferFor(peer)
	for frameType, ids := range referencedIDs(offerable.References) {
		allowed[frameType] = map[ID]bool{}
		for _, id := range ids {
			allowed[frameType][id] = true
		}
	}
	for _, ref := range alreadyHave {
		if allowed[ref.Type][ref.FrameID] || b.frameIsDeliveredTo(ref.FrameID, ref.Type, peer) {
			b.markFrameDelivered(ref.FrameID, ref.Type, peer)
			acks = append(acks, ref)
		}
	}

	return requests, acks
}

func (b *Bounce) determineFramesAlreadyReceived(offeredIDs []ID, frameType uint16) ([]frameReference, []frameReference) {
	dontHave := []frameReference{}
	alreadyHave := []frameReference{}

	table, ok := typeTable[frameType]
	if !ok {
		log.WithFields(log.Fields{
			"type": frameType,
		}).Warn("attempted to get frame that doesn't have database table")
	}

	for _, id := range offeredIDs {
		ref := frameReference{FrameID: id, Type: frameType}

		var count int64
		err := b.database.Table(table).Where("id = ?", id).Count(&count).Error
		if err == nil && count == 0 {
			err = gorm.ErrRecordNotFound
		}

		if err == nil {
			alreadyHave = append(alreadyHave, ref)
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			dontHave = append(dontHave, ref)
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error getting frame to request or ack")
		}
	}

	return dontHave, alreadyHave
}
