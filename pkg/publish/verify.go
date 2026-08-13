package publish

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/sirosfoundation/go-zk-circuits/pkg/catalog"
)

// hexDigestPattern enforces a pure lowercase-hex charset on the digest
// extracted from a "sha256:<hex>" ref before it is ever joined into a
// filesystem path. catalog.ValidateEntry already enforces this via the same
// pattern earlier in Verify's loop, so in practice this can't fail — but
// hexFromHashRef re-checks it directly rather than relying on that call
// order, since the only thing standing between this value and a
// path-traversal string would otherwise be "some earlier validation ran".
var hexDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// VerifyReport is what `circuitctl verify` found — spec §5.3's CI gate.
type VerifyReport struct {
	EntriesChecked   int
	ArtifactsChecked int
	Problems         []string
}

func (r *VerifyReport) fail(format string, args ...any) {
	r.Problems = append(r.Problems, fmt.Sprintf(format, args...))
}

// OK reports whether verification found nothing wrong.
func (r *VerifyReport) OK() bool { return len(r.Problems) == 0 }

// Verify implements circuitctl verify (spec §5.3): recompute every
// artifact's hash from disk, re-validate every entry against the schema,
// confirm every artifact.url resolves to a file present in the tree,
// confirm no orphan artifacts, confirm manifest.json is byte-identical to a
// fresh regeneration. This is the CI gate that makes hand-editing the
// manifest or swapping bytes under an existing id impossible to merge.
func Verify(root string) (*VerifyReport, error) {
	report := &VerifyReport{}

	entries, err := LoadCatalogEntries(root)
	if err != nil {
		return nil, fmt.Errorf("load catalog entries: %w", err)
	}
	report.EntriesChecked = len(entries)

	referenced := map[string]bool{} // artifact filename (hex digest) -> referenced by some entry

	for i := range entries {
		e := &entries[i]
		if err := catalog.ValidateEntry(e); err != nil {
			report.fail("entry %q: schema validation failed: %v", e.ID, err)
			continue
		}
		if e.Artifact == nil {
			continue // metadata-only entry (spec §2.9) — nothing to check on disk
		}
		hexDigest, err := hexFromHashRef(e.Artifact.Hash)
		if err != nil {
			report.fail("entry %q: %v", e.ID, err)
			continue
		}
		referenced[hexDigest] = true

		artifactPath := filepath.Join(root, catalog.ArtifactFilePath(hexDigest))
		data, err := os.ReadFile(artifactPath) //#nosec G304 -- hexDigest is validated as pure lowercase-hex by hexFromHashRef just above; root is the operator-supplied repo path, not remote input
		if err != nil {
			report.fail("entry %q: artifact.url resolves to %s but it could not be read: %v", e.ID, artifactPath, err)
			continue
		}
		report.ArtifactsChecked++

		if got := HashRef(data); got != e.Artifact.Hash {
			report.fail("entry %q: artifact.hash is %s but the bytes on disk hash to %s", e.ID, e.Artifact.Hash, got)
		}
		if got := int64(len(data)); got != e.Artifact.Size {
			report.fail("entry %q: artifact.size is %d but the bytes on disk are %d", e.ID, e.Artifact.Size, got)
		}
		if e.Artifact.Uncompressed != nil {
			decompressed, err := DecompressZstd(data)
			if err != nil {
				report.fail("entry %q: could not re-decompress to check uncompressed.hash: %v", e.ID, err)
			} else {
				if got := HashRef(decompressed); got != e.Artifact.Uncompressed.Hash {
					report.fail("entry %q: uncompressed.hash is %s but decompressing the bytes on disk gives %s", e.ID, e.Artifact.Uncompressed.Hash, got)
				}
				if got := int64(len(decompressed)); got != e.Artifact.Uncompressed.Size {
					report.fail("entry %q: uncompressed.size is %d but decompressing the bytes on disk gives %d", e.ID, e.Artifact.Uncompressed.Size, got)
				}
			}
		}
	}

	// Orphan detection (spec §5.4 gate 4 / §5.3): every artifact file on
	// disk must be referenced by at least one entry.
	artifactsDir := filepath.Join(root, "artifacts", "sha256")
	if files, err := os.ReadDir(artifactsDir); err == nil {
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			if !referenced[f.Name()] {
				report.fail("orphan artifact %s is not referenced by any catalog entry", f.Name())
			}
		}
	}

	// Manifest freshness (spec §5.5): regenerating from the same entries with
	// the currently-committed generatedAt must reproduce manifest.json byte-for-byte.
	manifestPath := filepath.Join(root, "catalog", "manifest.json")
	committed, err := os.ReadFile(manifestPath) //#nosec G304 -- fixed literal suffix; root is the operator-supplied repo path, not remote input
	if err != nil {
		report.fail("could not read catalog/manifest.json: %v", err)
	} else {
		var existing catalog.Manifest
		if err := loadJSON(manifestPath, &existing); err != nil {
			report.fail("catalog/manifest.json is not valid JSON: %v", err)
		} else {
			fresh := catalog.BuildManifest(catalog.PublishedOnly(entries), existing.GeneratedAt)
			freshBytes, err := catalog.MarshalDeterministic(fresh)
			if err != nil {
				report.fail("failed to regenerate manifest for comparison: %v", err)
			} else if !catalog.Equal(committed, freshBytes) {
				report.fail("catalog/manifest.json does not match what circuitctl would generate from catalog/circuits/*.json — it was hand-edited or is stale; run circuitctl regenerate")
			}
		}
	}

	return report, nil
}

func loadJSON(path string, v any) error {
	data, err := os.ReadFile(path) //#nosec G304 -- always called with a path this package already constructed, never remote input
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func hexFromHashRef(ref string) (string, error) {
	const prefix = "sha256:"
	if len(ref) != len(prefix)+64 || ref[:len(prefix)] != prefix {
		return "", fmt.Errorf("hash %q is not sha256:<64 lowercase hex>", ref)
	}
	digest := ref[len(prefix):]
	if !hexDigestPattern.MatchString(digest) {
		return "", fmt.Errorf("hash %q does not have a pure lowercase-hex digest", ref)
	}
	return digest, nil
}
