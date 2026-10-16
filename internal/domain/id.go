package domain

import (
	"crypto/rand"
	"encoding/hex"
)

func NewTaskID() TaskID {
	bytes := make([]byte, 16)
	_, _ = rand.Read(bytes)
	return TaskID(hex.EncodeToString(bytes))
}
func NewIdempotencyKey() string {
	bytes := make([]byte, 16)
	_, _ = rand.Read(bytes)
	return hex.EncodeToString(bytes)
}
