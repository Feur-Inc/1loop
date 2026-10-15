// Package core holds the small types and helpers every part of 1loop
// shares.
package core

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

func Identifier() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// ImagePart is a picture attached to a message (clipboard paste, file…),
// stored as base64 so a conversation stays a single self-contained record.
type ImagePart struct {
	Mime string `json:"mime"`
	Data string `json:"data"` // base64, no data: prefix
}

// TruncateText cuts long strings, keeping both ends: the start holds the
// interesting part, the tail often holds the final line or error.
func TruncateText(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	head, tail := limit*2/3, limit/3
	return string(runes[:head]) + " …[" + fmt.Sprint(len(runes)-limit) + " chars skipped]… " + string(runes[len(runes)-tail:])
}
