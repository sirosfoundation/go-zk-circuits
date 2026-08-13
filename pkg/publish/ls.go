package publish

import (
	"sort"

	"github.com/sirosfoundation/go-zk-circuits/pkg/catalog"
)

// LsRow is one line of `circuitctl ls` output.
type LsRow struct {
	ID            string
	System        string
	SystemVersion string
	Published     bool // spec §2.4.1 — false means this entry never reaches the served manifest
	Status        string
	Size          int64 // 0 for metadata-only entries
	VerifiedCount int
	Stale         bool // set by caller (Ls doesn't know about the SDK pin table's proverCrate version)
}

// Ls implements circuitctl ls (spec §5.3): a human-readable table, sorted
// by id for stable output. Deliberately shows every entry regardless of
// Published — this is the internal operator's view of the whole repo, not
// what a client would see.
func Ls(root string) ([]LsRow, error) {
	entries, err := LoadCatalogEntries(root)
	if err != nil {
		return nil, err
	}
	rows := make([]LsRow, 0, len(entries))
	for _, e := range entries {
		row := LsRow{ID: e.ID, System: e.System, SystemVersion: e.SystemVersion, Published: e.Published, Status: e.Status}
		if e.Artifact != nil {
			row.Size = e.Artifact.Size
		}
		if e.Source != nil {
			row.VerifiedCount = len(e.Source.VerifiedBy)
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}

// StaleRows implements the `--stale` flag (spec §5.7): flags any active
// entry with no interop verification recorded at all. Detecting staleness
// against a specific pinned proverCrate version is a Phase 4 concern once an
// SDK pin table actually exists to compare against — this is the
// unconditional, always-available half of the check.
func StaleRows(root string) ([]LsRow, error) {
	rows, err := Ls(root)
	if err != nil {
		return nil, err
	}
	var stale []LsRow
	for _, r := range rows {
		if r.Status == catalog.StatusActive && r.VerifiedCount == 0 {
			r.Stale = true
			stale = append(stale, r)
		}
	}
	return stale, nil
}
