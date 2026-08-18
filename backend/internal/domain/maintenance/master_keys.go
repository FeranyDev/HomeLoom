// Package maintenance holds durable maintenance contracts that are shared by
// the application service and persistence adapters.
package maintenance

// MasterKeyStatus intentionally reports only version and ciphertext counts.
// It never returns a key, encrypted payload, secret scope, or plaintext.
type MasterKeyStatus struct {
	ActiveVersion        uint32            `json:"activeVersion"`
	RetainedVersions     []uint32          `json:"retainedVersions"`
	CiphertextsByVersion map[uint32]uint64 `json:"ciphertextsByVersion"`
	LegacyCiphertexts    uint64            `json:"legacyCiphertexts"`
	NeedsReencryption    bool              `json:"needsReencryption"`
}

// MasterKeyRotation is the safe result of either starting a new rotation or
// resuming an interrupted batch. Retained old keys are required to decrypt old
// backups and are never exposed through this API.
type MasterKeyRotation struct {
	PreviousVersion uint32          `json:"previousVersion"`
	ActiveVersion   uint32          `json:"activeVersion"`
	Reencrypted     uint64          `json:"reencrypted"`
	Status          MasterKeyStatus `json:"status"`
}
