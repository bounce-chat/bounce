package chat

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/assert/v2"
)

func TestDeletingAnAttachmentCannotDeleteAnotherMessagesFile(t *testing.T) {
	me, _, bob, groupID := createUsersAndGroups(t)
	data := []byte("the original attachment")
	assert.NoError(t, me.SendGroupMessage(groupID, "original", []OutgoingAttachment{{
		Name: "original.txt", Size: int64(len(data)), Reader: io.NopCloser(bytes.NewReader(data)),
	}}, NilID))
	var original groupMessage
	assert.NoError(t, me.database.Preload("FileAttachments").First(&original, "text = ?", "original").Error)
	assert.Equal(t, 1, len(original.FileAttachments))
	fileID := original.FileAttachments[0].FileID
	var target file
	assert.NoError(t, me.database.First(&target, "id = ?", fileID).Error)
	_, err := os.Stat(target.Path)
	assert.NoError(t, err)

	now := time.Now().Unix()
	carrier := groupMessage{
		Nonce: newNonce(), WrittenAt: now, Author: bob.currentUserID(), Destination: groupID,
		Text: "foreign file reference",
		FileAttachments: []fileAttachment{{
			FileID: fileID, Name: "original.txt", Size: int64(len(data)),
		}},
	}
	carrier.ID = carrier.generateID()
	_, accepted := me.handleGroupMessage(firstAddress(bob), signedFrame(t, bob, typeGroupMessage, carrier), false)
	assert.True(t, accepted)

	me.deleteGroupMessageAt(now, carrier.ID)
	assert.NoError(t, me.database.First(&file{}, "id = ?", fileID).Error)
	_, err = os.Stat(target.Path)
	assert.NoError(t, err)
	assert.NoError(t, me.database.First(&groupMessage{}, "id = ?", original.ID).Error)
	var chunks int64
	assert.NoError(t, me.database.Model(&chunk{}).Where("file_id = ?", fileID).Count(&chunks).Error)
	assert.NotEqual(t, int64(0), chunks)
	var carrierAttachments int64
	assert.NoError(t, me.database.Model(&fileAttachment{}).Where("message_id = ?", carrier.ID).Count(&carrierAttachments).Error)
	assert.Equal(t, int64(0), carrierAttachments)
}

func TestDeletingADirectImageAttachmentCannotDeleteAnotherMessagesFile(t *testing.T) {
	me, _, bob, groupID := createUsersAndGroups(t)
	data := []byte("the original image")
	assert.NoError(t, me.SendGroupMessage(groupID, "original image", []OutgoingAttachment{{
		Name: "original.png", Size: int64(len(data)), Reader: io.NopCloser(bytes.NewReader(data)),
		Image: &ImageInfo{Width: 1, Height: 1, BlurHash: "placeholder"},
	}}, NilID))
	var original groupMessage
	assert.NoError(t, me.database.Preload("ImageAttachments").First(&original, "text = ?", "original image").Error)
	assert.Equal(t, 1, len(original.ImageAttachments))
	fileID := original.ImageAttachments[0].FileID
	var target file
	assert.NoError(t, me.database.First(&target, "id = ?", fileID).Error)
	_, err := os.Stat(target.Path)
	assert.NoError(t, err)

	now := time.Now().Unix()
	carrier := directMessage{
		Nonce: newNonce(), WrittenAt: now, Author: bob.currentUserID(),
		Xor: xor(bob.currentUserID(), me.currentUserID()), Text: "foreign image reference",
		ImageAttachments: []imageAttachment{{
			FileID: fileID, Name: "original.png", Size: int64(len(data)), Width: 1, Height: 1, BlurHash: "placeholder",
		}},
	}
	carrier.ID = carrier.generateID()
	_, accepted := me.handleDirectMessage(firstAddress(bob), signedFrame(t, bob, typeDirectMessage, carrier), false)
	assert.True(t, accepted)

	me.deleteDirectMessageAt(now, carrier.ID)
	assert.NoError(t, me.database.First(&file{}, "id = ?", fileID).Error)
	_, err = os.Stat(target.Path)
	assert.NoError(t, err)
	assert.NoError(t, me.database.First(&groupMessage{}, "id = ?", original.ID).Error)
	var carrierAttachments int64
	assert.NoError(t, me.database.Model(&imageAttachment{}).Where("message_id = ?", carrier.ID).Count(&carrierAttachments).Error)
	assert.Equal(t, int64(0), carrierAttachments)
}

func TestMessageHandlersRejectInvalidAttachments(t *testing.T) {
	me, alice, _, groupID := createUsersAndGroups(t)
	invalid := fileAttachment{FileID: NewID(), Name: "invalid.txt", Size: -1}

	dm := directMessage{
		Nonce: newNonce(), WrittenAt: time.Now().Unix(), Author: alice.currentUserID(),
		Xor: xor(alice.currentUserID(), me.currentUserID()), Text: "invalid direct attachment",
		FileAttachments: []fileAttachment{invalid},
	}
	dm.ID = dm.generateID()
	_, accepted := me.handleDirectMessage(firstAddress(alice), signedFrame(t, alice, typeDirectMessage, dm), false)
	assert.False(t, accepted)
	assert.Error(t, me.database.First(&directMessage{}, "id = ?", dm.ID).Error)

	gm := groupMessage{
		Nonce: newNonce(), WrittenAt: time.Now().Unix(), Author: alice.currentUserID(), Destination: groupID,
		Text: "invalid group attachment", FileAttachments: []fileAttachment{invalid},
	}
	gm.ID = gm.generateID()
	_, accepted = me.handleGroupMessage(firstAddress(alice), signedFrame(t, alice, typeGroupMessage, gm), false)
	assert.False(t, accepted)
	assert.Error(t, me.database.First(&groupMessage{}, "id = ?", gm.ID).Error)
}

func TestMessageWritesRejectInvalidAttachments(t *testing.T) {
	me, alice, _, groupID := createUsersAndGroups(t)
	signed := SignedFrame{
		Signer: alice.network.Address(), OriginalPayload: []byte("payload"), Signature: []byte("signature"),
	}

	dm := directMessage{
		SignedFrame: signed, Nonce: newNonce(), WrittenAt: time.Now().Unix(), Author: alice.currentUserID(),
		Xor: xor(alice.currentUserID(), me.currentUserID()), Text: "invalid direct attachment",
		FileAttachments: []fileAttachment{{FileID: NewID(), Name: "invalid.txt", Size: -1}},
	}
	dm.ID = dm.generateID()
	assert.Error(t, me.database.Create(&dm).Error)

	gm := groupMessage{
		SignedFrame: signed, Nonce: newNonce(), WrittenAt: time.Now().Unix(), Author: alice.currentUserID(),
		Destination: groupID, Text: "invalid group attachment",
		ImageAttachments: []imageAttachment{{
			FileID: NewID(), Name: "invalid.png", Size: 1, Width: -1, Height: 1,
		}},
	}
	gm.ID = gm.generateID()
	assert.Error(t, me.database.Create(&gm).Error)
}

func TestValidatingMessageAttachments(t *testing.T) {
	validFile := fileAttachment{FileID: NewID(), Name: strings.Repeat("é", maximumAttachmentNameLength), Size: 0}
	validImage := imageAttachment{
		FileID: NewID(), Name: strings.Repeat("n", maximumAttachmentNameLength), Size: 0,
		Width: 0, Height: maximumAttachmentDimension, BlurHash: strings.Repeat("b", maximumBlurHashLength),
	}
	assert.NoError(t, validMessageAttachments([]imageAttachment{validImage}, []fileAttachment{validFile}))

	nilFile := validFile
	nilFile.FileID = NilID
	negativeFile := validFile
	negativeFile.Size = -1
	longFileName := validFile
	longFileName.Name += "x"
	for _, test := range []struct {
		name       string
		attachment fileAttachment
	}{
		{"nil file ID", nilFile},
		{"negative size", negativeFile},
		{"long name", longFileName},
	} {
		t.Run("file/"+test.name, func(t *testing.T) {
			assert.Error(t, validMessageAttachments(nil, []fileAttachment{test.attachment}))
		})
	}

	nilImage := validImage
	nilImage.FileID = NilID
	negativeImage := validImage
	negativeImage.Size = -1
	longImageName := validImage
	longImageName.Name += "x"
	longBlurHash := validImage
	longBlurHash.BlurHash += "x"
	negativeWidth := validImage
	negativeWidth.Width = -1
	wideImage := validImage
	wideImage.Width = maximumAttachmentDimension + 1
	negativeHeight := validImage
	negativeHeight.Height = -1
	tallImage := validImage
	tallImage.Height = maximumAttachmentDimension + 1
	for _, test := range []struct {
		name       string
		attachment imageAttachment
	}{
		{"nil file ID", nilImage},
		{"negative size", negativeImage},
		{"long name", longImageName},
		{"long blur hash", longBlurHash},
		{"negative width", negativeWidth},
		{"excessive width", wideImage},
		{"negative height", negativeHeight},
		{"excessive height", tallImage},
	} {
		t.Run("image/"+test.name, func(t *testing.T) {
			assert.Error(t, validMessageAttachments([]imageAttachment{test.attachment}, nil))
		})
	}

	files := make([]fileAttachment, MaximumFileAttachments+1)
	for i := range files {
		files[i] = validFile
		files[i].FileID = NewID()
	}
	assert.NoError(t, validMessageAttachments(nil, files[:MaximumFileAttachments]))
	assert.Error(t, validMessageAttachments(nil, files))
	images := make([]imageAttachment, MaximumImageAttachments+1)
	for i := range images {
		images[i] = validImage
		images[i].FileID = NewID()
	}
	assert.NoError(t, validMessageAttachments(images[:MaximumImageAttachments], nil))
	assert.Error(t, validMessageAttachments(images, nil))

	assert.Error(t, validMessageAttachments(nil, []fileAttachment{validFile, validFile}), "duplicate file attachments")
	assert.Error(t, validMessageAttachments([]imageAttachment{validImage, validImage}, nil), "duplicate image attachments")
	sameAsImage := validFile
	sameAsImage.FileID = validImage.FileID
	assert.Error(t, validMessageAttachments([]imageAttachment{validImage}, []fileAttachment{sameAsImage}), "duplicate across attachment kinds")
}

func TestSendingRejectsInvalidAttachments(t *testing.T) {
	me, alice, _, groupID := createUsersAndGroups(t)
	tooMany := make([]OutgoingAttachment, MaximumFileAttachments+1)
	for i := range tooMany {
		tooMany[i] = OutgoingAttachment{
			Name: "file.txt", Size: 1, Reader: io.NopCloser(bytes.NewReader([]byte("x"))),
		}
	}
	assert.Equal(t, errInvalidMessageAttachments, me.SendGroupMessage(groupID, "too many", tooMany, NilID))

	badImage := []OutgoingAttachment{{
		Name: "image.png", Size: 1, Reader: io.NopCloser(bytes.NewReader([]byte("x"))),
		Image: &ImageInfo{Width: maximumAttachmentDimension + 1, Height: 1},
	}}
	assert.Equal(t, errInvalidMessageAttachments, me.SendDirectMessage(alice.currentUserID(), "bad image", badImage, NilID))

	var directMessages int64
	assert.NoError(t, me.database.Model(&directMessage{}).Where("text = ?", "bad image").Count(&directMessages).Error)
	assert.Equal(t, int64(0), directMessages)
	var groupMessages int64
	assert.NoError(t, me.database.Model(&groupMessage{}).Where("text = ?", "too many").Count(&groupMessages).Error)
	assert.Equal(t, int64(0), groupMessages)
	blobs, err := os.ReadDir(me.platform.ConfigDirectory + "/blobs")
	assert.NoError(t, err)
	assert.Equal(t, 0, len(blobs), "rejected messages must not leave prepared blobs behind")
}
