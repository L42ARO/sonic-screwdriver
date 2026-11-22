package utils

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// NewID returns a short random id with a prefix, e.g. "job_3f9a1c2b7d4e".
func NewID(prefix string) string {
	b := make([]byte, 6)
	rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

// Now is the timestamp format stored in the database.
func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
