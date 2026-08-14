package catalog

import (
	"fmt"
	"regexp"
	"time"
)

// idPattern enforces spec §2.3: [A-Za-z0-9._-], 1-128 chars — safe as a path
// segment and a filename on every platform.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// hashPattern enforces "sha256:<64 lowercase hex>" (spec §2.6).
var hashPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// ValidateManifest checks the envelope and every entry against the schema
// rules in spec §2, plus the cross-entry uniqueness rule for ids/aliases
// (§2.3: an alias MUST NOT collide with another entry's id or alias).
// It does not check filesystem consistency (hash-matches-bytes, orphaned
// artifacts) — that is circuitctl verify's job (spec §5.3), which calls this
// first and then layers filesystem checks on top.
func ValidateManifest(m *Manifest) error {
	if m.ManifestVersion != ManifestVersion {
		return fmt.Errorf("unknown manifestVersion %d (this build understands %d)", m.ManifestVersion, ManifestVersion)
	}
	if m.Catalog != CatalogName {
		return fmt.Errorf("catalog field is %q, expected %q", m.Catalog, CatalogName)
	}
	if _, err := time.Parse(time.RFC3339, m.GeneratedAt); err != nil {
		return fmt.Errorf("generatedAt %q is not RFC3339: %w", m.GeneratedAt, err)
	}

	seen := map[string]string{} // id/alias -> owning entry id, for collision detection
	for i := range m.Circuits {
		e := &m.Circuits[i]
		if err := ValidateEntry(e); err != nil {
			return fmt.Errorf("entry %q: %w", e.ID, err)
		}
		if owner, exists := seen[e.ID]; exists {
			return fmt.Errorf("id %q collides with an id/alias already used by %q", e.ID, owner)
		}
		seen[e.ID] = e.ID
		for _, alias := range e.Aliases {
			if owner, exists := seen[alias]; exists {
				return fmt.Errorf("alias %q of %q collides with an id/alias already used by %q", alias, e.ID, owner)
			}
			seen[alias] = e.ID
		}
	}
	return nil
}

// ValidateEntry checks a single CircuitDescriptor against spec §2.4's field rules.
func ValidateEntry(e *CircuitDescriptor) error {
	if !idPattern.MatchString(e.ID) {
		return fmt.Errorf("id %q does not match %s", e.ID, idPattern.String())
	}
	for _, alias := range e.Aliases {
		if !idPattern.MatchString(alias) {
			return fmt.Errorf("alias %q does not match %s", alias, idPattern.String())
		}
	}
	if e.System == "" {
		return fmt.Errorf("system is required")
	}
	if e.SystemVersion == "" {
		return fmt.Errorf("systemVersion is required")
	}
	switch e.Status {
	case StatusActive, StatusDeprecated, StatusRevoked:
	default:
		return fmt.Errorf("status %q is not one of active|deprecated|revoked", e.Status)
	}
	if e.Status != StatusActive && e.DeprecatedAt == "" {
		return fmt.Errorf("deprecatedAt is required when status is %q", e.Status)
	}
	if e.DeprecatedAt != "" {
		if _, err := time.Parse(time.RFC3339, e.DeprecatedAt); err != nil {
			return fmt.Errorf("deprecatedAt %q is not RFC3339: %w", e.DeprecatedAt, err)
		}
	}
	if _, err := time.Parse(time.RFC3339, e.PublishedAt); err != nil {
		return fmt.Errorf("publishedAt %q is not RFC3339: %w", e.PublishedAt, err)
	}
	if err := validateParams(e.Params); err != nil {
		return fmt.Errorf("params: %w", err)
	}
	if e.Artifact != nil {
		if err := validateArtifact(e.Artifact); err != nil {
			return fmt.Errorf("artifact: %w", err)
		}
	}
	if e.Source != nil {
		if err := validateSource(e.Source); err != nil {
			return fmt.Errorf("source: %w", err)
		}
	}
	return nil
}

// validateParams enforces spec §2.2: scalar values only (string, number, bool) — no nested objects or arrays.
func validateParams(params map[string]any) error {
	for k, v := range params {
		switch v.(type) {
		case string, bool, float64, int, int64:
			// scalar, fine
		default:
			return fmt.Errorf("param %q has non-scalar value of type %T (only string/number/boolean allowed)", k, v)
		}
	}
	return nil
}

func validateArtifact(a *Artifact) error {
	if a.URL == "" {
		return fmt.Errorf("url is required")
	}
	if !hashPattern.MatchString(a.Hash) {
		return fmt.Errorf("hash %q does not match sha256:<64 lowercase hex>", a.Hash)
	}
	if a.Size <= 0 {
		return fmt.Errorf("size must be positive")
	}
	switch a.Compression {
	case CompressionZstd, CompressionNone:
	default:
		return fmt.Errorf("compression %q is not zstd|none", a.Compression)
	}
	if a.MediaType == "" {
		return fmt.Errorf("mediaType is required")
	}
	if a.Uncompressed != nil {
		if !hashPattern.MatchString(a.Uncompressed.Hash) {
			return fmt.Errorf("uncompressed.hash %q does not match sha256:<64 lowercase hex>", a.Uncompressed.Hash)
		}
		if a.Uncompressed.Size <= 0 {
			return fmt.Errorf("uncompressed.size must be positive")
		}
	}
	return nil
}

func validateSource(s *Source) error {
	if s.Origin == "" {
		return fmt.Errorf("origin is required")
	}
	for i, v := range s.VerifiedBy {
		if v.Tool == "" || v.ToolVersion == "" || v.VerifierIdentity == "" {
			return fmt.Errorf("verifiedBy[%d]: tool, toolVersion, and verifierIdentity are all required", i)
		}
		if _, err := time.Parse(time.RFC3339, v.Date); err != nil {
			return fmt.Errorf("verifiedBy[%d].date %q is not RFC3339: %w", i, v.Date, err)
		}
		switch v.Result {
		case ResultAccepted, ResultRejected:
		default:
			return fmt.Errorf("verifiedBy[%d].result %q is not accepted|rejected", i, v.Result)
		}
	}
	return nil
}
