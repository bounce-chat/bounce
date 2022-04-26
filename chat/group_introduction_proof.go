package chat

import "github.com/google/uuid"

type groupIntroductionProof struct {
	ID         uuid.UUID `gorm:"type:uuid;primary_key;"`
	Group      uuid.UUID
	User       []byte
	Introducer uuid.UUID
	Signature  []byte
}
