package catalog

import (
	"encoding/json"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

func sampleEntry(id string) CircuitDescriptor {
	return CircuitDescriptor{
		ID:            id,
		System:        "longfellow",
		SystemVersion: "8",
		Status:        StatusActive,
		Params: map[string]any{
			"version":        float64(8),
			"num_attributes": float64(2),
		},
		Source: &Source{
			Origin: "https://example.invalid/origin",
		},
		PublishedAt: "2026-08-13T21:40:11Z",
	}
}

func TestPublishedOnly_FiltersOutUnpublished(t *testing.T) {
	published := sampleEntry("published-one")
	published.Published = true
	unpublished := sampleEntry("unpublished-one") // Published left at zero value (false)

	out := PublishedOnly([]CircuitDescriptor{published, unpublished})
	require.Len(t, out, 1)
	require.Equal(t, "published-one", out[0].ID)
}

func TestPublishedOnly_EmptyInputGivesEmptyOutput(t *testing.T) {
	require.Empty(t, PublishedOnly(nil))
}

func TestBuildManifest_SortsByID(t *testing.T) {
	m := BuildManifest([]CircuitDescriptor{sampleEntry("zzz"), sampleEntry("aaa")}, "2026-08-13T21:40:11Z")
	require.Equal(t, "aaa", m.Circuits[0].ID)
	require.Equal(t, "zzz", m.Circuits[1].ID)
}

func TestMarshalDeterministic_StableAcrossRebuilds(t *testing.T) {
	m1 := BuildManifest([]CircuitDescriptor{sampleEntry("b"), sampleEntry("a")}, "2026-08-13T21:40:11Z")
	m2 := BuildManifest([]CircuitDescriptor{sampleEntry("a"), sampleEntry("b")}, "2026-08-13T21:40:11Z")

	out1, err := MarshalDeterministic(m1)
	require.NoError(t, err)
	out2, err := MarshalDeterministic(m2)
	require.NoError(t, err)

	require.True(t, Equal(out1, out2), "manifest bytes must not depend on input order")
	require.Equal(t, byte('\n'), out1[len(out1)-1], "must end with exactly one trailing newline")
}

func TestValidateManifest_RejectsUnknownVersion(t *testing.T) {
	m := &Manifest{ManifestVersion: 2, Catalog: CatalogName, GeneratedAt: "2026-08-13T21:40:11Z"}
	require.Error(t, ValidateManifest(m))
}

func TestValidateManifest_RejectsWrongCatalogName(t *testing.T) {
	m := &Manifest{ManifestVersion: ManifestVersion, Catalog: "something-else", GeneratedAt: "2026-08-13T21:40:11Z"}
	require.Error(t, ValidateManifest(m))
}

func TestValidateManifest_DetectsAliasCollision(t *testing.T) {
	a := sampleEntry("a")
	a.Aliases = []string{"shared-alias"}
	b := sampleEntry("b")
	b.Aliases = []string{"shared-alias"}
	m := BuildManifest([]CircuitDescriptor{a, b}, "2026-08-13T21:40:11Z")
	require.Error(t, ValidateManifest(m))
}

func TestValidateEntry_RejectsBadID(t *testing.T) {
	e := sampleEntry("has a space")
	require.Error(t, ValidateEntry(&e))
}

func TestValidateEntry_RejectsNonScalarParam(t *testing.T) {
	e := sampleEntry("ok-id")
	e.Params["nested"] = map[string]any{"x": 1}
	require.Error(t, ValidateEntry(&e))
}

func TestValidateEntry_RequiresDeprecatedAtWhenNotActive(t *testing.T) {
	e := sampleEntry("ok-id")
	e.Status = StatusDeprecated
	require.Error(t, ValidateEntry(&e))
	e.DeprecatedAt = "2026-08-13T21:40:11Z"
	require.NoError(t, ValidateEntry(&e))
}

func TestValidateEntry_ArtifactHashFormat(t *testing.T) {
	e := sampleEntry("ok-id")
	e.Artifact = &Artifact{
		URL:         "/v1/artifacts/sha256/deadbeef",
		Hash:        "sha256:not-hex",
		Size:        10,
		Compression: CompressionZstd,
		MediaType:   "application/zstd",
	}
	require.Error(t, ValidateEntry(&e))
}

func TestValidateEntry_VerifiedByRequiresValidResult(t *testing.T) {
	e := sampleEntry("ok-id")
	e.Source.VerifiedBy = []VerificationRecord{{
		Tool: "circuitctl verify-interop", ToolVersion: "0.1.0", VerifierIdentity: "test-verifier",
		Date: "2026-08-13T21:40:11Z", Result: "maybe",
	}}
	require.Error(t, ValidateEntry(&e))
}

func TestLoadEntriesFromDir_MissingDirIsNotAnError(t *testing.T) {
	fsys := fstest.MapFS{}
	entries, err := LoadEntriesFromDir(fsys, "catalog/circuits")
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestEntryFilePath_And_ArtifactFilePath(t *testing.T) {
	require.Equal(t, "catalog/circuits/foo.json", EntryFilePath("foo"))
	require.Equal(t, "artifacts/sha256/deadbeef", ArtifactFilePath("deadbeef"))
}

func TestLoadEntriesFromDir_ReadsAndSkipsNonJSON(t *testing.T) {
	entryBytes, err := json.Marshal(sampleEntry("a"))
	require.NoError(t, err)

	fsys := fstest.MapFS{
		"catalog/circuits/a.json":     {Data: entryBytes},
		"catalog/circuits/README.txt": {Data: []byte("not json, must be skipped")},
	}

	entries, err := LoadEntriesFromDir(fsys, "catalog/circuits")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "a", entries[0].ID)
}

func TestLoadEntriesFromDir_SkipsSubdirectories(t *testing.T) {
	entryBytes, err := json.Marshal(sampleEntry("a"))
	require.NoError(t, err)

	fsys := fstest.MapFS{
		"catalog/circuits/a.json":        {Data: entryBytes},
		"catalog/circuits/subdir/b.json": {Data: entryBytes},
	}

	entries, err := LoadEntriesFromDir(fsys, "catalog/circuits")
	require.NoError(t, err)
	require.Len(t, entries, 1, "a directory entry must be skipped, not descended into")
}

func TestLoadEntriesFromDir_RejectsInvalidJSON(t *testing.T) {
	fsys := fstest.MapFS{"catalog/circuits/bad.json": {Data: []byte("not json")}}
	_, err := LoadEntriesFromDir(fsys, "catalog/circuits")
	require.Error(t, err)
}

func TestLoadManifestFile_HappyPath(t *testing.T) {
	m := BuildManifest([]CircuitDescriptor{sampleEntry("a")}, "2026-08-13T21:40:11Z")
	data, err := MarshalDeterministic(m)
	require.NoError(t, err)
	fsys := fstest.MapFS{"catalog/manifest.json": {Data: data}}

	loaded, err := LoadManifestFile(fsys, "catalog/manifest.json")
	require.NoError(t, err)
	require.Equal(t, "a", loaded.Circuits[0].ID)
}

func TestLoadManifestFile_MissingFile(t *testing.T) {
	fsys := fstest.MapFS{}
	_, err := LoadManifestFile(fsys, "catalog/manifest.json")
	require.Error(t, err)
}

func TestLoadManifestFile_InvalidJSON(t *testing.T) {
	fsys := fstest.MapFS{"catalog/manifest.json": {Data: []byte("not json")}}
	_, err := LoadManifestFile(fsys, "catalog/manifest.json")
	require.Error(t, err)
}
