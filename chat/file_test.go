package chat

import (
	"bytes"
	"io"
	"testing"

	"github.com/alecthomas/assert/v2"
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
	}}))

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
