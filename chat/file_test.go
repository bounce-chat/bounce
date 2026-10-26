package chat

import (
	"bytes"
	"io"
	"testing"
	"time"

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
		assert.NoError(t, b.SendGroupMessage(groupID, text, attachments))
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
