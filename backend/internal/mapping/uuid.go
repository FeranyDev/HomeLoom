package mapping

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"time"
)

// NewUUIDv7 returns a canonical, lowercase RFC 9562 UUIDv7. Mapping Profile
// IDs are opaque and immutable; their time ordering is useful for storage and
// diagnostics, but callers must never infer Profile behavior from the ID.
func NewUUIDv7() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate UUIDv7 randomness: %w", err)
	}
	milliseconds := uint64(time.Now().UTC().UnixMilli())
	for index := 5; index >= 0; index-- {
		raw[index] = byte(milliseconds)
		milliseconds >>= 8
	}
	raw[6] = (raw[6] & 0x0f) | 0x70
	raw[8] = (raw[8] & 0x3f) | 0x80
	return formatUUID(raw), nil
}

// BuiltInProfileID gives an immutable built-in template a stable UUIDv7-shaped
// identity. The timestamp portion is a fixed historical instant because built-
// ins are compiled into the program rather than created at runtime; the hash
// prevents their human-readable identifiers from becoming database keys.
func BuiltInProfileID(identifier string) string {
	digest := sha256.Sum256([]byte("homeloom/builtin-profile/" + identifier))
	var raw [16]byte
	copy(raw[:], digest[:])
	// 2024-01-01T00:00:00Z in Unix milliseconds, encoded as UUIDv7's first
	// 48 bits. Keeping it fixed makes all releases resolve legacy bindings to
	// the same immutable ID.
	const builtInEpochMilliseconds = uint64(1_704_067_200_000)
	milliseconds := builtInEpochMilliseconds
	for index := 5; index >= 0; index-- {
		raw[index] = byte(milliseconds)
		milliseconds >>= 8
	}
	raw[6] = (raw[6] & 0x0f) | 0x70
	raw[8] = (raw[8] & 0x3f) | 0x80
	return formatUUID(raw)
}

// IsUUIDv7 accepts only canonical lowercase UUIDv7 text. UUIDs are kept as
// strings across the public API and both supported SQL backends.
func IsUUIDv7(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' || value[14] != '7' {
		return false
	}
	if value[19] != '8' && value[19] != '9' && value[19] != 'a' && value[19] != 'b' {
		return false
	}
	for index := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if !(value[index] >= '0' && value[index] <= '9' || value[index] >= 'a' && value[index] <= 'f') {
			return false
		}
	}
	return true
}

func formatUUID(raw [16]byte) string {
	const hexDigits = "0123456789abcdef"
	var result [36]byte
	position := 0
	for index, value := range raw {
		if index == 4 || index == 6 || index == 8 || index == 10 {
			result[position] = '-'
			position++
		}
		result[position] = hexDigits[value>>4]
		result[position+1] = hexDigits[value&0x0f]
		position += 2
	}
	return string(result[:])
}
