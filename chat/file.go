package chat

import (
	"errors"

	"github.com/google/uuid"
)

const embeddedFileLimit = 1024 * 1024 * 10 // 10MiB

var ErrFileTooBig = errors.New("file is too large")

type file struct {
	ID   uuid.UUID `gorm:"type:uuid;primary_key;"`
	Size int
	Data []byte `gorm:"type:blob"`
}

func (b *bounce) storeFile(data []byte) (uuid.UUID, error) {
	if len(data) > embeddedFileLimit {
		return uuid.Nil, ErrFileTooBig
	}

	fileID := uuid.New()

	err := b.database.Create(&file{
		ID:   fileID,
		Size: len(data),
		Data: data,
	}).Error
	if err != nil {
		return uuid.Nil, err
	}

	return fileID, nil
}

func validImage(data []byte) bool {
	// TODO
	return true
}
