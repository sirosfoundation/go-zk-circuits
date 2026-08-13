// Package publish holds circuitctl's logic, shared with the service via
// pkg/catalog's loading/validation code (spec §5.3: "sharing pkg/publish
// with the service so validation logic exists once").
package publish

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sirosfoundation/go-zk-circuits/pkg/catalog"
)

// LoadCatalogEntries reads every catalog/circuits/<id>.json under root.
func LoadCatalogEntries(root string) ([]catalog.CircuitDescriptor, error) {
	return catalog.LoadEntriesFromDir(os.DirFS(root), "catalog/circuits")
}

// writeEntryFile writes a single entry as its own pretty-printed JSON file.
// This does not need to be byte-reproducible the way the generated manifest
// does (spec §5.5) — only manifest.json is a build product with a
// determinism requirement; the per-entry file is the hand-reviewed source of truth.
func writeEntryFile(path string, entry *catalog.CircuitDescriptor) error {
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal entry: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("mkdir for entry file: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write entry file: %w", err)
	}
	return nil
}

// RegenerateManifest rebuilds catalog/manifest.json from the per-entry
// files under root (spec §5.2, §5.5), **excluding any entry with
// published=false** (spec §2.4.1) — an unpublished entry stays in the repo
// as source of truth but never reaches the generated manifest, which is the
// only thing the deployed service ever loads. generatedAt should be the
// publishing commit's timestamp in CI (spec §5.5's determinism rule); pass
// "" to use the current time, which is fine for local dev but NOT
// reproducible — callers that need reproducibility must supply it explicitly.
func RegenerateManifest(root string, generatedAt string) error {
	entries, err := LoadCatalogEntries(root)
	if err != nil {
		return err
	}
	if generatedAt == "" {
		return fmt.Errorf("generatedAt must not be empty when regenerating the committed manifest")
	}
	m := catalog.BuildManifest(catalog.PublishedOnly(entries), generatedAt)
	if err := catalog.ValidateManifest(m); err != nil {
		return fmt.Errorf("generated manifest failed validation: %w", err)
	}
	out, err := catalog.MarshalDeterministic(m)
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(root, "catalog", "manifest.json")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o750); err != nil {
		return fmt.Errorf("mkdir for manifest: %w", err)
	}
	return os.WriteFile(manifestPath, out, 0o600)
}
