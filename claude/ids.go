package claude

import (
	"crypto/rand"
	"encoding/hex"
)

func randID() string {
	if RandID != nil {
		return RandID()
	}
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "local"
	}
	return hex.EncodeToString(b)
}
