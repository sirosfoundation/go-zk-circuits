package catalog

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func sampleArtifact() *Artifact {
	return &Artifact{
		URL:         "/v1/artifacts/sha256/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Hash:        "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Size:        1024,
		Compression: CompressionZstd,
		MediaType:   "application/zstd",
	}
}

func TestValidateArtifact_RequiresURL(t *testing.T) {
	a := sampleArtifact()
	a.URL = ""
	require.ErrorContains(t, validateArtifact(a), "url is required")
}

func TestValidateArtifact_RequiresPositiveSize(t *testing.T) {
	a := sampleArtifact()
	a.Size = 0
	require.ErrorContains(t, validateArtifact(a), "size must be positive")
}

func TestValidateArtifact_RejectsUnknownCompression(t *testing.T) {
	a := sampleArtifact()
	a.Compression = "gzip"
	require.ErrorContains(t, validateArtifact(a), "compression")
}

func TestValidateArtifact_RequiresMediaType(t *testing.T) {
	a := sampleArtifact()
	a.MediaType = ""
	require.ErrorContains(t, validateArtifact(a), "mediaType is required")
}

func TestValidateArtifact_ValidatesUncompressedHashAndSize(t *testing.T) {
	a := sampleArtifact()
	a.Uncompressed = &Uncompressed{Hash: "sha256:not-hex", Size: 10}
	require.ErrorContains(t, validateArtifact(a), "uncompressed.hash")

	a.Uncompressed.Hash = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	a.Uncompressed.Size = 0
	require.ErrorContains(t, validateArtifact(a), "uncompressed.size")

	a.Uncompressed.Size = 2048
	require.NoError(t, validateArtifact(a))
}

func TestValidateArtifact_AcceptsCompressionNone(t *testing.T) {
	a := sampleArtifact()
	a.Compression = CompressionNone
	require.NoError(t, validateArtifact(a))
}

func TestValidateSource_RequiresOrigin(t *testing.T) {
	s := &Source{}
	require.ErrorContains(t, validateSource(s), "origin is required")
}

func TestValidateSource_VerifiedByRequiresAllIdentityFields(t *testing.T) {
	s := &Source{Origin: "o", VerifiedBy: []VerificationRecord{
		{Tool: "", ToolVersion: "1.0", VerifierIdentity: "v", Date: "2026-08-13T21:40:11Z", Result: ResultAccepted},
	}}
	require.ErrorContains(t, validateSource(s), "tool, toolVersion, and verifierIdentity")
}

func TestValidateSource_VerifiedByRequiresRFC3339Date(t *testing.T) {
	s := &Source{Origin: "o", VerifiedBy: []VerificationRecord{
		{Tool: "t", ToolVersion: "1.0", VerifierIdentity: "v", Date: "not-a-date", Result: ResultAccepted},
	}}
	require.ErrorContains(t, validateSource(s), "is not RFC3339")
}

func TestValidateSource_AcceptsValidVerifiedByAndRejected(t *testing.T) {
	s := &Source{Origin: "o", VerifiedBy: []VerificationRecord{
		{Tool: "t", ToolVersion: "1.0", VerifierIdentity: "v", Date: "2026-08-13T21:40:11Z", Result: ResultRejected},
	}}
	require.NoError(t, validateSource(s))
}
