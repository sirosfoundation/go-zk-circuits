package publish

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"

	"github.com/sirosfoundation/go-zk-circuits/pkg/catalog"
)

func writeZstdFixture(t *testing.T, dir, filename string, raw []byte) string {
	t.Helper()
	enc, err := zstd.NewWriter(nil)
	require.NoError(t, err)
	compressed := enc.EncodeAll(raw, nil)
	require.NoError(t, enc.Close())
	path := filepath.Join(dir, filename)
	require.NoError(t, os.WriteFile(path, compressed, 0o600))
	return path
}

func TestParseLongfellowFilename(t *testing.T) {
	p, err := ParseLongfellowFilename("8_2_4307_2945_bb8e6a26d2700ddad968562d1c4aee83067772fee6f889748a0bc64f2c694ad5")
	require.NoError(t, err)
	require.Equal(t, 8, p.Version)
	require.Equal(t, 2, p.NumAttributes)
	require.Equal(t, 4307, p.BlockEncHash)
	require.Equal(t, 2945, p.BlockEncSig)
	require.Equal(t, "longfellow-libzk-v1_8_2_4307_2945", p.DefaultID())
}

func TestParseLongfellowFilename_RejectsBadConvention(t *testing.T) {
	_, err := ParseLongfellowFilename("not-a-circuit-filename")
	require.Error(t, err)
}

func TestCrossCheckParam_DetectsContradiction(t *testing.T) {
	p, err := ParseLongfellowFilename("8_2_4307_2945_deadbeef")
	require.NoError(t, err)
	require.NoError(t, p.CrossCheckParam("num_attributes", "2"))
	require.Error(t, p.CrossCheckParam("num_attributes", "99"))
	require.NoError(t, p.CrossCheckParam("unrelated_key", "anything")) // not filename-derived, passes through
}

func TestAddThenVerify_HappyPath(t *testing.T) {
	root := t.TempDir()
	raw := []byte("this is fake circuit content for a test fixture, not a real circuit")
	inputDir := t.TempDir()
	file := writeZstdFixture(t, inputDir, "8_2_4307_2945_deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", raw)

	result, err := Add(root, AddOptions{
		InputFile: file,
		System:    "longfellow",
		Origin:    "https://example.invalid/test",
	})
	require.NoError(t, err)
	require.Equal(t, "longfellow-libzk-v1_8_2_4307_2945", result.Entry.ID)
	require.Equal(t, catalog.CompressionZstd, result.Entry.Artifact.Compression)
	require.NotNil(t, result.Entry.Artifact.Uncompressed)

	require.NoError(t, RegenerateManifest(root, "2026-08-13T21:40:11Z"))

	report, err := Verify(root)
	require.NoError(t, err)
	require.True(t, report.OK(), "problems: %v", report.Problems)
	require.Equal(t, 1, report.EntriesChecked)
	require.Equal(t, 1, report.ArtifactsChecked)
}

func TestAdd_RejectsFilenameParamContradiction(t *testing.T) {
	root := t.TempDir()
	inputDir := t.TempDir()
	file := writeZstdFixture(t, inputDir, "8_2_4307_2945_deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", []byte("x"))

	_, err := Add(root, AddOptions{
		InputFile: file,
		System:    "longfellow",
		Origin:    "https://example.invalid/test",

		ExplicitParams: map[string]string{"num_attributes": "99"},
	})
	require.Error(t, err)
}

func TestAdd_RejectsIDCollision(t *testing.T) {
	root := t.TempDir()
	inputDir := t.TempDir()
	file := writeZstdFixture(t, inputDir, "8_2_4307_2945_deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", []byte("x"))

	_, err := Add(root, AddOptions{InputFile: file, System: "longfellow", Origin: "o"})
	require.NoError(t, err)

	_, err = Add(root, AddOptions{InputFile: file, System: "longfellow", Origin: "o"})
	require.Error(t, err, "adding the same id twice must fail")
}

func TestAdd_RejectsAliasCollidingWithExistingID(t *testing.T) {
	root := t.TempDir()
	inputDir := t.TempDir()
	fileA := writeZstdFixture(t, inputDir, "8_2_4307_2945_deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", []byte("a"))
	_, err := Add(root, AddOptions{InputFile: fileA, System: "longfellow", Origin: "o"})
	require.NoError(t, err)

	fileB := writeZstdFixture(t, inputDir, "8_3_4307_2945_deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", []byte("b"))
	_, err = Add(root, AddOptions{
		InputFile: fileB, System: "longfellow", Origin: "o",
		Aliases: []string{"longfellow-libzk-v1_8_2_4307_2945"}, // collides with fileA's id
	})
	require.Error(t, err)
}

func TestAdd_RejectsIDCollidingWithExistingAlias(t *testing.T) {
	root := t.TempDir()
	inputDir := t.TempDir()
	fileA := writeZstdFixture(t, inputDir, "8_2_4307_2945_deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", []byte("a"))
	_, err := Add(root, AddOptions{
		InputFile: fileA, System: "longfellow", Origin: "o",
		Aliases: []string{"shared-alias"},
	})
	require.NoError(t, err)

	fileB := writeZstdFixture(t, inputDir, "8_3_4307_2945_deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", []byte("b"))
	_, err = Add(root, AddOptions{InputFile: fileB, System: "longfellow", ID: "shared-alias", Origin: "o"})
	require.Error(t, err)
}

func TestAdd_RejectsAliasCollidingWithExistingAlias(t *testing.T) {
	root := t.TempDir()
	inputDir := t.TempDir()
	fileA := writeZstdFixture(t, inputDir, "8_2_4307_2945_deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", []byte("a"))
	_, err := Add(root, AddOptions{
		InputFile: fileA, System: "longfellow", Origin: "o",
		Aliases: []string{"shared-alias"},
	})
	require.NoError(t, err)

	fileB := writeZstdFixture(t, inputDir, "8_3_4307_2945_deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", []byte("b"))
	_, err = Add(root, AddOptions{
		InputFile: fileB, System: "longfellow", Origin: "o",
		Aliases: []string{"shared-alias"},
	})
	require.Error(t, err)
}

func TestIsZstdFrame(t *testing.T) {
	require.False(t, IsZstdFrame(nil))
	require.False(t, IsZstdFrame([]byte{0x01, 0x02}))
	require.False(t, IsZstdFrame([]byte{0x00, 0x00, 0x00, 0x00, 0x00}))

	enc, err := zstd.NewWriter(nil)
	require.NoError(t, err)
	frame := enc.EncodeAll([]byte("real frame"), nil)
	require.NoError(t, enc.Close())
	require.True(t, IsZstdFrame(frame))
}

func TestAdd_RejectsOversizedInput(t *testing.T) {
	root := t.TempDir()
	inputDir := t.TempDir()
	file := writeZstdFixture(t, inputDir, "8_2_4307_2945_deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", make([]byte, 100))

	_, err := Add(root, AddOptions{InputFile: file, System: "longfellow", Origin: "o", MaxBytes: 10})
	require.Error(t, err)
}

func TestAdd_RequiresOrigin(t *testing.T) {
	root := t.TempDir()
	inputDir := t.TempDir()
	file := writeZstdFixture(t, inputDir, "8_2_4307_2945_deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", []byte("x"))

	_, err := Add(root, AddOptions{InputFile: file, System: "longfellow"})
	require.Error(t, err, "missing origin must fail — spec §5.4 gate 2")
}

func TestVerify_DetectsHandEditedManifest(t *testing.T) {
	root := t.TempDir()
	inputDir := t.TempDir()
	file := writeZstdFixture(t, inputDir, "8_2_4307_2945_deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", []byte("x"))
	_, err := Add(root, AddOptions{InputFile: file, System: "longfellow", Origin: "o"})
	require.NoError(t, err)
	require.NoError(t, RegenerateManifest(root, "2026-08-13T21:40:11Z"))

	// Hand-edit the committed manifest — simulates someone bypassing circuitctl.
	manifestPath := filepath.Join(root, "catalog", "manifest.json")
	data, err := os.ReadFile(manifestPath) //#nosec G304 -- manifestPath is always under t.TempDir() in this test
	require.NoError(t, err)
	tampered := append(data[:len(data)-2], []byte(`,"tampered":true}`)...) // crude but sufficient to break byte-equality
	require.NoError(t, os.WriteFile(manifestPath, tampered, 0o600))        //#nosec G703 -- manifestPath is always under t.TempDir() in this test

	report, err := Verify(root)
	require.NoError(t, err)
	require.False(t, report.OK(), "hand-edited manifest must be caught")
}

func TestVerify_DetectsOrphanArtifact(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "artifacts", "sha256"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "catalog", "circuits"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "artifacts", "sha256", "orphanhash"), []byte("orphan"), 0o600))
	require.NoError(t, RegenerateManifest(root, "2026-08-13T21:40:11Z"))

	report, err := Verify(root)
	require.NoError(t, err)
	require.False(t, report.OK())
}

func TestVerify_DetectsTamperedArtifactBytes(t *testing.T) {
	root := t.TempDir()
	inputDir := t.TempDir()
	file := writeZstdFixture(t, inputDir, "8_2_4307_2945_deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", []byte("original content"))
	result, err := Add(root, AddOptions{InputFile: file, System: "longfellow", Origin: "o"})
	require.NoError(t, err)
	require.NoError(t, RegenerateManifest(root, "2026-08-13T21:40:11Z"))

	require.NoError(t, os.WriteFile(result.ArtifactPath, []byte("tampered content, different bytes"), 0o600))

	report, err := Verify(root)
	require.NoError(t, err)
	require.False(t, report.OK(), "tampered artifact bytes must be caught even though manifest.json is untouched")
}

func TestDeprecateThenRevoke(t *testing.T) {
	root := t.TempDir()
	inputDir := t.TempDir()
	file := writeZstdFixture(t, inputDir, "8_2_4307_2945_deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", []byte("x"))
	result, err := Add(root, AddOptions{InputFile: file, System: "longfellow", Origin: "o"})
	require.NoError(t, err)

	require.NoError(t, Deprecate(root, result.Entry.ID, "superseded by a newer circuit"))
	entries, err := LoadCatalogEntries(root)
	require.NoError(t, err)
	require.Equal(t, catalog.StatusDeprecated, entries[0].Status)

	require.NoError(t, Revoke(root, result.Entry.ID, "found to be unsound"))
	entries, err = LoadCatalogEntries(root)
	require.NoError(t, err)
	require.Equal(t, catalog.StatusRevoked, entries[0].Status)

	require.Error(t, Revoke(root, result.Entry.ID, "again"), "revoking an already-revoked entry must fail")
}

func TestLifecycle_RequiresReason(t *testing.T) {
	root := t.TempDir()
	inputDir := t.TempDir()
	file := writeZstdFixture(t, inputDir, "8_2_4307_2945_deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", []byte("x"))
	result, err := Add(root, AddOptions{InputFile: file, System: "longfellow", Origin: "o"})
	require.NoError(t, err)

	require.Error(t, Deprecate(root, result.Entry.ID, ""))
}

func TestAdd_DefaultsToPublished(t *testing.T) {
	root := t.TempDir()
	inputDir := t.TempDir()
	file := writeZstdFixture(t, inputDir, "8_2_4307_2945_deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", []byte("x"))

	result, err := Add(root, AddOptions{InputFile: file, System: "longfellow", Origin: "o"})
	require.NoError(t, err)
	require.True(t, result.Entry.Published)
}

func TestAdd_UnpublishedFlagKeepsEntryOutOfManifest(t *testing.T) {
	root := t.TempDir()
	inputDir := t.TempDir()
	file := writeZstdFixture(t, inputDir, "8_2_4307_2945_deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", []byte("x"))

	result, err := Add(root, AddOptions{InputFile: file, System: "longfellow", Origin: "o", Unpublished: true})
	require.NoError(t, err)
	require.False(t, result.Entry.Published)

	require.NoError(t, RegenerateManifest(root, "2026-08-13T21:40:11Z"))

	served, err := os.ReadFile(filepath.Join(root, "catalog", "manifest.json")) //#nosec G304 -- fixed literal suffix under t.TempDir()
	require.NoError(t, err)
	require.NotContains(t, string(served), result.Entry.ID, "an unpublished entry must not appear in the generated manifest at all")

	// But it must still be fully present and integrity-checked on disk —
	// unpublished means "not servable," not "not tracked."
	report, err := Verify(root)
	require.NoError(t, err)
	require.True(t, report.OK(), "problems: %v", report.Problems)
	require.Equal(t, 1, report.EntriesChecked)
	require.Equal(t, 1, report.ArtifactsChecked)
}

func TestAdd_OpenSourceDefaultsFalseAndCanBeAsserted(t *testing.T) {
	root := t.TempDir()
	inputDir := t.TempDir()
	file := writeZstdFixture(t, inputDir, "8_2_4307_2945_deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", []byte("x"))

	result, err := Add(root, AddOptions{InputFile: file, System: "longfellow", Origin: "o"})
	require.NoError(t, err)
	require.False(t, result.Entry.Source.OpenSource, "openSource must default to false, not be inferred from anything")

	root2 := t.TempDir()
	result2, err := Add(root2, AddOptions{
		InputFile: file, System: "longfellow", Origin: "o",
		OpenSource: true, License: "MPL-2.0", Toolchain: "cargo build --release, rustc 1.84.0",
	})
	require.NoError(t, err)
	require.True(t, result2.Entry.Source.OpenSource)
	require.Equal(t, "MPL-2.0", result2.Entry.Source.License)
	require.Equal(t, "cargo build --release, rustc 1.84.0", result2.Entry.Source.Toolchain)
}

func TestPublishThenUnpublish(t *testing.T) {
	root := t.TempDir()
	inputDir := t.TempDir()
	file := writeZstdFixture(t, inputDir, "8_2_4307_2945_deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", []byte("x"))
	result, err := Add(root, AddOptions{InputFile: file, System: "longfellow", Origin: "o", Unpublished: true})
	require.NoError(t, err)
	require.False(t, result.Entry.Published)

	// No --reason required for publish/unpublish, unlike deprecate/revoke.
	require.NoError(t, Publish(root, result.Entry.ID, ""))
	entries, err := LoadCatalogEntries(root)
	require.NoError(t, err)
	require.True(t, entries[0].Published)

	require.NoError(t, Unpublish(root, result.Entry.ID, "provenance still unresolved"))
	entries, err = LoadCatalogEntries(root)
	require.NoError(t, err)
	require.False(t, entries[0].Published)
	require.Contains(t, entries[0].Notes, "provenance still unresolved")
}

func TestStaleRows_FlagsUnverifiedActiveEntries(t *testing.T) {
	root := t.TempDir()
	inputDir := t.TempDir()
	file := writeZstdFixture(t, inputDir, "8_2_4307_2945_deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", []byte("x"))
	_, err := Add(root, AddOptions{InputFile: file, System: "longfellow", Origin: "o"})
	require.NoError(t, err)

	stale, err := StaleRows(root)
	require.NoError(t, err)
	require.Len(t, stale, 1)
}
