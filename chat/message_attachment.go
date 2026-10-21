package chat

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type fileAttachment struct {
	ID        ID
	FileID    ID
	MessageID ID
	Name      string
	Size      int64
}

func (fa *fileAttachment) AfterDelete(tx *gorm.DB) error {
	if fa.ID == NilID {
		return nil
	}
	return tx.Clauses(clause.Returning{}).Where("id = ?", fa.FileID).Delete(&file{}).Error
}

type imageAttachment struct {
	ID        ID
	FileID    ID
	MessageID ID
	Name      string
	Size      int64
	Width     int
	Height    int
	BlurHash  string
}

func (ia *imageAttachment) AfterDelete(tx *gorm.DB) error {
	if ia.ID == NilID {
		return nil
	}
	return tx.Clauses(clause.Returning{}).Where("id = ?", ia.FileID).Delete(&file{}).Error
}
