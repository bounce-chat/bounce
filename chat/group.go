package chat

import (
	"github.com/google/uuid"
)

type group struct {
	ID        uuid.UUID
	Name      string
	Image     []byte
	Retention int64
	Users     []user
}
