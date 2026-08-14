package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/sirosfoundation/g119612/pkg/logging"
	"github.com/stretchr/testify/require"

	"github.com/sirosfoundation/go-zk-circuits/pkg/publish"
)

func TestEnvOr_DefaultAndOverride(t *testing.T) {
	require.Equal(t, "default", envOr("ZKC_TEST_UNSET_VAR", "default"))
	t.Setenv("ZKC_TEST_VAR", "custom")
	require.Equal(t, "custom", envOr("ZKC_TEST_VAR", "default"))
}

func TestEnvInt_DefaultOverrideAndInvalid(t *testing.T) {
	require.Equal(t, 42, envInt("ZKC_TEST_INT_UNSET", 42))
	t.Setenv("ZKC_TEST_INT", "7")
	require.Equal(t, 7, envInt("ZKC_TEST_INT", 42))
	t.Setenv("ZKC_TEST_INT_BAD", "not-a-number")
	require.Equal(t, 42, envInt("ZKC_TEST_INT_BAD", 42))
}

func TestHostOnly(t *testing.T) {
	require.Equal(t, "localhost:8080", hostOnly(":8080"))
	require.Equal(t, "example.invalid:9090", hostOnly("example.invalid:9090"))
}

func TestNewLogger_AllLevels(t *testing.T) {
	for _, level := range []string{"debug", "warn", "error", "info", "unrecognized"} {
		require.NotNil(t, newLogger(level))
	}
}

func TestResolveFS_EmbeddedByDefault(t *testing.T) {
	_, source := resolveFS("")
	require.Equal(t, "embedded", source)
}

func TestResolveFS_DirectoryWhenSet(t *testing.T) {
	dir := t.TempDir()
	_, source := resolveFS(dir)
	require.Equal(t, "directory:"+dir, source)
}

func TestConfigFromEnv_Defaults(t *testing.T) {
	for _, k := range []string{"ZKC_LISTEN", "ZKC_BASE_URL", "ZKC_LOG_LEVEL", "ZKC_RATE_LIMIT_RPS", "ZKC_RATE_LIMIT_BURST", "ZKC_ARTIFACT_DIR"} {
		original, wasSet := os.LookupEnv(k)
		require.NoError(t, os.Unsetenv(k))
		if wasSet {
			t.Cleanup(func() { _ = os.Setenv(k, original) })
		}
	}
	cfg := configFromEnv()
	require.Equal(t, ":8080", cfg.Listen)
	require.Equal(t, "info", cfg.LogLevel)
	require.Equal(t, 20, cfg.RateLimitRPS)
	require.Equal(t, 60, cfg.RateLimitBurst)
}

// buildCatalogFixture writes a minimal real catalog to disk, exactly like
// the other test suites' fixtures, so newServer exercises the same
// LoadCatalog path a deployed instance does.
func buildCatalogFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	enc, err := zstd.NewWriter(nil)
	require.NoError(t, err)
	compressed := enc.EncodeAll([]byte("fake circuit bytes for zkc main_test"), nil)
	require.NoError(t, enc.Close())
	inputDir := t.TempDir()
	inputFile := filepath.Join(inputDir, "8_2_4307_2945_deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	require.NoError(t, os.WriteFile(inputFile, compressed, 0o600))
	_, err = publish.Add(root, publish.AddOptions{InputFile: inputFile, System: "longfellow", Origin: "o"})
	require.NoError(t, err)
	require.NoError(t, publish.RegenerateManifest(root, "2026-08-14T00:00:00Z"))
	return root
}

func TestNewServer_BuildsWorkingHandlerWithConfiguredTimeouts(t *testing.T) {
	root := buildCatalogFixture(t)
	cfg := config{Listen: ":0", ArtifactDir: root, LogLevel: "debug", RateLimitRPS: 20, RateLimitBurst: 60}

	srv, cleanup, err := newServer(cfg, logging.DefaultLogger())
	require.NoError(t, err)
	defer cleanup()

	require.Equal(t, 30*time.Second, srv.ReadTimeout)
	require.Equal(t, 60*time.Second, srv.WriteTimeout)
	require.Equal(t, 120*time.Second, srv.IdleTimeout)
	require.Equal(t, 10*time.Second, srv.ReadHeaderTimeout)

	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	require.Equal(t, http.StatusOK, w.Code)

	w2 := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/v1/manifest.json", nil))
	require.Equal(t, http.StatusOK, w2.Code)
}

func TestNewServer_FailsWhenCatalogMissing(t *testing.T) {
	cfg := config{Listen: ":0", ArtifactDir: t.TempDir(), LogLevel: "info", RateLimitRPS: 20, RateLimitBurst: 60}
	_, _, err := newServer(cfg, logging.DefaultLogger())
	require.Error(t, err)
}

func TestNewServer_DerivesBaseURLFromListenWhenUnset(t *testing.T) {
	root := buildCatalogFixture(t)
	cfg := config{Listen: ":9999", ArtifactDir: root, LogLevel: "info", RateLimitRPS: 20, RateLimitBurst: 60}
	srv, cleanup, err := newServer(cfg, logging.DefaultLogger())
	require.NoError(t, err)
	defer cleanup()
	require.Equal(t, ":9999", srv.Addr)
}
