package deobfuscator

import (
	"crypto/rand"
	"encoding/hex"
)

func NewRunID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}
