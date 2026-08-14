package publish

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sirosfoundation/go-zk-circuits/pkg/catalog"
)

// DefaultMaxArtifactBytes guards against accidentally committing something
// much larger than a circuit (spec §5.3 step 1): "a guard against someone
// accidentally committing a whole test-vector directory."
const DefaultMaxArtifactBytes = 8 * 1024 * 1024

// AddOptions mirrors circuitctl add's flags (spec §5.3).
type AddOptions struct {
	InputFile      string
	System         string
	ID             string // optional; derived if empty (longfellow only, for now)
	Aliases        []string
	DocTypes       []string
	Origin         string
	Toolchain      string
	License        string
	OpenSource     bool              // spec §2.8.1: default false — requires an affirmative claim, not inferred from License
	ExplicitParams map[string]string // from repeated --param key=value
	MaxBytes       int64             // 0 = DefaultMaxArtifactBytes
	Now            string            // RFC3339 publishedAt; empty = time.Now().UTC()
	Notes          string
	Unpublished    bool // spec §2.4.1: default is published=true; set this to keep the entry out of the served manifest
}

// AddResult reports what Add did, for the CLI to print and for tests to assert on.
type AddResult struct {
	Entry        catalog.CircuitDescriptor
	ArtifactPath string
	EntryPath    string
}

// Add implements circuitctl add (spec §5.3): read, hash, cross-check,
// derive id, write artifact + entry file. It does NOT regenerate the
// manifest — call RegenerateManifest(root) afterward, same as the real CLI does.
func Add(root string, opts AddOptions) (*AddResult, error) {
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxArtifactBytes
	}

	data, err := os.ReadFile(opts.InputFile)
	if err != nil {
		return nil, fmt.Errorf("read input file: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("input file is %d bytes, exceeds max %d bytes", len(data), maxBytes)
	}
	if opts.System == "" {
		return nil, fmt.Errorf("--system is required")
	}
	if opts.Origin == "" {
		return nil, fmt.Errorf("--origin is required (spec §5.4 gate 2)")
	}

	entry := catalog.CircuitDescriptor{
		System:    opts.System,
		Aliases:   opts.Aliases,
		DocTypes:  opts.DocTypes,
		Published: !opts.Unpublished,
		Status:    catalog.StatusActive,
		Notes:     opts.Notes,
		Source: &catalog.Source{
			Origin:     opts.Origin,
			Toolchain:  opts.Toolchain,
			License:    opts.License,
			OpenSource: opts.OpenSource,
		},
	}

	artifactHash := HashRef(data)
	entry.Artifact = &catalog.Artifact{
		Hash: artifactHash,
		Size: int64(len(data)),
	}

	if IsZstdFrame(data) {
		entry.Artifact.Compression = catalog.CompressionZstd
		entry.Artifact.MediaType = "application/zstd"
		uncompressed, err := DecompressZstd(data)
		if err != nil {
			return nil, fmt.Errorf("input claims to be zstd but failed to decompress — refusing to publish a possibly-corrupt artifact: %w", err)
		}
		entry.Artifact.Uncompressed = &catalog.Uncompressed{
			Hash: HashRef(uncompressed),
			Size: int64(len(uncompressed)),
		}
	} else {
		entry.Artifact.Compression = catalog.CompressionNone
		entry.Artifact.MediaType = "application/octet-stream"
	}

	// System-specific parsing/cross-check (spec §5.3 step 4). Longfellow is
	// the only known system with a self-describing filename today (spec
	// §2.7); other systems pass through with only explicit --param values.
	params := map[string]any{}
	var defaultID string
	if opts.System == "longfellow" {
		lf, err := ParseLongfellowFilename(filepath.Base(opts.InputFile))
		if err != nil {
			return nil, err
		}
		for k, v := range opts.ExplicitParams {
			if err := lf.CrossCheckParam(k, v); err != nil {
				return nil, err
			}
		}
		params = lf.ToParams()
		defaultID = lf.DefaultID()
		entry.SystemVersion = fmt.Sprintf("%d", lf.Version)
	}
	for k, v := range opts.ExplicitParams {
		if _, alreadySet := params[k]; !alreadySet {
			params[k] = v // unknown-system params pass through as strings; publisher's responsibility (spec §2.1 rationale #4)
		}
	}
	entry.Params = params

	id := opts.ID
	if id == "" {
		if defaultID == "" {
			return nil, fmt.Errorf("--id is required for system %q (no filename convention to derive it from)", opts.System)
		}
		id = defaultID
	}
	entry.ID = id
	entry.Artifact.URL = "/v1/" + catalog.ArtifactFilePath(HashHex(data))

	publishedAt := opts.Now
	if publishedAt == "" {
		publishedAt = time.Now().UTC().Format(time.RFC3339)
	}
	entry.PublishedAt = publishedAt

	if err := checkNoCollision(root, entry.ID, entry.Aliases); err != nil {
		return nil, err
	}
	if err := catalog.ValidateEntry(&entry); err != nil {
		return nil, fmt.Errorf("generated entry failed validation: %w", err)
	}

	artifactPath := filepath.Join(root, catalog.ArtifactFilePath(HashHex(data)))
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o750); err != nil { //#nosec G703 -- HashHex(data) is our own hex.EncodeToString(sha256.Sum256(...)) output, never remote input
		return nil, fmt.Errorf("mkdir for artifact: %w", err)
	}
	if err := os.WriteFile(artifactPath, data, 0o600); err != nil { //#nosec G703 -- same artifactPath as above
		return nil, fmt.Errorf("write artifact: %w", err)
	}

	entryPath := filepath.Join(root, catalog.EntryFilePath(entry.ID))
	if err := writeEntryFile(entryPath, &entry); err != nil {
		return nil, err
	}

	return &AddResult{Entry: entry, ArtifactPath: artifactPath, EntryPath: entryPath}, nil
}

// checkNoCollision rejects a collision with any existing id or alias (spec §5.3 step 5).
func checkNoCollision(root, id string, aliases []string) error {
	existing, err := LoadCatalogEntries(root)
	if err != nil {
		return err
	}
	for _, e := range existing {
		if e.ID == id {
			return fmt.Errorf("id %q already exists", id)
		}
		for _, a := range e.Aliases {
			if a == id {
				return fmt.Errorf("id %q collides with an existing alias of %q", id, e.ID)
			}
		}
		for _, a := range aliases {
			if a == e.ID {
				return fmt.Errorf("alias %q collides with existing id %q", a, e.ID)
			}
			for _, ea := range e.Aliases {
				if a == ea {
					return fmt.Errorf("alias %q collides with an existing alias of %q", a, e.ID)
				}
			}
		}
	}
	return nil
}
