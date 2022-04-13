package chat

import (
	"github.com/google/uuid"
)

type updateGroup struct {
	ID          uuid.UUID
	Name        string
	DeleteUsers string
	AddUsers    []user
}
