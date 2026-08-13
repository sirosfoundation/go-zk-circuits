package publish

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/sirosfoundation/go-zk-circuits/pkg/catalog"
)

// Deprecate implements circuitctl deprecate (spec §5.3, §5.6 level 1):
// still served, still usable, signals "prefer something else" to maintainers only.
func Deprecate(root, id, reason string) error {
	return setStatus(root, id, catalog.StatusDeprecated, reason)
}

// Revoke implements circuitctl revoke (spec §5.3, §5.6 level 2): still
// served (so bytes remain inspectable) but conforming clients MUST refuse
// to use it even from cache (spec §7.4). Fail-closed, so a compromised
// manifest asserting this is a DoS, never a bad proof — which is why this
// is deliberately a separate, harder-to-invoke command from Deprecate.
func Revoke(root, id, reason string) error {
	return setStatus(root, id, catalog.StatusRevoked, reason)
}

// Publish implements circuitctl publish (spec §5.3, §2.4.1): marks the
// entry published=true, so the next manifest regeneration includes it.
func Publish(root, id, reason string) error {
	return setPublished(root, id, true, reason)
}

// Unpublish implements circuitctl unpublish (spec §5.3, §2.4.1): marks the
// entry published=false, so the next manifest regeneration excludes it —
// the entry and its artifact bytes stay in the repo, but the deployed
// service can no longer serve them at all (ArtifactHandler included).
func Unpublish(root, id, reason string) error {
	return setPublished(root, id, false, reason)
}

func setPublished(root, id string, published bool, reason string) error {
	entryPath := catalog.EntryFilePath(id)
	fullPath := filepath.Join(root, entryPath)
	var entry catalog.CircuitDescriptor
	if err := loadJSON(fullPath, &entry); err != nil {
		return fmt.Errorf("load entry %q: %w", id, err)
	}
	entry.Published = published
	if reason != "" {
		action := "unpublish"
		if published {
			action = "publish"
		}
		if entry.Notes != "" {
			entry.Notes = entry.Notes + " | " + action + ": " + reason
		} else {
			entry.Notes = action + ": " + reason
		}
	}
	if err := catalog.ValidateEntry(&entry); err != nil {
		return fmt.Errorf("updated entry failed validation: %w", err)
	}
	return writeEntryFile(fullPath, &entry)
}

func setStatus(root, id, status, reason string) error {
	if reason == "" {
		return fmt.Errorf("--reason is required for %s (spec §5.3: this is a client-visible, consequential action)", status)
	}
	entryPath := catalog.EntryFilePath(id)
	var entry catalog.CircuitDescriptor
	fullPath := filepath.Join(root, entryPath)
	if err := loadJSON(fullPath, &entry); err != nil {
		return fmt.Errorf("load entry %q: %w", id, err)
	}
	if entry.Status == catalog.StatusRevoked {
		return fmt.Errorf("entry %q is already revoked; revocation is not reversed by this tool", id)
	}
	entry.Status = status
	entry.DeprecatedAt = time.Now().UTC().Format(time.RFC3339)
	if entry.Notes != "" {
		entry.Notes = entry.Notes + " | " + status + ": " + reason
	} else {
		entry.Notes = status + ": " + reason
	}
	if err := catalog.ValidateEntry(&entry); err != nil {
		return fmt.Errorf("updated entry failed validation: %w", err)
	}
	return writeEntryFile(fullPath, &entry)
}
