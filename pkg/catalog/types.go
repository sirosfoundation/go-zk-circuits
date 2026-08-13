// Package catalog defines the wire schema for the zk-circuits manifest
// (circuit-distribution-service-spec.md §2) and the logic to load, validate,
// and deterministically regenerate it.
package catalog

// ManifestVersion is the only schema version this build understands.
// Bumping it is a breaking change and moves in lockstep with the /v1/ URL prefix (spec §2.10, §3.2).
const ManifestVersion = 1

// CatalogName is the constant identifying this catalog, so a misconfigured
// base URL pointing at an unrelated JSON document fails loudly (spec §2.10).
const CatalogName = "siros-zk-circuits"

// Status values for a CircuitDescriptor (spec §2.4, §7.4).
const (
	StatusActive     = "active"
	StatusDeprecated = "deprecated"
	StatusRevoked    = "revoked"
)

// Compression values for an Artifact (spec §2.6).
const (
	CompressionZstd = "zstd"
	CompressionNone = "none"
)

// Result values for a VerificationRecord (spec §2.8, §5.7).
const (
	ResultAccepted = "accepted"
	ResultRejected = "rejected"
)

// Manifest is the top-level document served at /v1/manifest.json (spec §2.10).
//
// Field order matters: it is emitted exactly in struct-declaration order by
// encoding/json, which is what makes manifest generation byte-reproducible
// (spec §5.5) together with sorting Circuits by ID before marshaling.
type Manifest struct {
	ManifestVersion int                 `json:"manifestVersion"`
	GeneratedAt     string              `json:"generatedAt"`
	Catalog         string              `json:"catalog"`
	Circuits        []CircuitDescriptor `json:"circuits"`
	Next            *string             `json:"next"`
}

// CircuitDescriptor is one catalog entry (spec §2.4).
//
// Published (spec §2.4.1) is a build-time visibility gate, distinct from
// PublishedAt (when the entry was added to the repo) and from Status (its
// lifecycle once visible). An entry with Published=false stays in the repo
// — hash-verified by circuitctl verify, fully tooled — but is excluded from
// the generated manifest.json entirely, so it is never visible to a client:
// not in /v1/manifest.json, not at /v1/circuits/{id}.json, and (per
// ArtifactHandler's check) not downloadable at /v1/artifacts/sha256/{hex}
// either, even though its bytes are go:embed'd into the binary.
type CircuitDescriptor struct {
	ID            string         `json:"id"`
	Aliases       []string       `json:"aliases,omitempty"`
	System        string         `json:"system"`
	SystemVersion string         `json:"systemVersion"`
	DocTypes      []string       `json:"docTypes,omitempty"`
	Published     bool           `json:"published"`
	Status        string         `json:"status"`
	Params        map[string]any `json:"params"`
	Artifact      *Artifact      `json:"artifact,omitempty"`
	Source        *Source        `json:"source,omitempty"`
	PublishedAt   string         `json:"publishedAt"`
	DeprecatedAt  string         `json:"deprecatedAt,omitempty"`
	Notes         string         `json:"notes,omitempty"`
}

// Artifact describes the downloadable bytes for a circuit (spec §2.6).
// Note the two-hashes trap documented there: Hash is over the bytes AS SERVED
// (compressed, if Compression != "none"); Uncompressed.Hash is over the
// decompressed bytes. Neither is params["circuit_hash"], which is the proof
// system's own identifier and is never assumed to equal either of these.
type Artifact struct {
	URL          string        `json:"url"`
	Hash         string        `json:"hash"`
	Size         int64         `json:"size"`
	Compression  string        `json:"compression"`
	MediaType    string        `json:"mediaType"`
	Uncompressed *Uncompressed `json:"uncompressed,omitempty"`
}

// Uncompressed is the decompressed-form hash/size, present when Compression != "none" (spec §2.6).
type Uncompressed struct {
	Hash string `json:"hash"`
	Size int64  `json:"size"`
}

// Source records provenance for a catalog entry (spec §2.8, §2.8.1).
//
// Toolchain and OpenSource exist so a human reviewer on the website (spec
// §1.6) can answer "what built this" and "can I use it" without leaving
// the page or knowing SPDX/OSI mappings. OpenSource is deliberately NOT
// derived from License — it defaults to false (fail-closed, same
// rationale as CircuitDescriptor.Published) and requires an affirmative
// claim from whoever ran `circuitctl add --open-source`.
type Source struct {
	Origin     string               `json:"origin"`
	OriginRef  string               `json:"originRef,omitempty"`
	OriginPath string               `json:"originPath,omitempty"`
	Toolchain  string               `json:"toolchain,omitempty"`
	License    string               `json:"license,omitempty"`
	OpenSource bool                 `json:"openSource"`
	AddedBy    string               `json:"addedBy"`
	VerifiedBy []VerificationRecord `json:"verifiedBy,omitempty"`
}

// VerificationRecord is a single structured, machine-readable interop
// confirmation (spec §2.8, §5.7). It is written exclusively by
// `circuitctl verify-interop`, never hand-edited, and carries no trust
// authority on its own — it is descriptive metadata for humans and
// dashboards, not a security control. Which circuit a client actually uses
// is still governed entirely by that client's compile-time pin table
// (spec §7.3).
type VerificationRecord struct {
	Tool             string `json:"tool"`
	ToolVersion      string `json:"toolVersion"`
	VerifierIdentity string `json:"verifierIdentity"`
	Date             string `json:"date"`
	Result           string `json:"result"`
	Notes            string `json:"notes,omitempty"`
}
