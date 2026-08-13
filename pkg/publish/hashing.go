package publish

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/klauspost/compress/zstd"
)

// zstdMagic is the 4-byte frame magic number (RFC 8878 §3.1.1).
var zstdMagic = []byte{0x28, 0xB5, 0x2F, 0xFD}

// HashHex returns the lowercase hex sha256 digest of data.
func HashHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// HashRef returns the "sha256:<hex>" form used throughout the manifest (spec §2.6).
func HashRef(data []byte) string {
	return "sha256:" + HashHex(data)
}

// IsZstdFrame reports whether data begins with the zstd frame magic number.
func IsZstdFrame(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	for i, b := range zstdMagic {
		if data[i] != b {
			return false
		}
	}
	return true
}

// DecompressZstd fully decodes a zstd frame. Spec §5.3 step 3: "Refuse if
// decompression fails — a corrupt artifact must never be publishable, and
// this is the cheapest possible place to catch it."
func DecompressZstd(data []byte) ([]byte, error) {
	dec, err := zstd.NewReader(nil)
	if err != nil {
		return nil, fmt.Errorf("create zstd decoder: %w", err)
	}
	defer dec.Close()
	out, err := dec.DecodeAll(data, nil)
	if err != nil {
		return nil, fmt.Errorf("decode zstd frame: %w", err)
	}
	return out, nil
}
