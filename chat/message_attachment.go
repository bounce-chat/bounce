package chat

import (
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type fileAttachment struct {
	ID        uuid.UUID
	FileID    uuid.UUID
	MessageID uuid.UUID
	Name      string
	Size      int64
}

func (fa *fileAttachment) AfterDelete(tx *gorm.DB) error {
	if fa.ID == uuid.Nil {
		return nil
	}
	return tx.Clauses(clause.Returning{}).Where("id = ?", fa.FileID).Delete(&file{}).Error
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
	if ia.ID == uuid.Nil {
		return nil
	}
	return tx.Clauses(clause.Returning{}).Where("id = ?", ia.FileID).Delete(&file{}).Error
}
