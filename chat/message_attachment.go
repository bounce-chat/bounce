package chat

import (
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type fileAttachment struct {
	ID        uuid.UUID
	FileID    uuid.UUID
	MessageID uuid.UUID
	Name      string
	Size      int64
}

func (fa *fileAttachment) AfterDelete(tx *gorm.DB) error {
	return tx.Where("id = ?", fa.FileID).Delete(&file{}).Error
}

type imageAttachment struct {
	ID        uuid.UUID
	FileID    uuid.UUID
	MessageID uuid.UUID
	Name      string
	Size      int64
	Width     int
	Height    int
	BlurHash  string
}

func (ia *imageAttachment) AfterDelete(tx *gorm.DB) error {
	return tx.Where("id = ?", ia.FileID).Delete(&file{}).Error
}
