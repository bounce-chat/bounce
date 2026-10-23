package chat

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/alecthomas/assert/v2"
	"github.com/zeebo/blake3"
)

// A content addressed frame's ID commits its author to one definition of the frame.  These tests pin the packing,
// which is what makes two nodes agree on an ID, and show that a frame carrying someone else's content under a known
// ID is refused.

func TestThePackingSeparatesTypesFieldsAndNonces(t *testing.T) {
	n := newNonce()

	// The same bytes under two frame types are two IDs
	assert.NotEqual(t,
		newContentHasher(typeGroupCreation, n).bytes([]byte("x")).generate(),
		newContentHasher(typeUpdateGroup, n).bytes([]byte("x")).generate())

	// So are the same bytes under two nonces: identical content sent twice is two frames
	assert.NotEqual(t,
		newContentHasher(typeUpdateGroup, n).bytes([]byte("x")).generate(),
		newContentHasher(typeUpdateGroup, newNonce()).bytes([]byte("x")).generate())

	// Moving a byte across a field boundary is a different frame, so the boundary is part of the preimage
	assert.NotEqual(t,
		newContentHasher(typeUpdateGroup, n).bytes([]byte("ab")).bytes([]byte("c")).generate(),
		newContentHasher(typeUpdateGroup, n).bytes([]byte("a")).bytes([]byte("bc")).generate())

	// A nil blob and an empty one are the same content, which is where hashing an encoder's output went wrong
	assert.Equal(t,
		newContentHasher(typeUpdateGroup, n).bytes(nil).generate(),
		newContentHasher(typeUpdateGroup, n).bytes([]byte{}).generate())

	// And the packing is a pure function of its inputs
	one, two := NewID(), NewID()
	assert.Equal(t,
		newContentHasher(typeUpdateGroup, n).id(one).id(two).int64(7).uint16(3).bytes([]byte("d")).generate(),
		newContentHasher(typeUpdateGroup, n).id(one).id(two).int64(7).uint16(3).bytes([]byte("d")).generate())
}

func TestAFrameWithoutANonceIsRefused(t *testing.T) {
	me, alice, _, groupID := createUsersAndGroups(t)
	bare := updateGroup{
		Actor:     alice.currentUserID(),
		Target:    groupID,
		Type:      updateGroupTypeChangeName,
		Data:      []byte("No nonce"),
		Timestamp: time.Now().Unix(),
	}
	bare.ID = bare.generateID()
	assert.False(t, idMatchesContent(&bare))
	_, accepted := me.handleUpdateGroup(alice.network.Address(), signedBy(t, alice, bare), false)
	assert.False(t, accepted, "an update whose author left out the nonce")
	assert.Error(t, me.database.Create(&bare).Error)

	// The sending path supplies one, so locally authored updates never hit this
	assert.NoError(t, alice.applyAndBroadcastUpdateGroup(&bare))
	assert.NotEqual(t, noNonce, bare.Nonce)
	assert.True(t, idMatchesContent(&bare))
}

// signedBy wraps an update group the way the sending engine does, with b's device signing it
func signedBy(t *testing.T, b *Bounce, ug updateGroup) []byte {
	t.Helper()
	payload, err := msgpack.Marshal(ug)
	assert.NoError(t, err)
	frame, err := msgpack.Marshal(b.createSignedContainer(typeUpdateGroup, payload))
	assert.NoError(t, err)
	return frame
}

func TestAnUpdateGroupCannotBeRedefinedUnderItsID(t *testing.T) {
	me, alice, _, groupID := createUsersAndGroups(t)
	rename := updateGroup{
		Nonce:     newNonce(),
		Actor:     alice.currentUserID(),
		Target:    groupID,
		Type:      updateGroupTypeChangeName,
		Data:      []byte("Book club"),
		Timestamp: time.Now().Unix(),
	}
	rename.ID = rename.generateID()

	// Alice's real rename is accepted
	_, accepted := me.handleUpdateGroup(alice.network.Address(), signedBy(t, alice, rename), false)
	assert.True(t, accepted)

	// The same frame with different content under the same ID is refused, even though Alice signed it too
	other := rename
	other.Data = []byte("Fight club")
	_, accepted = me.handleUpdateGroup(alice.network.Address(), signedBy(t, alice, other), false)
	assert.False(t, accepted, "a second definition of the update")

	// And the one we hold is the first definition
	var stored updateGroup
	assert.NoError(t, me.database.First(&stored, "id = ?", rename.ID).Error)
	assert.Equal(t, "Book club", string(stored.Data))

	// A locally built update with an ID that is not its content cannot even be saved
	wrong := rename
	wrong.Data = []byte("Chess club")
	assert.Error(t, me.database.Create(&wrong).Error)
}

func TestAGroupCreationCannotBeRedefinedUnderItsID(t *testing.T) {
	me, alice, _, groupID := createUsersAndGroups(t)
	var original groupCreation
	assert.NoError(t, me.database.First(&original, "id = ?", groupID).Error)

	// Re-encode the founding group with a different name but the same ID, signed by the real creator
	var g group
	assert.NoError(t, msgpack.Unmarshal(original.Data, &g))
	g.Name = "Not the group you joined"
	tampered := groupCreation{ID: original.ID, Nonce: original.Nonce, Timestamp: original.Timestamp}
	var err error
	tampered.Data, err = msgpack.Marshal(g)
	assert.NoError(t, err)
	tampered.OriginalPayload, err = msgpack.Marshal(tampered)
	assert.NoError(t, err)
	frame, err := msgpack.Marshal(me.createSignedContainer(typeGroupCreation, tampered.OriginalPayload))
	assert.NoError(t, err)

	_, accepted := alice.handleGroupCreation(me.network.Address(), frame, false)
	assert.False(t, accepted, "a creation whose content is not what its ID names")
	assert.NotEqual(t, tampered.ID, tampered.generateID())
	assert.Equal(t, original.ID, original.generateID(), "the genuine creation does carry its content's ID")
}

// Add users and confirmations are content addressed like every other stored frame.  Confirmations additionally
// collapse by what they mean: a device confirms an update once, so repeating one under new IDs stores nothing.
func TestAnAddUserCannotBeRedefinedUnderItsID(t *testing.T) {
	me, _, _, _ := createUsersAndGroups(t)

	// A new contact and I introduce each other: they offered, my device requested and signed their record
	them := bnc01NewOnion()
	theirID := NewID()
	theirUser := user{ID: theirID, Name: "New contact", Devices: []device{bnc01Original(theirID, them.addr(), time.Now().Unix())}}
	theirBytes, err := msgpack.Marshal(theirUser)
	assert.NoError(t, err)
	myBytes := bnc01MarshalUser(me)
	theirHash, myHash := blake3.Sum256(theirBytes), blake3.Sum256(myBytes)
	au := addUser{
		Nonce:              newNonce(),
		Xor:                xor(theirID, me.currentUserID()),
		Timestamp:          time.Now().Unix(),
		OfferUser:          theirBytes,
		RequesterUser:      myBytes,
		OfferDevice:        them.addr(),
		RequesterDevice:    me.network.Address(),
		RequesterSignature: me.network.Sign(theirHash[:]),
		OfferSignature:     them.sign(myHash[:]),
	}
	au.ID = au.generateID()
	payload, err := msgpack.Marshal(&au)
	assert.NoError(t, err)
	_, accepted := me.handleAddUser(them.addr(), payload, false)
	assert.True(t, accepted)

	// With the introduction time moved under the same ID, it is refused
	tampered := au
	tampered.Timestamp = au.Timestamp + 3600
	payload, err = msgpack.Marshal(&tampered)
	assert.NoError(t, err)
	refused, fresh := me.handleAddUser(them.addr(), payload, false)
	assert.Zero(t, refused)
	assert.False(t, fresh)
	var stored addUser
	assert.NoError(t, me.database.First(&stored, "id = ?", au.ID).Error)
	assert.Equal(t, au.Timestamp, stored.Timestamp)

	// Offered again as is, it is recognised rather than refused
	payload, err = msgpack.Marshal(&au)
	assert.NoError(t, err)
	existing, fresh := me.handleAddUser(them.addr(), payload, false)
	assert.NotZero(t, existing)
	assert.False(t, fresh)
}

// confirmationBy builds the confirmation signer's device would send for an update
func confirmationBy(signer *Bounce, ug updateGroup, timestamp int64) confirmation {
	c := confirmation{
		Nonce:         newNonce(),
		UpdateGroupID: ug.ID,
		SigningDevice: signer.network.Address(),
		Signature:     signer.network.Sign(ug.ID[:]),
		Timestamp:     timestamp,
	}
	c.ID = c.generateID()
	return c
}

func confirmationsHeld(t *testing.T, b *Bounce, updateGroupID ID) int64 {
	t.Helper()
	var n int64
	assert.NoError(t, b.database.Model(&confirmation{}).Where("update_group_id = ?", updateGroupID).Count(&n).Error)
	return n
}

func TestAConfirmationCannotBeRedefinedUnderItsID(t *testing.T) {
	me, alice, _, groupID := createUsersAndGroups(t)
	rename := updateGroup{
		Nonce:     newNonce(),
		Actor:     alice.currentUserID(),
		Target:    groupID,
		Type:      updateGroupTypeChangeName,
		Data:      []byte("Renamed"),
		Timestamp: time.Now().Unix(),
	}
	rename.ID = rename.generateID()
	_, accepted := me.handleUpdateGroup(alice.network.Address(), signedBy(t, alice, rename), false)
	assert.True(t, accepted)

	// Alice's confirmation of her own rename is accepted
	c := confirmationBy(alice, rename, time.Now().Unix())
	_, accepted = me.handleConfirmation(alice.network.Address(), c.getPayload(), false)
	assert.True(t, accepted)

	// The same ID over a different timestamp is a different confirmation and is refused
	other := c
	other.Timestamp = c.Timestamp + 60
	_, accepted = me.handleConfirmation(alice.network.Address(), other.getPayload(), false)
	assert.False(t, accepted)
	var stored confirmation
	assert.NoError(t, me.database.First(&stored, "id = ?", c.ID).Error)
	assert.Equal(t, c.Timestamp, stored.Timestamp)
}

func TestADeviceConfirmsAnUpdateOnce(t *testing.T) {
	me, alice, bob, groupID := createUsersAndGroups(t)
	rename := updateGroup{
		Nonce:     newNonce(),
		Actor:     alice.currentUserID(),
		Target:    groupID,
		Type:      updateGroupTypeChangeName,
		Data:      []byte("Renamed again"),
		Timestamp: time.Now().Unix(),
	}
	rename.ID = rename.generateID()
	_, accepted := me.handleUpdateGroup(alice.network.Address(), signedBy(t, alice, rename), false)
	assert.True(t, accepted)
	before := confirmationsHeld(t, me, rename.ID)

	// Bob's first confirmation is stored and passed on
	first := confirmationBy(bob, rename, time.Now().Unix())
	_, accepted = me.handleConfirmation(bob.network.Address(), first.getPayload(), false)
	assert.True(t, accepted)
	assert.Equal(t, before+1, confirmationsHeld(t, me, rename.ID))

	// A hundred more from the same device, each a well-formed frame with its own nonce and ID, store nothing
	for i := 1; i <= 100; i++ {
		again := confirmationBy(bob, rename, time.Now().Unix()+int64(i))
		passedOn, accepted := me.handleConfirmation(bob.network.Address(), again.getPayload(), false)
		assert.False(t, accepted)
		assert.Zero(t, passedOn)
	}
	assert.Equal(t, before+1, confirmationsHeld(t, me, rename.ID))

	// Offering the one we hold again is recognised, so delivery is tracked for it
	held, fresh := me.handleConfirmation(bob.network.Address(), first.getPayload(), false)
	assert.NotZero(t, held)
	assert.False(t, fresh)
}

// Every frame whose content never changes once stored is content addressed.  For each one, the real thing from its
// author is accepted and a second definition of it under the same ID, from the same author, is refused with the
// first kept.  That is the whole point: an ID commits its author to one definition of the frame.

// signedFrame wraps a frame the way the sending engine does, with b's device signing it
func signedFrame(t *testing.T, b *Bounce, frameType uint16, f interface{}) []byte {
	t.Helper()
	payload, err := msgpack.Marshal(f)
	assert.NoError(t, err)
	frame, err := msgpack.Marshal(b.createSignedContainer(frameType, payload))
	assert.NoError(t, err)
	return frame
}

func TestADirectMessageCannotBeRedefinedUnderItsID(t *testing.T) {
	me, alice, _, _ := createUsersAndGroups(t)
	dm := directMessage{
		Nonce:     newNonce(),
		WrittenAt: time.Now().Unix(),
		Author:    alice.currentUserID(),
		Xor:       xor(alice.currentUserID(), me.currentUserID()),
		Text:      "Dinner at eight",
	}
	dm.ID = dm.generateID()
	_, accepted := me.handleDirectMessage(alice.network.Address(), signedFrame(t, alice, typeDirectMessage, dm), false)
	assert.True(t, accepted)

	other := dm
	other.Text = "Dinner at nine"
	_, accepted = me.handleDirectMessage(alice.network.Address(), signedFrame(t, alice, typeDirectMessage, other), false)
	assert.False(t, accepted, "a second text under the same message ID")

	var stored directMessage
	assert.NoError(t, me.database.First(&stored, "id = ?", dm.ID).Error)
	assert.Equal(t, "Dinner at eight", stored.Text)
}

func TestAMessagesAttachmentsArePartOfItsContent(t *testing.T) {
	_, alice, _, groupID := createUsersAndGroups(t)
	gm := groupMessage{
		Nonce:       newNonce(),
		WrittenAt:   time.Now().Unix(),
		Author:      alice.currentUserID(),
		Destination: groupID,
		Text:        "see attached",
		FileAttachments: []fileAttachment{
			{ID: NewID(), FileID: NewID(), Name: "minutes.pdf", Size: 1024},
		},
	}
	withAttachment := gm.generateID()

	// Changing anything about the attachment, or dropping it, is a different message
	renamed := gm
	renamed.FileAttachments = []fileAttachment{{ID: gm.FileAttachments[0].ID, FileID: gm.FileAttachments[0].FileID, Name: "agenda.pdf", Size: 1024}}
	assert.NotEqual(t, withAttachment, renamed.generateID())
	swapped := gm
	swapped.FileAttachments = []fileAttachment{{ID: gm.FileAttachments[0].ID, FileID: NewID(), Name: "minutes.pdf", Size: 1024}}
	assert.NotEqual(t, withAttachment, swapped.generateID())
	none := gm
	none.FileAttachments = nil
	assert.NotEqual(t, withAttachment, none.generateID())

	// Whereas the message ID written into the attachment row is not, since it is what is being generated
	linked := gm
	linked.FileAttachments = []fileAttachment{{ID: gm.FileAttachments[0].ID, FileID: gm.FileAttachments[0].FileID, MessageID: withAttachment, Name: "minutes.pdf", Size: 1024}}
	assert.Equal(t, withAttachment, linked.generateID())
}

func TestASentMessageCarriesItsContentID(t *testing.T) {
	me, alice, _, groupID := createUsersAndGroups(t)
	me.SendGroupMessage(groupID, "hello everyone", nil)
	var sent groupMessage
	assert.NoError(t, me.database.Preload("FileAttachments").Preload("ImageAttachments").Where("author = ? AND text = ?", me.currentUserID(), "hello everyone").First(&sent).Error)
	assert.True(t, idMatchesContent(&sent))
	awaitAck(t, me, alice, typeGroupMessage, sent.ID)

	me.SendDirectMessage(alice.currentUserID(), "just you", nil)
	var dm directMessage
	assert.NoError(t, me.database.Where("author = ? AND text = ?", me.currentUserID(), "just you").First(&dm).Error)
	assert.True(t, idMatchesContent(&dm))
}

func TestAnUpdateDMCannotBeRedefinedUnderItsID(t *testing.T) {
	me, alice, _, _ := createUsersAndGroups(t)
	ud := updateDM{
		Nonce:     newNonce(),
		Actor:     alice.currentUserID(),
		Target:    xor(alice.currentUserID(), me.currentUserID()),
		Timestamp: time.Now().Unix(),
		Type:      updateDMTypeChangeRetention,
		Data:      []byte{0, 0, 0, 0, 0, 0, 0, 1},
	}
	ud.ID = ud.generateID()
	_, accepted := me.handleUpdateDM(alice.network.Address(), signedFrame(t, alice, typeUpdateDM, ud), false)
	assert.True(t, accepted)

	other := ud
	other.Data = []byte{0, 0, 0, 0, 0, 0, 0, 2}
	_, accepted = me.handleUpdateDM(alice.network.Address(), signedFrame(t, alice, typeUpdateDM, other), false)
	assert.False(t, accepted)
}

func TestAnUpdateUserCannotBeRedefinedUnderItsID(t *testing.T) {
	me, alice, _, _ := createUsersAndGroups(t)
	uu := updateUser{
		Nonce:     newNonce(),
		Target:    alice.currentUserID(),
		Timestamp: time.Now().Unix(),
		Type:      updateUserTypeUpdateName,
		Data:      []byte("Alice B"),
	}
	uu.ID = uu.generateID()
	_, accepted := me.handleUpdateUser(alice.network.Address(), signedFrame(t, alice, typeUpdateUser, uu), false)
	assert.True(t, accepted)

	other := uu
	other.Data = []byte("Alice C")
	_, accepted = me.handleUpdateUser(alice.network.Address(), signedFrame(t, alice, typeUpdateUser, other), false)
	assert.False(t, accepted)
	var stored updateUser
	assert.NoError(t, me.database.First(&stored, "id = ?", uu.ID).Error)
	assert.Equal(t, "Alice B", string(stored.Data))
}

func TestAnUpdateDeviceCannotBeRedefinedUnderItsID(t *testing.T) {
	me, alice, _, _ := createUsersAndGroups(t)
	aliceDevice, ok := me.getDeviceFromAddress(alice.network.Address())
	assert.True(t, ok)
	ud := updateDevice{
		Nonce:     newNonce(),
		Target:    aliceDevice.ID,
		Type:      updateDeviceTypeUpdateName,
		Data:      []byte("Laptop"),
		Timestamp: time.Now().Unix(),
	}
	ud.ID = ud.generateID()
	_, accepted := me.handleUpdateDevice(alice.network.Address(), signedFrame(t, alice, typeUpdateDevice, ud), false)
	assert.True(t, accepted)

	other := ud
	other.Data = []byte("Desktop")
	_, accepted = me.handleUpdateDevice(alice.network.Address(), signedFrame(t, alice, typeUpdateDevice, other), false)
	assert.False(t, accepted)
}

func TestAReadReceiptCannotBeRedefinedUnderItsID(t *testing.T) {
	me, alice, _, groupID := createUsersAndGroups(t)
	me.SendGroupMessage(groupID, "read me", nil)
	var sent groupMessage
	assert.NoError(t, me.database.Where("text = ?", "read me").First(&sent).Error)
	awaitAck(t, me, alice, typeGroupMessage, sent.ID)

	rr := readReceipt{
		Nonce:      newNonce(),
		Actor:      alice.currentUserID(),
		Target:     sent.ID,
		TargetType: typeGroupMessage,
		Timestamp:  time.Now().Unix(),
	}
	rr.ID = rr.generateID()
	_, accepted := me.handleReadReceipt(alice.network.Address(), signedFrame(t, alice, typeReadReceipt, rr), false)
	assert.True(t, accepted)

	other := rr
	other.Timestamp = rr.Timestamp + 60
	_, accepted = me.handleReadReceipt(alice.network.Address(), signedFrame(t, alice, typeReadReceipt, other), false)
	assert.False(t, accepted)
}

func TestADeviceCannotBeRedefinedUnderItsID(t *testing.T) {
	me, alice, _, _ := createUsersAndGroups(t)
	phone := bnc01NewOnion()
	dev := introducedByBounce(alice.currentUserID(), alice, phone, time.Now().Unix())
	_, accepted := me.handleDevice(alice.network.Address(), dev.getPayload(), false)
	assert.True(t, accepted)

	// The same device ID presented with another address and a genuine introduction of that address: refused, so a
	// later revocation of this ID cannot mean different devices on different nodes
	tablet := bnc01NewOnion()
	other := introducedByBounce(alice.currentUserID(), alice, tablet, dev.Timestamp)
	other.ID = dev.ID
	_, accepted = me.handleDevice(alice.network.Address(), other.getPayload(), false)
	assert.False(t, accepted)
	_, known := me.getDeviceFromAddress(tablet.addr())
	assert.False(t, known)

	// A device whose introduction was altered after the ID was generated is also refused
	stranger := bnc01NewOnion()
	forged := introducedByBounce(alice.currentUserID(), alice, bnc01NewOnion(), time.Now().Unix())
	forged.Signature.PreexistingDevice = stranger.addr()
	_, accepted = me.handleDevice(alice.network.Address(), forged.getPayload(), false)
	assert.False(t, accepted)

	// And a device without a content ID inside a whole user record makes the record invalid everywhere
	var aliceRecord user
	assert.NoError(t, me.database.Preload("Devices.Signature").First(&aliceRecord, "id = ?", alice.currentUserID()).Error)
	mislabelled := introducedByBounce(alice.currentUserID(), alice, bnc01NewOnion(), time.Now().Unix())
	mislabelled.ID = NewID()
	aliceRecord.Devices = append(aliceRecord.Devices, mislabelled)
	assert.False(t, me.hasValidDeviceGroup(aliceRecord))
}

// Files are content addressed too, with one exception made for what refers to them: the message, group or user that
// carries a file names the file's ID in its own content, so the file's ID is generated before they exist and cannot
// include what it is attached to or sent towards.

func TestAFileCannotBeRedefinedUnderItsID(t *testing.T) {
	me, alice, _, groupID := createUsersAndGroups(t)
	data := []byte("the minutes of the meeting")
	assert.NoError(t, me.SendGroupMessage(groupID, "minutes attached", []OutgoingAttachment{{
		Name: "minutes.txt", Size: int64(len(data)), Reader: io.NopCloser(bytes.NewReader(data)),
	}}))
	var sent groupMessage
	assert.NoError(t, me.database.Preload("FileAttachments").First(&sent, "text = ?", "minutes attached").Error)
	var f file
	assert.NoError(t, me.database.First(&f, "id = ?", sent.FileAttachments[0].FileID).Error)
	assert.True(t, idMatchesContent(&f))
	assert.Equal(t, sent.ID, f.AttachedTo, "the file names the message that carries it")
	awaitAck(t, me, alice, typeFile, f.ID)

	// The same file ID claiming different data is refused, even signed by the real author
	other := f
	other.Hash = hashString(blake3.Sum256([]byte("different minutes")))
	other.OriginalPayload, _ = msgpack.Marshal(other)
	frame, err := msgpack.Marshal(me.createSignedContainer(typeFile, other.OriginalPayload))
	assert.NoError(t, err)
	_, accepted := alice.handleFile(me.network.Address(), frame, false)
	assert.False(t, accepted)

	// Whereas where it is attached is not content, so the author may attach one file to two things under one ID
	elsewhere := f
	elsewhere.AttachedTo = NewID()
	assert.Equal(t, f.ID, elsewhere.generateID())
}

func TestAnIconIsPreparedBeforeWhatCarriesIt(t *testing.T) {
	me, _, _, _ := createUsersAndGroups(t)
	image := []byte("\x89PNG\r\n\x1a\nnot really a png but large enough to be a blob")

	// A new group names its icon in the creation, and the icon's file carries the content ID and is attached to the group
	assert.NoError(t, me.CreateGroup(NewGroup{Name: "Iconic", Image: image, Retention: 3600}))
	var g group
	assert.NoError(t, me.database.First(&g, "name = ?", "Iconic").Error)
	iconID, err := ParseID(g.Images)
	assert.NoError(t, err)
	var icon file
	assert.NoError(t, me.database.First(&icon, "id = ?", iconID).Error)
	assert.True(t, idMatchesContent(&icon))
	assert.Equal(t, g.ID, icon.AttachedTo)
	assert.Equal(t, g.ID, icon.Destination)
}

func TestAGroupCreationIsBoundedLikeAnInvite(t *testing.T) {
	me, alice, _, _ := createUsersAndGroups(t)

	// A creation whose group blob is over the invite cap is refused before anything looks inside it
	oversized := groupCreation{Nonce: newNonce(), Timestamp: time.Now().Unix(), Data: make([]byte, maximumUserRecordBytes+1)}
	oversized.ID = oversized.generateID()
	var err error
	oversized.OriginalPayload, err = msgpack.Marshal(oversized)
	assert.NoError(t, err)
	frame, err := msgpack.Marshal(alice.createSignedContainer(typeGroupCreation, oversized.OriginalPayload))
	assert.NoError(t, err)
	_, accepted := me.handleGroupCreation(alice.network.Address(), frame, false)
	assert.False(t, accepted)
	var stored int64
	assert.NoError(t, me.database.Model(&groupCreation{}).Where("id = ?", oversized.ID).Count(&stored).Error)
	assert.Equal(t, int64(0), stored)

	// The largest legitimate creation, a creator with the maximum devices and the longest names, is well under it
	creator, _ := alice.currentUser()
	for i := 1; i < maximumDevicesPerUser; i++ {
		creator.Devices = append(creator.Devices, introducedByBounce(creator.ID, alice, bnc01NewOnion(), time.Now().Unix()))
	}
	creator.Name = strings.Repeat("名", MaximumNameLength)
	largest, err := msgpack.Marshal(group{
		Name: strings.Repeat("名", MaximumNameLength), Images: NewID().String(), CreatedBy: creator.ID,
		CreatedAt: time.Now().Unix(), Users: []user{creator}, Admins: creator.ID.String(),
	})
	assert.NoError(t, err)
	assert.True(t, len(largest) < maximumUserRecordBytes*2/3, "headroom under the cap")
}
