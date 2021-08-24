package ui

import (
	"github.com/google/uuid"
)

type user struct {
	id   uuid.UUID
	name string // TODO: bind this, support update functions
}
