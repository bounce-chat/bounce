package chat

import (
	"bytes"
	"io"
	"testing"

	"github.com/alecthomas/assert/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestDeletingAMessageDeletesAllFileData(t *testing.T) {
	b, _, _, groupID := createUsersAndGroups(t)

	fileID := uuid.New()
	data := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09}
	text := "this message contains an embedded file"
	gm := GroupMessage{
		Thread: groupID,
		Text:   text,
	}
	gm.ImageAttachments = []ImageAttachment{}
	gm.FileAttachments = []FileAttachment{
		FileAttachment{
			ID:   fileID,
			Name: "filename",
			Size: int64(len(data)),
		},
	}

	readers := map[uuid.UUID]io.ReadCloser{
		fileID: io.NopCloser(bytes.NewReader(data)),
	}
	sources := map[uuid.UUID]string{}

	b.SendGroupMessage(gm, readers, sources)

	// Make sure that all the database objects were created
	var createdGM groupMessage
	err := b.database.First(&createdGM, "text = ?", text).Error
	assert.NoError(t, err)

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
