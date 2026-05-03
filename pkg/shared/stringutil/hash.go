package stringutil

import (
	"crypto/sha256"
	"encoding/hex"
)

// ShortHash returns a deterministic hex string derived from the SHA-256 of key,
// truncated to n bytes (2*n hex characters). Common values: 6, 8, 12.
func ShortHash(key string, n int) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:n])
}
