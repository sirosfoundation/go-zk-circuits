package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
)

// PublishedOnly filters entries down to Published==true (spec §2.4.1). This
// is what the generated catalog/manifest.json — and therefore everything
// the service ever serves — is built from; circuitctl's own operations
// (verify, ls) deliberately use the unfiltered set instead, since an
// unpublished entry's bytes and metadata still need full tooling coverage.
func PublishedOnly(entries []CircuitDescriptor) []CircuitDescriptor {
	out := make([]CircuitDescriptor, 0, len(entries))
	for _, e := range entries {
		if e.Published {
			out = append(out, e)
		}
	}
	return out
}

// BuildManifest assembles a Manifest from a set of per-entry descriptors,
// sorting them by id (spec §5.5: "Entries sorted by id, lexicographic byte
// order") so the result is deterministic regardless of read order.
func BuildManifest(entries []CircuitDescriptor, generatedAt string) *Manifest {
	sorted := make([]CircuitDescriptor, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	return &Manifest{
		ManifestVersion: ManifestVersion,
		GeneratedAt:     generatedAt,
		Catalog:         CatalogName,
		Circuits:        sorted,
		Next:            nil,
	}
}

// MarshalDeterministic renders the manifest per spec §5.5: 2-space indent,
// "\n" line endings, no trailing whitespace, UTF-8 no BOM, and (via
// BuildManifest's caller having already sorted entries and Go's
// encoding/json sorting map keys) a fully reproducible byte sequence for
// unchanged input. This is what CI compares against the committed
// catalog/manifest.json to reject hand-edits (spec §5.4 gate 1, §5.5).
func MarshalDeterministic(m *Manifest) ([]byte, error) {
	buf, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	// encoding/json never emits a BOM and uses \n; append exactly one
	// trailing newline, matching how gofmt-adjacent tools write JSON files.
	buf = append(buf, '\n')
	return buf, nil
}

// LoadEntriesFromDir reads every catalog/circuits/<id>.json file from fsys
// (either an os.DirFS in local dev, or an embed.FS at runtime — spec §5.2:
// "catalog/circuits/<id>.json ... catalog/manifest.json as a generated build
// product"). dir is the path within fsys to the circuits directory.
func LoadEntriesFromDir(fsys fs.FS, dir string) ([]CircuitDescriptor, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil // no circuits published yet — not an error
		}
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}
	var out []CircuitDescriptor
	for _, de := range entries {
		if de.IsDir() || path.Ext(de.Name()) != ".json" {
			continue
		}
		data, err := fs.ReadFile(fsys, path.Join(dir, de.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", de.Name(), err)
		}
		var cd CircuitDescriptor
		if err := json.Unmarshal(data, &cd); err != nil {
			return nil, fmt.Errorf("parse %s: %w", de.Name(), err)
		}
		out = append(out, cd)
	}
	return out, nil
}

// LoadManifestFile reads and parses an already-generated manifest.json from fsys.
func LoadManifestFile(fsys fs.FS, filePath string) (*Manifest, error) {
	data, err := fs.ReadFile(fsys, filePath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filePath, err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", filePath, err)
	}
	return &m, nil
}

// EntryFilePath returns the per-entry source-of-truth path for an id (spec §5.2).
func EntryFilePath(id string) string {
	return path.Join("catalog", "circuits", id+".json")
}

// ArtifactFilePath returns the content-addressed artifact path for a sha256 hex digest (spec §2.6, §4.6).
func ArtifactFilePath(hexDigest string) string {
	return path.Join("artifacts", "sha256", hexDigest)
}

// Equal reports whether two marshaled manifests are byte-identical — the
// exact check `circuitctl verify` runs to catch hand-editing (spec §5.4 gate 1).
func Equal(a, b []byte) bool {
	return bytes.Equal(a, b)
}
