package chat

import (
	"bytes"
	crand "crypto/rand"
	"image"
	"image/color"
	"image/png"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/alecthomas/assert/v2"
	"github.com/zeebo/blake3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestDeletingAMessageDeletesAllFileData(t *testing.T) {
	b, _, _, groupID := createUsersAndGroups(t)

	data := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09}
	text := "this message contains an embedded file"
	assert.NoError(t, b.SendGroupMessage(groupID, text, []OutgoingAttachment{{
		Name:   "filename",
		Size:   int64(len(data)),
		Reader: io.NopCloser(bytes.NewReader(data)),
	}}, NilID))

	// Make sure that all the database objects were created, with the file ID the engine assigned
	var createdGM groupMessage
	err := b.database.Preload("FileAttachments").First(&createdGM, "text = ?", text).Error
	assert.NoError(t, err)
	assert.Equal(t, 1, len(createdGM.FileAttachments))
	fileID := createdGM.FileAttachments[0].FileID

	var createdFileAttachment fileAttachment
	err = b.database.First(&createdFileAttachment, "message_id = ? AND file_id = ?", createdGM.ID, fileID).Error
	assert.NoError(t, err)

	var createdFile file
	err = b.database.First(&createdFile, "id = ?", fileID).Error
	assert.NoError(t, err)

	var createdChunk chunk
	err = b.database.First(&createdChunk, "file_id = ?", fileID).Error
	assert.NoError(t, err)

	// Delete the message
	err = b.database.Clauses(clause.Returning{}).Where("id = ?", createdGM.ID).Delete(&groupMessage{}).Error
	assert.NoError(t, err)

	// Make sure that all the database objects were deleted
	err = b.database.First(&createdGM, "id = ?", createdGM.ID).Error
	assert.Equal(t, gorm.ErrRecordNotFound, err)

	err = b.database.First(&createdFileAttachment, "message_id = ? AND file_id = ?", createdGM.ID, fileID).Error
	assert.Equal(t, gorm.ErrRecordNotFound, err)

	err = b.database.First(&createdFile, "id = ?", fileID).Error
	assert.Equal(t, gorm.ErrRecordNotFound, err)

	err = b.database.First(&createdChunk, "file_id = ?", fileID).Error
	assert.Equal(t, gorm.ErrRecordNotFound, err)
}

// Child rows are removed by their parent's AfterDelete hook, and GORM runs that hook once per batch unless the
// delete goes through deleteEach.  Every cascade that can match more than one row is exercised here: the retention
// prune (many messages, each with many attachments) and deleting a group (many messages, updates and files).
func TestDeletingManyMessagesDeletesAllOfTheirFileData(t *testing.T) {
	b, _, _, groupID := createUsersAndGroups(t)

	sendWithTwoAttachments := func(text string) (ID, []ID) {
		attachments := []OutgoingAttachment{}
		for _, name := range []string{"one", "two"} {
			data := []byte(text + " " + name)
			attachments = append(attachments, OutgoingAttachment{Name: name, Size: int64(len(data)), Reader: io.NopCloser(bytes.NewReader(data))})
		}
		assert.NoError(t, b.SendGroupMessage(groupID, text, attachments, NilID))
		var gm groupMessage
		assert.NoError(t, b.database.Preload("FileAttachments").First(&gm, "text = ?", text).Error)
		assert.Equal(t, 2, len(gm.FileAttachments))
		return gm.ID, []ID{gm.FileAttachments[0].FileID, gm.FileAttachments[1].FileID}
	}
	count := func(table, where string, ids []ID) int64 {
		var n int64
		assert.NoError(t, b.database.Table(table).Where(where, ids).Count(&n).Error)
		return n
	}

	first, firstFiles := sendWithTwoAttachments("expires first")
	second, secondFiles := sendWithTwoAttachments("expires second")
	messages := []ID{first, second}
	files := append(firstFiles, secondFiles...)
	assert.Equal(t, int64(4), count("files", "id IN ?", files))

	// Both expire at once, through the engine's own prune
	assert.NoError(t, b.database.Table("group_messages").Where("id IN ?", messages).Update("delete_at", time.Now().Unix()-1).Error)
	b.pruneGroupMessages()
	assert.Equal(t, int64(0), count("group_messages", "id IN ?", messages))
	assert.Equal(t, int64(0), count("file_attachments", "message_id IN ?", messages))
	assert.Equal(t, int64(0), count("files", "id IN ?", files))
	assert.Equal(t, int64(0), count("chunks", "file_id IN ?", files))

	// Deleting the group cascades through every message it still has
	third, thirdFiles := sendWithTwoAttachments("deleted with the group")
	fourth, fourthFiles := sendWithTwoAttachments("also deleted with the group")
	assert.NoError(t, deleteEach[group](b.database, "id = ?", groupID))
	assert.Equal(t, int64(0), count("group_messages", "id IN ?", []ID{third, fourth}))
	assert.Equal(t, int64(0), count("files", "id IN ?", append(thirdFiles, fourthFiles...)))
	assert.Equal(t, int64(0), count("group_creations", "id IN ?", []ID{groupID}))
}

func TestUnauthorizedScopeFilesAndChunkOffersAreRejected(t *testing.T) {
	me, alice, bob, groupID := createUsersAndGroups(t)

	// Remove Bob from the shared group so he is still a known, unblocked user on Me,
	// but no longer a member of groupID and no longer sharing any group with Alice.
	assert.NoError(t, me.RemoveUserFromGroup(groupID, bob.currentUserID()))
	await(t, alice, "SetGroupState")
	gs, err := me.currentGroupState(groupID)
	assert.NoError(t, err)
	assert.False(t, gs.isMember(bob.currentUserID()))

	makeSignedFile := func(author *Bounce, scope, fileType int, dest ID) (*file, []byte) {
		data := []byte("scope auth test payload " + NewID().String())
		key := make([]byte, 32)
		_, err := crand.Read(key)
		assert.NoError(t, err)
		hash := blake3.Sum256(data)
		f := &file{
			Nonce:       newNonce(),
			Name:        "payload.bin",
			Type:        fileType,
			AttachedTo:  dest,
			Hash:        hashString(hash),
			Size:        int64(len(data)),
			Scope:       scope,
			Destination: dest,
			Author:      author.currentUserID(),
			Timestamp:   time.Now().Unix(),
			Key:         key,
		}
		_, hashList, encryptedHashList := splitChunks(NewID(), data, key)
		f.HashList = hashList
		f.EncryptedHashList = encryptedHashList
		f.ID = f.generateID()
		f.OriginalPayload, err = msgpack.Marshal(f)
		assert.NoError(t, err)
		sc := author.createSignedContainer(typeFile, f.OriginalPayload)
		wire, err := msgpack.Marshal(sc)
		assert.NoError(t, err)
		return f, wire
	}

	makeSignedChunkOffer := func(author *Bounce, scope int, dest, fileID ID, chunkHash string) (*chunkOffer, []byte) {
		co := &chunkOffer{
			ID:          NewID(),
			Scope:       scope,
			Destination: dest,
			Author:      author.currentUserID(),
			FileID:      fileID,
			Hash:        chunkHash,
			Location:    author.network.Address(),
			Timestamp:   time.Now().Unix(),
		}
		var err error
		co.OriginalPayload, err = msgpack.Marshal(co)
		assert.NoError(t, err)
		sc := author.createSignedContainer(typeChunkOffer, co.OriginalPayload)
		wire, err := msgpack.Marshal(sc)
		assert.NoError(t, err)
		return co, wire
	}

	assertFileAbsent := func(id ID) {
		var count int64
		assert.NoError(t, me.database.Model(&file{}).Where("id = ?", id).Count(&count).Error)
		assert.Equal(t, int64(0), count)
	}
	assertOfferAbsent := func(id ID) {
		var count int64
		assert.NoError(t, me.database.Model(&chunkOffer{}).Where("id = ?", id).Count(&count).Error)
		assert.Equal(t, int64(0), count)
	}

	// 1. Removed member Bob cannot inject scopeGroup or scopeGroupWithInvites files or chunk offers
	groupFile, groupFileWire := makeSignedFile(bob, scopeGroup, fileTypeMessageAttachment, groupID)
	br, first := me.handleFile(bob.network.Address(), groupFileWire, false)
	assert.True(t, br == nil && !first, "removed member's scopeGroup file must be rejected")
	assertFileAbsent(groupFile.ID)

	groupOffer, groupOfferWire := makeSignedChunkOffer(bob, scopeGroup, groupID, groupFile.ID, groupFile.HashList)
	br, first = me.handleChunkOffer(bob.network.Address(), groupOfferWire, false)
	assert.True(t, br == nil && !first, "removed member's scopeGroup chunkOffer must be rejected")
	assertOfferAbsent(groupOffer.ID)

	groupIconFile, groupIconFileWire := makeSignedFile(bob, scopeGroupWithInvites, fileTypeGroupImage, groupID)
	br, first = me.handleFile(bob.network.Address(), groupIconFileWire, false)
	assert.True(t, br == nil && !first, "removed member's unreferenced scopeGroupWithInvites file must be rejected")
	assertFileAbsent(groupIconFile.ID)

	groupIconOffer, groupIconOfferWire := makeSignedChunkOffer(bob, scopeGroupWithInvites, groupID, groupIconFile.ID, groupIconFile.HashList)
	br, first = me.handleChunkOffer(bob.network.Address(), groupIconOfferWire, false)
	assert.True(t, br == nil && !first, "removed member's scopeGroupWithInvites chunkOffer must be rejected")
	assertOfferAbsent(groupIconOffer.ID)

	// 2. Bob cannot inject a scopeUser file or chunkOffer targeting Me's DM thread with Alice
	meAliceXor := xor(me.currentUserID(), alice.currentUserID())
	thirdPartyFile, thirdPartyFileWire := makeSignedFile(bob, scopeUser, fileTypeMessageAttachment, meAliceXor)
	br, first = me.handleFile(bob.network.Address(), thirdPartyFileWire, false)
	assert.True(t, br == nil && !first, "third-party scopeUser file must be rejected")
	assertFileAbsent(thirdPartyFile.ID)

	thirdPartyOffer, thirdPartyOfferWire := makeSignedChunkOffer(bob, scopeUser, meAliceXor, thirdPartyFile.ID, thirdPartyFile.HashList)
	br, first = me.handleChunkOffer(bob.network.Address(), thirdPartyOfferWire, false)
	assert.True(t, br == nil && !first, "third-party scopeUser chunkOffer must be rejected")
	assertOfferAbsent(thirdPartyOffer.ID)

	// Bob's legitimate scopeUser file and chunkOffer to Me ARE accepted
	meBobXor := xor(me.currentUserID(), bob.currentUserID())
	validDMFile, validDMFileWire := makeSignedFile(bob, scopeUser, fileTypeMessageAttachment, meBobXor)
	br, first = me.handleFile(bob.network.Address(), validDMFileWire, false)
	assert.True(t, br != nil && first, "counterparty's own scopeUser file must be accepted")

	validDMOffer, validDMOfferWire := makeSignedChunkOffer(bob, scopeUser, meBobXor, validDMFile.ID, validDMFile.HashList)
	br, first = me.handleChunkOffer(bob.network.Address(), validDMOfferWire, false)
	assert.True(t, br != nil && first, "counterparty's own scopeUser chunkOffer must be accepted")
	assert.Equal(t, validDMOffer.ID, br.getID())

	// 3. Another user cannot inject scopeSync files or chunk offers
	syncFile, syncFileWire := makeSignedFile(bob, scopeSync, fileTypeMessageAttachment, NilID)
	br, first = me.handleFile(bob.network.Address(), syncFileWire, false)
	assert.True(t, br == nil && !first, "foreign scopeSync file must be rejected")
	assertFileAbsent(syncFile.ID)

	syncOffer, syncOfferWire := makeSignedChunkOffer(bob, scopeSync, NilID, syncFile.ID, syncFile.HashList)
	br, first = me.handleChunkOffer(bob.network.Address(), syncOfferWire, false)
	assert.True(t, br == nil && !first, "foreign scopeSync chunkOffer must be rejected")
	assertOfferAbsent(syncOffer.ID)

	// 4. scopeGlobal: Bob cannot publish a file with Destination != Bob or offer chunks for unrelated Alice
	spoofGlobalFile, spoofGlobalFileWire := makeSignedFile(bob, scopeGlobal, fileTypeUserImage, alice.currentUserID())
	br, first = me.handleFile(bob.network.Address(), spoofGlobalFileWire, false)
	assert.True(t, br == nil && !first, "scopeGlobal file targeting another user must be rejected")
	assertFileAbsent(spoofGlobalFile.ID)

	unrelatedGlobalOffer, unrelatedGlobalOfferWire := makeSignedChunkOffer(bob, scopeGlobal, alice.currentUserID(), spoofGlobalFile.ID, spoofGlobalFile.HashList)
	br, first = me.handleChunkOffer(bob.network.Address(), unrelatedGlobalOfferWire, false)
	assert.True(t, br == nil && !first, "scopeGlobal chunkOffer for user not sharing a group must be rejected")
	assertOfferAbsent(unrelatedGlobalOffer.ID)

	// Alice (still in groupID with Me) can publish her own avatar and offer chunks of Me's avatar
	aliceAvatar, aliceAvatarWire := makeSignedFile(alice, scopeGlobal, fileTypeUserImage, alice.currentUserID())
	br, first = me.handleFile(alice.network.Address(), aliceAvatarWire, false)
	assert.True(t, br != nil && first, "user's own scopeGlobal avatar file must be accepted")

	aliceMeAvatarOffer, aliceMeAvatarOfferWire := makeSignedChunkOffer(alice, scopeGlobal, me.currentUserID(), aliceAvatar.ID, aliceAvatar.HashList)
	br, first = me.handleChunkOffer(alice.network.Address(), aliceMeAvatarOfferWire, false)
	assert.True(t, br != nil && first, "contact re-seeding our own scopeGlobal avatar must be accepted")
	assert.Equal(t, aliceMeAvatarOffer.ID, br.getID())

	// 5. scopeCustom and mismatched fileType/Scope are rejected
	customFile, customFileWire := makeSignedFile(alice, scopeCustom, fileTypeMessageAttachment, groupID)
	br, first = me.handleFile(alice.network.Address(), customFileWire, false)
	assert.True(t, br == nil && !first, "scopeCustom file must be rejected")
	assertFileAbsent(customFile.ID)

	mismatchFile, mismatchFileWire := makeSignedFile(alice, scopeGroupWithInvites, fileTypeMessageAttachment, groupID)
	br, first = me.handleFile(alice.network.Address(), mismatchFileWire, false)
	assert.True(t, br == nil && !first, "message attachment with scopeGroupWithInvites must be rejected")
	assertFileAbsent(mismatchFile.ID)

	customOffer, customOfferWire := makeSignedChunkOffer(alice, scopeCustom, groupID, customFile.ID, customFile.HashList)
	br, first = me.handleChunkOffer(alice.network.Address(), customOfferWire, false)
	assert.True(t, br == nil && !first, "scopeCustom chunkOffer must be rejected")
	assertOfferAbsent(customOffer.ID)
}

func TestCanonicalGroupIconFromRemovedMemberStillAccepted(t *testing.T) {
	me, alice, bob, groupID := createUsersAndGroups(t)

	// Alice sets a valid group icon, then Me removes Alice from the group.
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	assert.NoError(t, png.Encode(&buf, img))
	assert.NoError(t, alice.SetGroupImage(groupID, buf.Bytes()))

	// Locate the icon file, offer, and SetImage update Alice created and wait for Me to process the update
	var iconFile file
	assert.NoError(t, alice.database.Where("type = ? AND destination = ?", fileTypeGroupImage, groupID).First(&iconFile).Error)
	var iconOffer chunkOffer
	assert.NoError(t, alice.database.Where("file_id = ? AND author = ?", iconFile.ID, alice.currentUserID()).First(&iconOffer).Error)
	var setImageUG updateGroup
	assert.NoError(t, alice.database.Where("target = ? AND type = ? AND actor = ?", groupID, updateGroupTypeSetImage, alice.currentUserID()).First(&setImageUG).Error)
	awaitAck(t, alice, me, typeUpdateGroup, setImageUG.ID)

	// Remove Alice with a strictly later timestamp so the SetImage update stays canonical
	aliceID := alice.currentUserID()
	assert.NoError(t, me.applyAndBroadcastUpdateGroup(&updateGroup{
		Actor:     me.currentUserID(),
		Target:    groupID,
		Timestamp: setImageUG.Timestamp + 1,
		Type:      updateGroupTypeRemoveUser,
		Data:      aliceID[:],
	}))
	gs, err := me.currentGroupState(groupID)
	assert.NoError(t, err)
	assert.False(t, gs.isMember(alice.currentUserID()))
	assert.True(t, slices.Contains(gs.images, iconFile.ID))

	// Delete Me's copy of the icon file and offer, then replay Alice's canonical icon and offer:
	// the file frame must still be accepted (it is named in gs.images), while Alice's chunk offer
	// is rejected since she is no longer a member or invitee; a current member's offer is accepted.
	assert.NoError(t, me.database.Where("id = ?", iconOffer.ID).Delete(&chunkOffer{}).Error)
	assert.NoError(t, me.database.Where("id = ?", iconFile.ID).Delete(&file{}).Error)

	br, first := me.handleFile(alice.network.Address(), iconFile.getPayload(), false)
	assert.True(t, br != nil && first, "canonical group icon from removed member must still be accepted")

	br, first = me.handleChunkOffer(alice.network.Address(), iconOffer.getPayload(), false)
	assert.True(t, br == nil && !first, "chunk offer from removed member must be rejected even for a canonical group icon")

	bobOffer := &chunkOffer{
		ID:          NewID(),
		Scope:       scopeGroupWithInvites,
		Destination: groupID,
		Author:      bob.currentUserID(),
		FileID:      iconFile.ID,
		Hash:        iconFile.HashList,
		Location:    bob.network.Address(),
		Timestamp:   time.Now().Unix(),
	}
	bobOffer.OriginalPayload, err = msgpack.Marshal(bobOffer)
	assert.NoError(t, err)
	bobSC := bob.createSignedContainer(typeChunkOffer, bobOffer.OriginalPayload)
	bobOfferWire, err := msgpack.Marshal(bobSC)
	assert.NoError(t, err)
	br, first = me.handleChunkOffer(bob.network.Address(), bobOfferWire, false)
	assert.True(t, br != nil && first, "chunk offer from current member for canonical group icon must be accepted")
}

func TestCrossFileChunkIsolation(t *testing.T) {
	me, alice, bob, groupID := createUsersAndGroups(t)

	// Bob sends a private DM attachment to Me so Me holds its downloaded chunk on disk.
	secretData := []byte("top secret me-bob DM attachment bytes " + NewID().String())
	assert.NoError(t, bob.SendDirectMessage(me.currentUserID(), "private dm", []OutgoingAttachment{{
		Name:   "secret.txt",
		Size:   int64(len(secretData)),
		Reader: io.NopCloser(bytes.NewReader(secretData)),
	}}, NilID))

	var secretDM directMessage
	assert.NoError(t, bob.database.Preload("FileAttachments").First(&secretDM, "text = ?", "private dm").Error)
	assert.Equal(t, 1, len(secretDM.FileAttachments))
	secretFileID := secretDM.FileAttachments[0].FileID
	var secretFile file
	assert.NoError(t, bob.database.First(&secretFile, "id = ?", secretFileID).Error)
	secretHash := secretFile.HashList

	// Wait until Me has downloaded Bob's private DM attachment
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var meSecret file
		if me.database.First(&meSecret, "id = ?", secretFileID).Error == nil && meSecret.Downloaded {
			break
		}
		me.makeAnyChunkRequests()
		time.Sleep(20 * time.Millisecond)
	}
	var meSecret file
	assert.NoError(t, me.database.First(&meSecret, "id = ?", secretFileID).Error)
	assert.True(t, meSecret.Downloaded, "Me must have downloaded Bob's private DM attachment")

	// Alice (not party to Me-Bob's DM) declares a valid DM wrapper file naming secretHash.
	makeWrapper := func(author *Bounce, dest ID, chunkHash string, size int64) (*file, []byte) {
		key := make([]byte, 32)
		_, err := crand.Read(key)
		assert.NoError(t, err)
		f := &file{
			Nonce:       newNonce(),
			Name:        "probe.bin",
			Type:        fileTypeMessageAttachment,
			AttachedTo:  dest,
			Hash:        chunkHash,
			Size:        size,
			HashList:    chunkHash,
			Scope:       scopeUser,
			Destination: dest,
			Author:      author.currentUserID(),
			Timestamp:   time.Now().Unix(),
			Key:         key,
		}
		f.ID = f.generateID()
		f.OriginalPayload, err = msgpack.Marshal(f)
		assert.NoError(t, err)
		sc := author.createSignedContainer(typeFile, f.OriginalPayload)
		wire, err := msgpack.Marshal(sc)
		assert.NoError(t, err)
		return f, wire
	}

	aliceWrapper, aliceWrapperWire := makeWrapper(alice, xor(me.currentUserID(), alice.currentUserID()), secretHash, int64(len(secretData)))
	br, first := me.handleFile(alice.network.Address(), aliceWrapperWire, false)
	assert.True(t, br != nil && first, "wrapper file in Alice-Me DM scope is structurally valid")

	// Save the wrapper file and a planted offer on Alice so any chunk reply from Me would complete it,
	// while a chunkUnavailable reply from Me deletes the planted offer.
	chunkID, err := ParseID(secretHash)
	assert.NoError(t, err)
	aliceLocal := *aliceWrapper
	aliceLocal.Path = alice.platform.ConfigDirectory + "/blobs/" + aliceLocal.ID.String()
	aliceLocal.Wanted = true
	aliceLocal.Downloaded = false
	aliceLocal.Signature = make([]byte, 64)
	aliceLocal.Signer = alice.network.Address()
	aliceLocal.Chunks = []chunk{{ID: xor(aliceLocal.ID, chunkID), FileID: aliceLocal.ID, Hash: secretHash, Index: 0}}
	assert.NoError(t, alice.database.Create(&aliceLocal).Error)

	plantedOffer := &chunkOffer{
		ID:          NewID(),
		Scope:       scopeUser,
		Destination: aliceLocal.Destination,
		Author:      me.currentUserID(),
		FileID:      aliceLocal.ID,
		Hash:        secretHash,
		Location:    me.network.Address(),
		Timestamp:   time.Now().Unix(),
	}
	plantedOffer.OriginalPayload, err = msgpack.Marshal(plantedOffer)
	assert.NoError(t, err)
	plantedOffer.Signature = make([]byte, 64)
	plantedOffer.Signer = me.network.Address()
	assert.NoError(t, alice.database.Create(plantedOffer).Error)

	// Request secretFile directly (unauthorized file ID) followed by aliceWrapper (authorized wrapper, undownloaded).
	// Me must ignore the unauthorized request and reply chunkUnavailable for aliceWrapper, never copying or serving secretData.
	alice.sendDirect(me.network.Address(), &chunkRequest{FileID: secretFileID, Hash: secretHash})
	alice.sendDirect(me.network.Address(), &chunkRequest{FileID: aliceWrapper.ID, Hash: secretHash})

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var count int64
		assert.NoError(t, alice.database.Model(&chunkOffer{}).Where("id = ?", plantedOffer.ID).Count(&count).Error)
		if count == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	var remainingOffers int64
	assert.NoError(t, alice.database.Model(&chunkOffer{}).Where("id = ?", plantedOffer.ID).Count(&remainingOffers).Error)
	assert.Equal(t, int64(0), remainingOffers, "Me must reply chunkUnavailable for Alice's undownloaded wrapper")

	// Alice must not have received secretData, and Me must neither have filled aliceWrapper nor created an offer for it
	var aliceAfter file
	assert.NoError(t, alice.database.First(&aliceAfter, "id = ?", aliceWrapper.ID).Error)
	assert.False(t, aliceAfter.Downloaded, "Alice must not extract bytes from Bob's private DM via wrapper file")

	var meWrapperChunk chunk
	assert.NoError(t, me.database.First(&meWrapperChunk, "file_id = ?", aliceWrapper.ID).Error)
	assert.False(t, meWrapperChunk.Downloaded, "Me must not copy Bob's private chunk into Alice's wrapper file")

	var meOffersForWrapper int64
	assert.NoError(t, me.database.Model(&chunkOffer{}).Where("file_id = ? AND author = ?", aliceWrapper.ID, me.currentUserID()).Count(&meOffersForWrapper).Error)
	assert.Equal(t, int64(0), meOffersForWrapper, "Me must not create a chunk offer revealing possession of secretHash")

	// A chunk frame tagged with secretFileID arriving in handleChunk must not fill aliceWrapper either
	me.handleChunk(bob.network.Address(), (&chunk{FileID: secretFileID, Data: secretData}).getPayload(), false)
	assert.NoError(t, me.database.First(&meWrapperChunk, "file_id = ?", aliceWrapper.ID).Error)
	assert.False(t, meWrapperChunk.Downloaded, "handleChunk for secretFileID must not fill aliceWrapper")

	// Removed group member cannot pull a group attachment either directly or via a DM wrapper
	groupData := []byte("confidential group attachment bytes " + NewID().String())
	assert.NoError(t, me.SendGroupMessage(groupID, "group secret", []OutgoingAttachment{{
		Name:   "group.txt",
		Size:   int64(len(groupData)),
		Reader: io.NopCloser(bytes.NewReader(groupData)),
	}}, NilID))
	var groupGM groupMessage
	assert.NoError(t, me.database.Preload("FileAttachments").First(&groupGM, "text = ?", "group secret").Error)
	groupFileID := groupGM.FileAttachments[0].FileID
	var groupFile file
	assert.NoError(t, me.database.First(&groupFile, "id = ?", groupFileID).Error)
	groupHash := groupFile.HashList

	assert.NoError(t, me.RemoveUserFromGroup(groupID, bob.currentUserID()))
	gs, err := me.currentGroupState(groupID)
	assert.NoError(t, err)
	assert.False(t, gs.isMember(bob.currentUserID()))

	bobWrapper, bobWrapperWire := makeWrapper(bob, xor(me.currentUserID(), bob.currentUserID()), groupHash, int64(len(groupData)))
	br, first = me.handleFile(bob.network.Address(), bobWrapperWire, false)
	assert.True(t, br != nil && first)

	groupChunkID, err := ParseID(groupHash)
	assert.NoError(t, err)
	bobLocal := *bobWrapper
	bobLocal.Path = bob.platform.ConfigDirectory + "/blobs/" + bobLocal.ID.String()
	bobLocal.Wanted = true
	bobLocal.Downloaded = false
	bobLocal.Signature = make([]byte, 64)
	bobLocal.Signer = bob.network.Address()
	bobLocal.Chunks = []chunk{{ID: xor(bobLocal.ID, groupChunkID), FileID: bobLocal.ID, Hash: groupHash, Index: 0}}
	assert.NoError(t, bob.database.Create(&bobLocal).Error)

	bobPlantedOffer := &chunkOffer{
		ID:          NewID(),
		Scope:       scopeUser,
		Destination: bobLocal.Destination,
		Author:      me.currentUserID(),
		FileID:      bobLocal.ID,
		Hash:        groupHash,
		Location:    me.network.Address(),
		Timestamp:   time.Now().Unix(),
	}
	bobPlantedOffer.OriginalPayload, err = msgpack.Marshal(bobPlantedOffer)
	assert.NoError(t, err)
	bobPlantedOffer.Signature = make([]byte, 64)
	bobPlantedOffer.Signer = me.network.Address()
	assert.NoError(t, bob.database.Create(bobPlantedOffer).Error)

	bob.sendDirect(me.network.Address(), &chunkRequest{FileID: groupFileID, Hash: groupHash})
	bob.sendDirect(me.network.Address(), &chunkRequest{FileID: bobWrapper.ID, Hash: groupHash})

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var count int64
		assert.NoError(t, bob.database.Model(&chunkOffer{}).Where("id = ?", bobPlantedOffer.ID).Count(&count).Error)
		if count == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	assert.NoError(t, bob.database.Model(&chunkOffer{}).Where("id = ?", bobPlantedOffer.ID).Count(&remainingOffers).Error)
	assert.Equal(t, int64(0), remainingOffers, "Me must reply chunkUnavailable for removed member's wrapper")

	var bobAfter file
	assert.NoError(t, bob.database.First(&bobAfter, "id = ?", bobWrapper.ID).Error)
	assert.False(t, bobAfter.Downloaded, "removed member must not extract group attachment bytes")
}

func TestMultiChunkAndRepeatedContentFileTransfer(t *testing.T) {
	me, alice, _, _ := createUsersAndGroups(t)

	// 1 MiB + 123 bytes exercises a full fileChunkSize chunk (plus FileID wire prefix) and a trailing partial chunk.
	payload := make([]byte, fileChunkSize+123)
	_, err := crand.Read(payload)
	assert.NoError(t, err)

	sendAndVerify := func(text string) ID {
		assert.NoError(t, me.SendDirectMessage(alice.currentUserID(), text, []OutgoingAttachment{{
			Name:   "multi.bin",
			Size:   int64(len(payload)),
			Reader: io.NopCloser(bytes.NewReader(payload)),
		}}, NilID))

		var dm directMessage
		assert.NoError(t, me.database.Preload("FileAttachments").First(&dm, "text = ?", text).Error)
		assert.Equal(t, 1, len(dm.FileAttachments))
		fileID := dm.FileAttachments[0].FileID

		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			var af file
			if alice.database.First(&af, "id = ?", fileID).Error == nil && af.Downloaded {
				break
			}
			alice.makeAnyChunkRequests()
			time.Sleep(20 * time.Millisecond)
		}
		var af file
		assert.NoError(t, alice.database.First(&af, "id = ?", fileID).Error)
		assert.True(t, af.Downloaded, "Alice must finish downloading multi-chunk file "+text)

		got, err := alice.GetFileData(fileID)
		assert.NoError(t, err)
		assert.Equal(t, payload, got)
		return fileID
	}

	firstID := sendAndVerify("first transfer")
	secondID := sendAndVerify("repeat identical content transfer")
	assert.NotEqual(t, firstID, secondID)
}
