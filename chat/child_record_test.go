package chat

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/alecthomas/assert/v2"
)

func TestIntroductionSignatureCannotBeReparented(t *testing.T) {
	me, _, bob, _ := createUsersAndGroups(t)

	victim := introducedByBounce(me.currentUserID(), me, bnc01NewOnion(), time.Now().Unix())
	assert.NoError(t, me.database.Create(&victim).Error)
	var original introductionSignature
	assert.NoError(t, me.database.First(&original, "device_id = ?", victim.ID).Error)
	assert.Equal(t, victim.ID, original.DeviceID)

	attacker := introducedByBounce(bob.currentUserID(), bob, bnc01NewOnion(), time.Now().Unix())
	attacker.Signature.ID = original.ID
	attacker.Signature.DeviceID = original.DeviceID
	payload, err := msgpack.Marshal(map[string]interface{}{
		"ID":        attacker.ID,
		"Nonce":     attacker.Nonce,
		"UserID":    attacker.UserID,
		"Address":   attacker.Address,
		"Network":   attacker.Network,
		"Timestamp": attacker.Timestamp,
		"RevokedAt": attacker.RevokedAt,
		"Signature": map[string]interface{}{
			"ID":                           attacker.Signature.ID,
			"DeviceID":                     attacker.Signature.DeviceID,
			"PreexistingDevice":            attacker.Signature.PreexistingDevice,
			"SignatureOfNewDevice":         attacker.Signature.SignatureOfNewDevice,
			"SignatureOfPreexistingDevice": attacker.Signature.SignatureOfPreexistingDevice,
		},
	})
	assert.NoError(t, err)
	_, accepted := me.handleDevice(firstAddress(bob), payload, false)
	assert.True(t, accepted)

	var preserved introductionSignature
	assert.NoError(t, me.database.First(&preserved, "id = ?", original.ID).Error)
	assert.Equal(t, victim.ID, preserved.DeviceID, "a device frame must not move another device's introduction")
	var storedAttacker introductionSignature
	assert.NoError(t, me.database.First(&storedAttacker, "device_id = ?", attacker.ID).Error)
	assert.Equal(t, attacker.ID, storedAttacker.ID)
	assert.Equal(t, attacker.ID, storedAttacker.DeviceID)

	current, ok := me.currentUser()
	assert.True(t, ok)
	assert.True(t, me.hasValidDeviceGroup(current))

	wire, err := msgpack.Marshal(attacker)
	assert.NoError(t, err)
	var decoded device
	assert.NoError(t, msgpack.Unmarshal(wire, &decoded))
	assert.NotZero(t, decoded.Signature)
	assert.Equal(t, NilID, decoded.Signature.ID)
	assert.Equal(t, NilID, decoded.Signature.DeviceID)
	assert.True(t, idMatchesContent(&decoded))
}

func TestMessageAttachmentRowsCannotBeReparented(t *testing.T) {
	me, _, bob, groupID := createUsersAndGroups(t)

	image := []byte("image attachment")
	file := []byte("file attachment")
	assert.NoError(t, me.SendGroupMessage(groupID, "original attachments", []OutgoingAttachment{
		{
			Name: "image.png", Size: int64(len(image)), Reader: io.NopCloser(bytes.NewReader(image)),
			Image: &ImageInfo{Width: 1, Height: 1, BlurHash: "placeholder"},
		},
		{Name: "file.txt", Size: int64(len(file)), Reader: io.NopCloser(bytes.NewReader(file))},
	}, NilID))
	var original groupMessage
	assert.NoError(t, me.database.Preload("FileAttachments").Preload("ImageAttachments").First(&original, "text = ?", "original attachments").Error)
	assert.Equal(t, 1, len(original.FileAttachments))
	assert.Equal(t, 1, len(original.ImageAttachments))
	originalFile := original.FileAttachments[0]
	originalImage := original.ImageAttachments[0]

	now := time.Now().Unix()
	attacker := directMessage{
		Nonce:     newNonce(),
		WrittenAt: now,
		Author:    bob.currentUserID(),
		Xor:       xor(bob.currentUserID(), me.currentUserID()),
		Text:      "hostile child keys",
		FileAttachments: []fileAttachment{{
			ID: originalFile.ID, FileID: NewID(), MessageID: original.ID, Name: "other.txt", Size: 1,
		}},
		ImageAttachments: []imageAttachment{{
			ID: originalImage.ID, FileID: NewID(), MessageID: original.ID, Name: "other.png", Size: 1,
			Width: 1, Height: 1, BlurHash: "placeholder",
		}},
	}
	attacker.ID = attacker.generateID()
	var err error
	attacker.OriginalPayload, err = msgpack.Marshal(map[string]interface{}{
		"ID":        attacker.ID,
		"Nonce":     attacker.Nonce,
		"WrittenAt": attacker.WrittenAt,
		"DeleteAt":  attacker.DeleteAt,
		"Author":    attacker.Author,
		"Xor":       attacker.Xor,
		"Text":      attacker.Text,
		"FileAttachments": []map[string]interface{}{{
			"ID": attacker.FileAttachments[0].ID, "FileID": attacker.FileAttachments[0].FileID,
			"MessageID": attacker.FileAttachments[0].MessageID, "Name": attacker.FileAttachments[0].Name,
			"Size": attacker.FileAttachments[0].Size,
		}},
		"ImageAttachments": []map[string]interface{}{{
			"ID": attacker.ImageAttachments[0].ID, "FileID": attacker.ImageAttachments[0].FileID,
			"MessageID": attacker.ImageAttachments[0].MessageID, "Name": attacker.ImageAttachments[0].Name,
			"Size": attacker.ImageAttachments[0].Size, "Width": attacker.ImageAttachments[0].Width,
			"Height": attacker.ImageAttachments[0].Height, "BlurHash": attacker.ImageAttachments[0].BlurHash,
		}},
	})
	assert.NoError(t, err)
	container := bob.createSignedContainer(typeDirectMessage, attacker.OriginalPayload)
	attacker.Signature = container.Signature
	attacker.Signer = container.Signer
	_, accepted := me.handleDirectMessage(firstAddress(bob), attacker.getPayload(), false)
	assert.True(t, accepted)

	var preservedFile fileAttachment
	assert.NoError(t, me.database.First(&preservedFile, "id = ?", originalFile.ID).Error)
	assert.Equal(t, original.ID, preservedFile.MessageID)
	assert.Equal(t, originalFile.FileID, preservedFile.FileID)
	var preservedImage imageAttachment
	assert.NoError(t, me.database.First(&preservedImage, "id = ?", originalImage.ID).Error)
	assert.Equal(t, original.ID, preservedImage.MessageID)
	assert.Equal(t, originalImage.FileID, preservedImage.FileID)

	var receivedFiles []fileAttachment
	assert.NoError(t, me.database.Find(&receivedFiles, "message_id = ?", attacker.ID).Error)
	assert.Equal(t, 1, len(receivedFiles))
	assert.NotEqual(t, NilID, receivedFiles[0].ID)
	assert.NotEqual(t, originalFile.ID, receivedFiles[0].ID)
	var receivedImages []imageAttachment
	assert.NoError(t, me.database.Find(&receivedImages, "message_id = ?", attacker.ID).Error)
	assert.Equal(t, 1, len(receivedImages))
	assert.NotEqual(t, NilID, receivedImages[0].ID)
	assert.NotEqual(t, originalImage.ID, receivedImages[0].ID)

	wire, err := msgpack.Marshal(attacker)
	assert.NoError(t, err)
	for _, payload := range [][]byte{wire, attacker.OriginalPayload} {
		var decoded directMessage
		assert.NoError(t, msgpack.Unmarshal(payload, &decoded))
		assert.Equal(t, NilID, decoded.FileAttachments[0].ID)
		assert.Equal(t, NilID, decoded.FileAttachments[0].MessageID)
		assert.Equal(t, NilID, decoded.ImageAttachments[0].ID)
		assert.Equal(t, NilID, decoded.ImageAttachments[0].MessageID)
		assert.True(t, idMatchesContent(&decoded))
	}
}

func TestProfileSettingsIDsAreAssignedLocally(t *testing.T) {
	me, _, _, _ := createUsersAndGroups(t)
	var settings profileSettings
	assert.NoError(t, me.database.First(&settings, "user_id = ?", me.currentUserID()).Error)
	assert.NotEqual(t, NilID, settings.ID)
	assert.Equal(t, me.currentUserID(), settings.UserID)

	payload, err := msgpack.Marshal(syncDeviceRequestAccepted{Settings: &settings})
	assert.NoError(t, err)
	var decoded syncDeviceRequestAccepted
	assert.NoError(t, msgpack.Unmarshal(payload, &decoded))
	assert.NotZero(t, decoded.Settings)
	assert.Equal(t, NilID, decoded.Settings.ID)
	assert.Equal(t, NilID, decoded.Settings.UserID)
}
