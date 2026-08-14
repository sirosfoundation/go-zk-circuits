package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

// writeZstdFixture mirrors pkg/publish's own test helper — kept as a
// separate copy since main_test.go can't import an unexported test helper
// from another package.
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

func TestSplitLeadingPositional_PositionalFirst(t *testing.T) {
	pos, rest, err := splitLeadingPositional([]string{"file.zst", "--system", "longfellow", "--origin", "o"}, nil)
	require.NoError(t, err)
	require.Equal(t, "file.zst", pos)
	require.Equal(t, []string{"--system", "longfellow", "--origin", "o"}, rest)
}

func TestSplitLeadingPositional_BoolFlagDoesNotSwallowNextFlag(t *testing.T) {
	// Regression test: a boolean flag (no value) sitting between two
	// value-taking flags previously consumed the next flag's NAME as its
	// own "value", stranding that flag's real value as a bare token that
	// got misidentified as a second positional argument. Caught by manual
	// testing against `circuitctl add ... --open-source --toolchain X`,
	// not by any test — hence this one.
	pos, rest, err := splitLeadingPositional([]string{
		"file.zst", "--license", "MPL-2.0", "--open-source", "--toolchain", "bazel 7.1",
	}, map[string]bool{"open-source": true})
	require.NoError(t, err)
	require.Equal(t, "file.zst", pos)
	require.Equal(t, []string{"--license", "MPL-2.0", "--open-source", "--toolchain", "bazel 7.1"}, rest)
}

func TestSplitLeadingPositional_BoolFlagAtEnd(t *testing.T) {
	pos, rest, err := splitLeadingPositional([]string{"file.zst", "--toolchain", "a", "--unpublished"}, map[string]bool{"unpublished": true})
	require.NoError(t, err)
	require.Equal(t, "file.zst", pos)
	require.Equal(t, []string{"--toolchain", "a", "--unpublished"}, rest)
}

func TestSplitLeadingPositional_RejectsMultiplePositionals(t *testing.T) {
	_, _, err := splitLeadingPositional([]string{"a", "b"}, nil)
	require.Error(t, err)
}

func TestSplitLeadingPositional_RejectsNoPositionals(t *testing.T) {
	_, _, err := splitLeadingPositional([]string{"--system", "longfellow"}, nil)
	require.Error(t, err)
}

func TestRepeatedFlag_StringAndSet(t *testing.T) {
	var r repeatedFlag
	require.NoError(t, r.Set("a"))
	require.NoError(t, r.Set("b"))
	require.Equal(t, []string{"a", "b"}, []string(r))
	require.Equal(t, "a,b", r.String())
}

func TestReasonRequired(t *testing.T) {
	require.True(t, reasonRequired("deprecate"))
	require.True(t, reasonRequired("revoke"))
	require.False(t, reasonRequired("publish"))
	require.False(t, reasonRequired("unpublish"))
}

func TestUsage_DoesNotPanic(t *testing.T) {
	usage()
}

func addFixture(t *testing.T, root string) string {
	t.Helper()
	inputDir := t.TempDir()
	file := writeZstdFixture(t, inputDir,
		"8_2_4307_2945_deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		[]byte("fake circuit bytes for circuitctl main_test"))
	require.NoError(t, runAdd([]string{
		file, "--root", root, "--system", "longfellow",
		"--origin", "https://example.invalid/test",
	}))
	return "longfellow-libzk-v1_8_2_4307_2945"
}

func TestRunAdd_HappyPath(t *testing.T) {
	root := t.TempDir()
	id := addFixture(t, root)

	entryPath := filepath.Join(root, "catalog", "circuits", id+".json")
	require.FileExists(t, entryPath)
	manifestPath := filepath.Join(root, "catalog", "manifest.json")
	require.FileExists(t, manifestPath)
}

func TestRunAdd_MissingOriginFails(t *testing.T) {
	root := t.TempDir()
	inputDir := t.TempDir()
	file := writeZstdFixture(t, inputDir,
		"8_2_4307_2945_deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		[]byte("fake circuit bytes"))
	err := runAdd([]string{file, "--root", root, "--system", "longfellow"})
	require.Error(t, err)
}

func TestRunAdd_BadPositionalCount(t *testing.T) {
	err := runAdd([]string{"--system", "longfellow"})
	require.Error(t, err)
}

func TestRunAdd_BadParamFormat(t *testing.T) {
	root := t.TempDir()
	inputDir := t.TempDir()
	file := writeZstdFixture(t, inputDir,
		"8_2_4307_2945_deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		[]byte("fake circuit bytes"))
	err := runAdd([]string{
		file, "--root", root, "--system", "longfellow",
		"--origin", "o", "--param", "not-a-key-value-pair",
	})
	require.Error(t, err)
}

func TestRunVerify_HappyPath(t *testing.T) {
	root := t.TempDir()
	addFixture(t, root)
	require.NoError(t, runVerify([]string{"--root", root}))
}

func TestRunVerify_DetectsTamperedManifest(t *testing.T) {
	root := t.TempDir()
	addFixture(t, root)
	manifestPath := filepath.Join(root, "catalog", "manifest.json")
	require.NoError(t, os.WriteFile(manifestPath, []byte(`{"circuits":[]}`), 0o600))
	require.Error(t, runVerify([]string{"--root", root}))
}

func TestRunLs_HappyPathAndStale(t *testing.T) {
	root := t.TempDir()
	addFixture(t, root)
	require.NoError(t, runLs([]string{"--root", root}))
	require.NoError(t, runLs([]string{"--root", root, "--stale"}))
}

func TestRunLifecycle_DeprecateRequiresReason(t *testing.T) {
	root := t.TempDir()
	id := addFixture(t, root)
	err := runLifecycle([]string{id}, "deprecate")
	require.Error(t, err)
}

func TestRunLifecycle_DeprecateThenRevoke(t *testing.T) {
	root := t.TempDir()
	id := addFixture(t, root)
	require.NoError(t, runLifecycle([]string{id, "--root", root, "--reason", "test deprecation"}, "deprecate"))
	require.NoError(t, runLifecycle([]string{id, "--root", root, "--reason", "test revocation"}, "revoke"))
}

func TestRunLifecycle_PublishUnpublishReasonOptional(t *testing.T) {
	root := t.TempDir()
	id := addFixture(t, root)
	require.NoError(t, runLifecycle([]string{id, "--root", root}, "unpublish"))
	require.NoError(t, runLifecycle([]string{id, "--root", root}, "publish"))
}

func TestRunLifecycle_UnknownIDFails(t *testing.T) {
	root := t.TempDir()
	addFixture(t, root)
	err := runLifecycle([]string{"does-not-exist", "--root", root, "--reason", "x"}, "deprecate")
	require.Error(t, err)
}
