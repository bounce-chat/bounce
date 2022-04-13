package chat

import (
	"sync"

	"github.com/google/uuid"
)

type group struct {
	ID           uuid.UUID
	Name         string
	Image        []byte
	Retention    int64
	Users        []*user `gorm:"many2many:group_users;"`
	payload      []byte
	payloadMutex sync.Mutex
}
