package chat

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"testing"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/alecthomas/assert/v2"
)

// These tests describe how frames are stored on encrypted devices (mailbox.go): addressed to mailboxes derived
// from secrets the host does not hold, so that the host can serve them to the right readers without learning who
// those readers are, who wrote the frame, what conversation it belongs to, what type it is, or when it was written.

// signedDM builds a direct message from author to counterparty the way the engine would, so it can be sealed
func signedDM(author *Bounce, counterparty ID, text string) *directMessage {
	dm := &directMessage{Nonce: newNonce(), Author: author.currentUserID(), Xor: xor(author.currentUserID(), counterparty), Text: text, WrittenAt: time.Now().Unix()}
	dm.ID = dm.generateID()
	dm.OriginalPayload, _ = msgpack.Marshal(dm)
	sc := author.createSignedContainer(typeDirectMessage, dm.OriginalPayload)
	dm.Signature, dm.Signer = sc.Signature, sc.Signer
	return dm
}

// signedGroupMessage builds a group message from author the way the engine would, so it can be sealed
func signedGroupMessage(author *Bounce, groupID ID, text string) *groupMessage {
	gm := &groupMessage{Nonce: newNonce(), Author: author.currentUserID(), Destination: groupID, Text: text, WrittenAt: time.Now().Unix()}
	gm.ID = gm.generateID()
	gm.OriginalPayload, _ = msgpack.Marshal(gm)
	sc := author.createSignedContainer(typeGroupMessage, gm.OriginalPayload)
	gm.Signature, gm.Signer = sc.Signature, sc.Signer
	return gm
}

// sealFor seals a frame the way sender would for one of its own encrypted devices: to every mailbox the frame
// belongs in
func sealFor(t *testing.T, sender *Bounce, br broadcastable) *encryptedFrame {
	t.Helper()
	secrets, err := sender.threadSecretsFor(br)
	assert.NoError(t, err)
	ef, err := sealFrame(br, sender.secretsForOwner(secrets, NilID), sender.copySettingsFor(br))
	assert.NoError(t, err)
	return ef
}

// sealForHost seals a frame the way sender would for a particular encrypted device, which for another user's
// device means only the mailboxes that user can read
func sealForHost(t *testing.T, sender *Bounce, br broadcastable, host *Bounce) *encryptedFrame {
	t.Helper()
	secrets, err := sender.hostSecretsFor(br, host.network.Address())
	assert.NoError(t, err)
	ef, err := sealFrame(br, secrets, sender.copySettingsFor(br))
	assert.NoError(t, err)
	return ef
}

// signedDraft builds a draft of a thread the way the engine would when the user types
func signedDraft(author *Bounce, thread ID, text string, timestamp int64) *draft {
	d := &draft{ID: NewID(), Thread: thread, Text: text, Timestamp: timestamp}
	d.OriginalPayload, _ = msgpack.Marshal(d)
	sc := author.createSignedContainer(typeDraft, d.OriginalPayload)
	d.Signature, d.Signer = sc.Signature, sc.Signer
	return d
}

// receivesOf turns a stored frame into what a host would hand a reader, one per mailbox
func receivesOf(ef *encryptedFrame) []encryptedReceive {
	receives := []encryptedReceive{}
	for _, entry := range ef.Mailboxes {
		receives = append(receives, encryptedReceive{ID: ef.ID, Payload: ef.Payload, Label: entry.Label, WrappedDEK: entry.WrappedDEK})
	}
	return receives
}

// openWith tries to open a stored frame as reader, through whichever of its mailboxes the reader can read
func openWith(reader *Bounce, ef *encryptedFrame) (frame, bool) {
	index := reader.mailboxIndex()
	for _, er := range receivesOf(ef) {
		if opened, err := index.open(er); err == nil {
			return opened, true
		}
	}
	return frame{}, false
}

func TestADirectMessageSealedForAHostOpensForBothParticipantsOnly(t *testing.T) {
	me, alice, bob, _ := createUsersAndGroups(t)

	dm := signedDM(me, alice.currentUserID(), "for alice")
	ef := sealFor(t, me, dm)

	// One mailbox, the one the two of us share; my own devices derive it as well
	assert.Equal(t, 1, len(ef.Mailboxes))

	opened, ok := openWith(alice, ef)
	assert.True(t, ok)
	assert.Equal(t, dm.ID, opened.ID)
	assert.Equal(t, typeDirectMessage, opened.Type)
	assert.Equal(t, dm.getPayload(), opened.Payload)

	_, ok = openWith(me, ef)
	assert.True(t, ok, "the sender's other devices read it through the same DM mailbox")

	_, ok = openWith(bob, ef)
	assert.False(t, ok, "a third party has no mailbox for it")
}

func TestAGroupFrameCostsOneMailboxWhateverTheMembership(t *testing.T) {
	me, alice, bob, groupID := createUsersAndGroups(t)
	for _, b := range []*Bounce{me, alice, bob} {
		waitForGroupKeys(t, b, groupID, 1)
	}

	ef := sealFor(t, me, signedGroupMessage(me, groupID, "hello group"))
	assert.Equal(t, 1, len(ef.Mailboxes), "every member derives the same group key, so there is nothing per member")

	for _, member := range []*Bounce{alice, bob, me} {
		_, ok := openWith(member, ef)
		assert.True(t, ok)
	}
}

func TestARemovedMemberCannotOpenFramesSealedAfterTheirRemoval(t *testing.T) {
	me, alice, bob, groupID := createUsersAndGroups(t)
	for _, b := range []*Bounce{me, alice, bob} {
		waitForGroupKeys(t, b, groupID, 1)
	}

	before := sealFor(t, me, signedGroupMessage(me, groupID, "while bob was here"))

	assert.NoError(t, me.RemoveUserFromGroup(groupID, bob.currentUserID()))
	waitForGroupKeys(t, alice, groupID, 2)
	time.Sleep(200 * time.Millisecond) // let Bob learn he was removed

	after := sealFor(t, me, signedGroupMessage(me, groupID, "after bob left"))

	_, ok := openWith(bob, before)
	assert.True(t, ok, "frames from while Bob was a member stay readable to him")
	_, ok = openWith(bob, after)
	assert.False(t, ok, "frames sealed under the rotated key are not")
	_, ok = openWith(alice, after)
	assert.True(t, ok)
}

func TestANewMemberOpensFramesSealedBeforeTheyJoined(t *testing.T) {
	quickReferenceRetries(t)
	me, _, _, groupID := createUsersAndGroups(t)
	waitForGroupKeys(t, me, groupID, 1)

	earlier := sealFor(t, me, signedGroupMessage(me, groupID, "before carol"))

	carol := newBounceUser("Carol")
	t.Cleanup(carol.Shutdown)
	introduce(t, me, carol)
	assert.NoError(t, me.InviteUserToGroup(groupID, carol.currentUserID()))
	waitForGroupKeys(t, carol, groupID, 1)

	// The invite carried the whole chain, so the history on encrypted devices is as readable to Carol as the
	// history she receives directly from the other members
	_, ok := openWith(carol, earlier)
	assert.True(t, ok)
}

func TestFramesSealedUnderAConcurrentRotationStillOpen(t *testing.T) {
	me, alice, _, groupID := createUsersAndGroups(t)
	waitForGroupKeys(t, me, groupID, 1)
	waitForGroupKeys(t, alice, groupID, 1)

	// Me and Alice rotate at the same time
	meState, _ := me.currentGroupState(groupID)
	meRotates := &updateGroup{Actor: me.currentUserID(), Target: groupID, Timestamp: time.Now().Unix(), Type: updateGroupTypeRotateKey}
	assert.NoError(t, me.attachGroupKeySecret(meRotates, meState, NilID))
	meFrame := signUpdateGroup(me, meRotates)
	aliceState, _ := alice.currentGroupState(groupID)
	aliceRotates := &updateGroup{Actor: alice.currentUserID(), Target: groupID, Timestamp: time.Now().Unix() + 1, Type: updateGroupTypeRotateKey}
	assert.NoError(t, alice.attachGroupKeySecret(aliceRotates, aliceState, NilID))
	aliceFrame := signUpdateGroup(alice, aliceRotates)

	// Alice, knowing only her own rotation, seals a message under her intermediate key
	alice.handleUpdateGroup(alice.network.Address(), aliceFrame, false)
	sealedByAlice := sealFor(t, alice, signedGroupMessage(alice, groupID, "sealed mid-rotation"))

	// Me applies both; Alice's intermediate key is not on Me's canonical chain, but the frame still opens
	me.handleUpdateGroup(me.network.Address(), meFrame, false)
	me.handleUpdateGroup(alice.network.Address(), aliceFrame, false)
	_, ok := openWith(me, sealedByAlice)
	assert.True(t, ok)
}

func TestMailboxLabelsRotateWithTheEpochAndOldOnesStayReadable(t *testing.T) {
	me, alice, _, _ := createUsersAndGroups(t)
	start := time.Now().Unix()
	mailboxClock = func() int64 { return start }
	t.Cleanup(func() { mailboxClock = func() int64 { return time.Now().Unix() } })

	thisWeek := sealFor(t, me, signedDM(me, alice.currentUserID(), "this week"))

	mailboxClock = func() int64 { return start + mailboxEpochSeconds }
	nextWeek := sealFor(t, me, signedDM(me, alice.currentUserID(), "next week"))

	assert.False(t, bytes.Equal(thisWeek.Mailboxes[0].Label, nextWeek.Mailboxes[0].Label), "labels change every epoch")

	_, ok := openWith(alice, thisWeek)
	assert.True(t, ok, "last week's mailbox is still within the lookback window")
	_, ok = openWith(alice, nextWeek)
	assert.True(t, ok)
}

func TestAHostLearnsAboutNewConversationsWithoutReconnecting(t *testing.T) {
	quickReferenceRetries(t)
	bob := newBounceUser("Bob")
	t.Cleanup(bob.Shutdown)
	host, key := newEncryptedDevice(t)
	claimEncryptedDevice(t, bob, host, key)
	waitForOwnerMailboxes(t, host)

	// With no contacts and no groups Bob can only read his sync mailbox; a new contact adds a DM mailbox, and the
	// next peer audit tells the host about it, so frames Alice leaves there are offered without Bob reconnecting
	before := len(host.peerLabels(bob.network.Address()))
	alice := newBounceUser("Alice")
	t.Cleanup(alice.Shutdown)
	introduce(t, bob, alice)
	bob.refreshMailboxRequests()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(host.peerLabels(bob.network.Address())) <= before {
		time.Sleep(25 * time.Millisecond)
	}
	after := host.peerLabels(bob.network.Address())
	assert.True(t, len(after) > before)
	secret, err := bob.dmThreadSecret(mustCurrentUser(bob).PrivateECDHKey, mustCurrentUser(alice).PublicECDHKey)
	assert.NoError(t, err)
	assert.True(t, after[string(mailboxLabel(secret, currentMailboxEpoch()))])

	// Nothing changed since, so another audit sends nothing
	bob.refreshMailboxRequests()
	assert.Equal(t, bob.mailboxIndex().digest(), mailboxRequestDigests[bob.network.Address()+host.network.Address()])
}

func mustCurrentUser(b *Bounce) user {
	u, _ := b.currentUser()
	return u
}

func TestAHostKeepsOnlyTheNewestDraftOfAThread(t *testing.T) {
	_, alice, bob, groupID := createUsersAndGroups(t)
	host, key := newEncryptedDevice(t)
	claimEncryptedDevice(t, bob, host, key)
	waitForOwnerMailboxes(t, host)

	store := func(br broadcastable) *encryptedFrame {
		ef := sealForHost(t, bob, br, host)
		payload, _ := msgpack.Marshal(ef)
		host.handleEncryptedFrame(bob.network.Address(), payload, false)
		return ef
	}
	held := func(ef *encryptedFrame) bool {
		var n int64
		host.database.Model(&encryptedFrame{}).Where("id = ?", ef.ID).Count(&n)
		return n == 1
	}

	// Bob types in his thread with Alice from two devices that were not online at the same time; the host hears
	// the versions out of order and keeps the newest, whichever arrived first
	thread := alice.currentUserID()
	now := time.Now().Unix()
	second := store(signedDraft(bob, thread, "hello al", now+10))
	first := store(signedDraft(bob, thread, "hello", now))
	assert.True(t, held(second))
	assert.False(t, held(first), "an older version arriving late is dropped")
	third := store(signedDraft(bob, thread, "hello alice", now+20))
	assert.True(t, held(third))
	assert.False(t, held(second), "a newer version replaces the one held")

	// A draft in another thread has its own slot, and the two slots say nothing about which threads they are
	other := store(signedDraft(bob, groupID, "group draft", now))
	assert.True(t, held(other))
	assert.True(t, held(third))
	assert.NotEqual(t, third.Slot, other.Slot)

	// Sending the message clears the draft with an empty one, which stays until the next draft replaces it so that
	// a device offline for a long time still learns the thread was cleared
	cleared := store(signedDraft(bob, thread, "", now+30))
	assert.True(t, held(cleared))
	assert.False(t, held(third))
	var stored encryptedFrame
	assert.NoError(t, host.database.First(&stored, "id = ?", cleared.ID).Error)
	assert.Equal(t, int64(0), stored.DeleteAt)

	// Every other frame carries a random slot, so nothing marks drafts out and nothing ever replaces a message
	message := store(signedDM(bob, alice.currentUserID(), "sent"))
	again := store(signedDM(bob, alice.currentUserID(), "sent again"))
	assert.Equal(t, slotSize, len(message.Slot))
	assert.NotEqual(t, message.Slot, again.Slot)
	assert.True(t, held(message))
	assert.True(t, held(again))
}

func TestStrangersCannotUseAHostAsADeadDrop(t *testing.T) {
	_, _, bob, _ := createUsersAndGroups(t)
	host, key := newEncryptedDevice(t)
	claimEncryptedDevice(t, bob, host, key)
	waitForOwnerMailboxes(t, host)

	randomLabel := make([]byte, mailboxLabelSize)
	rand.Read(randomLabel)
	randomTag := make([]byte, mailboxTagSize)
	rand.Read(randomTag)
	stranger := encryptedFrame{ID: NewID(), Payload: []byte("junk"), Slot: randomSlot(), Mailboxes: []mailboxEntry{{Label: randomLabel, Tag: randomTag, WrappedDEK: []byte("junk")}}}
	payload, _ := msgpack.Marshal(stranger)
	host.handleEncryptedFrame("stranger.onion", payload, false)
	var n int64
	host.database.Model(&encryptedFrame{}).Where("id = ?", stranger.ID).Count(&n)
	assert.Equal(t, int64(0), n, "a frame addressed to nobody the owner can read is refused")

	// The same stranger may leave a frame in one of the owner's mailboxes - that is how contacts the host has never
	// met deliver to the owner - and it lands in the untrusted tier
	var owners ownerMailbox
	assert.NoError(t, host.database.First(&owners).Error)
	rand.Read(randomTag)
	forOwner := encryptedFrame{ID: NewID(), Payload: []byte("junk"), Slot: randomSlot(), Mailboxes: []mailboxEntry{{Label: owners.Label, Tag: randomTag, WrappedDEK: []byte("junk")}}}
	payload, _ = msgpack.Marshal(forOwner)
	host.handleEncryptedFrame("stranger.onion", payload, false)
	var stored encryptedFrame
	assert.NoError(t, host.database.First(&stored, "id = ?", forOwner.ID).Error)
	assert.False(t, stored.FromOwner)
}

func TestAFrameRelayedByTwoDevicesIsStoredOnce(t *testing.T) {
	_, alice, bob, _ := createUsersAndGroups(t)
	host, key := newEncryptedDevice(t)
	claimEncryptedDevice(t, bob, host, key)
	waitForOwnerMailboxes(t, host)

	// Alice seals her message for Bob's host; Bob, having received it directly, relays it to his host as well.
	// The two copies have different IDs, different DEKs and different wraps, but their mailbox entries carry the
	// same tag.
	dm := signedDM(alice, bob.currentUserID(), "once is enough")
	fromAlice := sealForHost(t, alice, dm, host)
	fromBob := sealForHost(t, bob, dm, host)
	assert.NotEqual(t, fromAlice.ID, fromBob.ID)
	assert.Equal(t, 1, len(fromAlice.Mailboxes))
	assert.Equal(t, 1, len(fromBob.Mailboxes))
	assert.Equal(t, fromAlice.Mailboxes[0].Tag, fromBob.Mailboxes[0].Tag)
	assert.NotEqual(t, fromAlice.Mailboxes[0].WrappedDEK, fromBob.Mailboxes[0].WrappedDEK)

	for _, ef := range []*encryptedFrame{fromAlice, fromBob} {
		payload, _ := msgpack.Marshal(ef)
		host.handleEncryptedFrame(bob.network.Address(), payload, false)
	}
	// The host also holds Bob's own history, so look only for this frame's tag: one entry, on Alice's copy
	var withTag []mailboxEntry
	host.database.Where("tag = ?", fromAlice.Mailboxes[0].Tag).Find(&withTag)
	assert.Equal(t, 1, len(withTag))
	assert.Equal(t, fromAlice.ID, withTag[0].EncryptedFrameID)
	var n int64
	host.database.Model(&encryptedFrame{}).Where("id = ?", fromBob.ID).Count(&n)
	assert.Equal(t, int64(0), n)

	// The host remembers that the relayer holds the copy it kept, so it will not offer it back
	var dr deliveryRecord
	assert.NoError(t, host.database.Where("destination = ? AND frame_id = ?", bob.network.Address(), fromAlice.ID).First(&dr).Error)
}

func TestClearingHistoryDeletesCompletedEpochsOnTheHost(t *testing.T) {
	_, alice, bob, groupID := createUsersAndGroups(t)
	waitForGroupKeys(t, alice, groupID, 1)
	host, key := newEncryptedDevice(t)
	claimEncryptedDevice(t, bob, host, key)
	waitForOwnerMailboxes(t, host)

	start := time.Now().Unix()
	mailboxClock = func() int64 { return start }
	t.Cleanup(func() { mailboxClock = func() int64 { return time.Now().Unix() } })

	// Alice and Bob exchange messages this week, stored on Bob's host, alongside a group message of Alice's
	store := func(sender *Bounce, br broadcastable) ID {
		ef := sealForHost(t, sender, br, host)
		payload, _ := msgpack.Marshal(ef)
		host.handleEncryptedFrame(sender.network.Address(), payload, false)
		return ef.ID
	}
	held := func(copyID ID) bool {
		var n int64
		assert.NoError(t, host.database.Model(&encryptedFrame{}).Where("id = ?", copyID).Count(&n).Error)
		return n == 1
	}
	one := signedDM(alice, bob.currentUserID(), "one")
	two := signedDM(bob, alice.currentUserID(), "two")
	chatter := signedGroupMessage(alice, groupID, "group chatter")
	oneCopy := store(alice, one)
	twoCopy := store(bob, two)
	chatterCopy := store(alice, chatter)
	assert.True(t, held(oneCopy) && held(twoCopy) && held(chatterCopy))
	// Alice can open all three (the host also holds Bob's own history, some of it shared with her)
	openable := idsOf(framesOpenableBy(t, host, alice))
	for _, id := range []ID{one.ID, two.ID, chatter.ID} {
		assert.True(t, openable[id])
	}

	// A week later Bob clears the DM conversation: the host is told which mailboxes to drop, not which conversation
	mailboxClock = func() int64 { return start + mailboxEpochSeconds }
	clear := encryptedClearBefore{ID: NewID(), Labels: bob.clearLabelsForThread(xor(bob.currentUserID(), alice.currentUserID()), mailboxClock())}
	assert.True(t, len(clear.Labels) > 0)

	// The engines here are live.  Bob's device is still handing the host its own history, and the host has pushed
	// these messages to him, so copies sealed after this point can land on the host while the test looks at it.  The
	// clear is therefore held to what it was given: the copies and mailbox entries that were there when it was made.
	var inCleared []ID
	assert.NoError(t, host.database.Model(&mailboxEntry{}).Where("label IN ?", clear.Labels).Pluck("id", &inCleared).Error)
	assert.True(t, len(inCleared) >= 2)

	payload, _ := msgpack.Marshal(clear)
	host.handleEncryptedClearBefore(bob.network.Address(), payload, false)

	assert.False(t, held(oneCopy))
	assert.False(t, held(twoCopy))
	assert.True(t, held(chatterCopy), "the group conversation was not cleared")
	var left int64
	assert.NoError(t, host.database.Model(&mailboxEntry{}).Where("id IN ?", inCleared).Count(&left).Error)
	assert.Equal(t, int64(0), left, "nothing that was in the cleared mailboxes is left")
}

func idsOf(frames []frame) map[ID]bool {
	ids := map[ID]bool{}
	for _, f := range frames {
		ids[f.ID] = true
	}
	return ids
}

// framesOpenableBy returns, opened, every frame on a host that reader holds a mailbox for
func framesOpenableBy(t *testing.T, host *Bounce, reader *Bounce) []frame {
	t.Helper()
	var stored []encryptedFrame
	assert.NoError(t, host.database.Preload("Mailboxes").Find(&stored).Error)
	opened := []frame{}
	for i := range stored {
		if f, ok := openWith(reader, &stored[i]); ok {
			opened = append(opened, f)
		}
	}
	return opened
}

// waitForStoredCopyOf waits until a host holds a copy of a frame that reader can open, and returns the host's row
func waitForStoredCopyOf(t *testing.T, host *Bounce, reader *Bounce, id ID) encryptedFrame {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var stored []encryptedFrame
		host.database.Preload("Mailboxes").Find(&stored)
		for i := range stored {
			if f, ok := openWith(reader, &stored[i]); ok && f.ID == id {
				return stored[i]
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("host never stored a copy of %s", id)
	return encryptedFrame{}
}

// connectUsers sets up two users as contacts on the test network and returns them
func connectUsers(t *testing.T) (*Bounce, *Bounce) {
	t.Helper()
	alice := newBounceUser("Alice")
	t.Cleanup(alice.Shutdown)
	bob := newBounceUser("Bob")
	t.Cleanup(bob.Shutdown)
	introduce(t, alice, bob)
	return alice, bob
}

func TestTheHostStoresNothingThatNamesAnyone(t *testing.T) {
	quickReferenceRetries(t)
	alice, bob := connectUsers(t)
	host, key := newEncryptedDevice(t)
	claimEncryptedDevice(t, bob, host, key)
	waitForOwnerMailboxes(t, host)
	waitForUserToKnowEncryptedDevice(t, alice, bob.currentUserID(), host.network.Address())
	connectTo(t, alice, host.network.Address())

	// Bob stays connected to his host but not to Alice, so the only copy on the host is the one Alice sealed
	disconnectFrom(t, bob, alice)
	assert.NoError(t, alice.SendDirectMessage(bob.currentUserID(), "stored on bob's host", nil, NilID))
	var dm directMessage
	assert.NoError(t, alice.database.First(&dm, "text = ?", "stored on bob's host").Error)
	stored := waitForStoredCopyOf(t, host, bob, dm.ID)
	aliceUser, _ := alice.currentUser()
	bobUser, _ := bob.currentUser()
	threadID := xor(aliceUser.ID, bobUser.ID)

	// The host's row: a random ID, ciphertext, and one mailbox, the DM's; Alice's sync copy only goes to her own
	// hosts
	assert.NotEqual(t, dm.ID, stored.ID)
	assert.Equal(t, 1, len(stored.Mailboxes))
	everything := append([]byte{}, stored.Payload...)
	for _, entry := range stored.Mailboxes {
		everything = append(everything, entry.Label...)
		everything = append(everything, entry.Tag...)
		everything = append(everything, entry.WrappedDEK...)
	}
	for name, secret := range map[string][]byte{
		"alice's public key": aliceUser.PublicECDHKey,
		"bob's public key":   bobUser.PublicECDHKey,
		"the frame's id":     dm.ID[:],
		"the conversation":   threadID[:],
		"the text":           []byte("stored on bob's host"),
	} {
		assert.False(t, bytes.Contains(everything, secret), "host row must not contain %s", name)
	}

	// Nor does the schema have anywhere to put a type, a timestamp, a conversation, or a recipient key
	var columns []struct{ Name string }
	host.database.Raw("SELECT name FROM pragma_table_info('encrypted_frames')").Scan(&columns)
	for _, c := range columns {
		assert.NotEqual(t, "type", c.Name)
		assert.NotEqual(t, "timestamp", c.Name)
		assert.NotEqual(t, "batch_delete_key", c.Name)
	}
	host.database.Raw("SELECT name FROM pragma_table_info('mailbox_entries')").Scan(&columns)
	for _, c := range columns {
		assert.NotEqual(t, "public_key", c.Name)
		assert.NotEqual(t, "encrypter_key", c.Name)
	}

	// Bob is connected and asked for his mailboxes, so the host pushed the copy to him; he opened it, acked the
	// host's ID for it, and the host recorded the delivery
	deadline := time.Now().Add(5 * time.Second)
	delivered := false
	for time.Now().Before(deadline) {
		var dr deliveryRecord
		if host.database.Where("destination = ? AND frame_id = ? AND frame_type = ?", bob.network.Address(), stored.ID, typeEncryptedFrame).First(&dr).Error == nil {
			delivered = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	assert.True(t, delivered)
}

func TestAnOfflineOwnerReceivesThroughTheHostAfterReconnecting(t *testing.T) {
	quickReferenceRetries(t)
	alice, bob := connectUsers(t)
	host, key := newEncryptedDevice(t)
	claimEncryptedDevice(t, bob, host, key)
	waitForOwnerMailboxes(t, host)
	waitForUserToKnowEncryptedDevice(t, alice, bob.currentUserID(), host.network.Address())
	connectTo(t, alice, host.network.Address())

	// Bob drops off the network: no connection to Alice, none to his host
	disconnectFrom(t, bob, alice)
	disconnectFrom(t, bob, host)

	// Alice writes to him; only the host can take it
	assert.NoError(t, alice.SendDirectMessage(bob.currentUserID(), "read this later", nil, NilID))
	var dm directMessage
	assert.NoError(t, alice.database.First(&dm, "text = ?", "read this later").Error)
	stored := waitForStoredCopyOf(t, host, alice, dm.ID)
	var n int64
	bob.database.Model(&directMessage{}).Where("text = ?", "read this later").Count(&n)
	assert.Equal(t, int64(0), n, "bob has not received it yet")

	// Bob comes back and connects to his host only.  The host challenges him, he answers with his mailboxes, the
	// host offers the copy by its opaque ID, he requests it, opens it, and acks the host's ID.
	connectTo(t, bob, host.network.Address())
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		bob.database.Model(&directMessage{}).Where("text = ?", "read this later").Count(&n)
		if n == 1 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	assert.Equal(t, int64(1), n, "bob received the message through his host")

	var dr deliveryRecord
	assert.NoError(t, host.database.Where("destination = ? AND frame_id = ? AND frame_type = ?", bob.network.Address(), stored.ID, typeEncryptedFrame).First(&dr).Error)
}

// opaque hides a frame's concrete type, the way a frame known only by its type and payload would arrive
type opaque struct{ broadcastable }

// The update that replaces a user's keys must not be readable with the key it replaces, so it is sealed to the
// mailboxes two of the user's own devices share.  There is one way to seal a frame, used for a live broadcast and
// for a catch up alike, so a re-key a host asks for later is sealed the same way as one pushed to it live.
func TestAReKeyFrameIsSealedToDeviceMailboxesHoweverItReachesAHost(t *testing.T) {
	me, _, _, _ := createUsersAndGroups(t)

	// Give me a second device with a key of its own
	siblingKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	assert.NoError(t, err)
	sibling := introducedByBounce(me.currentUserID(), me, bnc01NewOnion(), time.Now().Unix())
	sibling.ECDHPublicKey = siblingKey.PublicKey().Bytes()
	assert.NoError(t, me.database.Create(&sibling).Error)
	var myDevice device
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		myDevice, _ = me.getDeviceFromAddress(me.network.Address())
		if len(myDevice.ECDHPrivateKey) > 0 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	assert.True(t, len(myDevice.ECDHPrivateKey) > 0)

	before := mustCurrentUser(me)
	assert.NoError(t, me.rollKeys())
	var reKey updateUser
	assert.NoError(t, me.database.Where("target = ? AND type = ?", me.currentUserID(), updateUserTypeReplaceKeys).First(&reKey).Error)
	after := mustCurrentUser(me)
	assert.NotEqual(t, before.PrivateECDHKey, after.PrivateECDHKey)

	// It is recognised as a re-key whether we hold the update itself or only its signed payload
	isReKey, err := me.replacesUserKeys(&reKey)
	assert.NoError(t, err)
	assert.True(t, isReKey)
	isReKey, err = me.replacesUserKeys(opaque{&reKey})
	assert.NoError(t, err)
	assert.True(t, isReKey, "the payload of an update user is a signed container, which has to be unpacked to see its type")

	// Sealed for a host, it sits in the mailbox the two devices share and nowhere else
	shared, err := me.deviceThreadSecret(myDevice.ECDHPrivateKey, sibling.ECDHPublicKey)
	assert.NoError(t, err)
	for _, frame := range []broadcastable{&reKey, opaque{&reKey}} {
		ef := me.sealForHost(frame, newTestnet().Address())
		assert.True(t, ef != nil)
		assert.Equal(t, 1, len(ef.Mailboxes))
		assert.Equal(t, mailboxLabel(shared, currentMailboxEpoch()), ef.Mailboxes[0].Label)

		// The sibling opens it with the secret it derives from its own device key
		theirs, err := me.deviceThreadSecret(siblingKey.Bytes(), myDevice.ECDHPublicKey)
		assert.NoError(t, err)
		mb := mailbox{Label: mailboxLabel(theirs, currentMailboxEpoch()), Secret: theirs, Epoch: currentMailboxEpoch()}
		index := &mailboxIndex{byLabel: map[string]mailbox{string(mb.Label): mb}}
		opened, err := index.open(receivesOf(ef)[0])
		assert.NoError(t, err)
		assert.Equal(t, reKey.ID, opened.ID)
		assert.Equal(t, typeUpdateUser, opened.Type)

		// Neither the old user key nor the new one names a mailbox it is in
		for _, userKey := range [][]byte{before.PrivateECDHKey, after.PrivateECDHKey} {
			assert.NotEqual(t, mailboxLabel(syncThreadSecret(userKey), currentMailboxEpoch()), ef.Mailboxes[0].Label)
		}
	}

	// Every host gets a copy of its own, so two hosts cannot tell they hold the same frame
	one := me.sealForHost(&reKey, newTestnet().Address())
	other := me.sealForHost(&reKey, newTestnet().Address())
	assert.NotEqual(t, one.ID, other.ID)
	assert.NotEqual(t, one.Payload, other.Payload)
}
