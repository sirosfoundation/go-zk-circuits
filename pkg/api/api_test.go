package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
	"github.com/sirosfoundation/g119612/pkg/logging"
	"github.com/stretchr/testify/require"

	"github.com/sirosfoundation/go-zk-circuits/pkg/catalog"
	"github.com/sirosfoundation/go-zk-circuits/pkg/publish"
)

// newTestServer builds a real gin.Engine backed by a populated temp
// catalog, so these tests exercise the same code path a deployed instance
// would (spec §3's curl examples), not a mock.
func newTestServer(t *testing.T) (*gin.Engine, *ServerContext, string) {
	t.Helper()
	root := t.TempDir()

	enc, err := zstd.NewWriter(nil)
	require.NoError(t, err)
	raw := []byte("fake circuit bytes for api_test")
	compressed := enc.EncodeAll(raw, nil)
	require.NoError(t, enc.Close())
	inputDir := t.TempDir()
	filename := "8_2_4307_2945_deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	inputFile := filepath.Join(inputDir, filename)
	require.NoError(t, os.WriteFile(inputFile, compressed, 0o600))

	_, err = publish.Add(root, publish.AddOptions{
		InputFile: inputFile, System: "longfellow", Origin: "o", AddedBy: "a",
		Aliases: []string{"longfellow-8-2"},
	})
	require.NoError(t, err)
	require.NoError(t, publish.RegenerateManifest(root, "2026-08-13T21:40:11Z"))

	gin.SetMode(gin.TestMode)
	serverCtx := NewServerContext(logging.DefaultLogger(), os.DirFS(root), "https://zk-circuits.test")
	require.NoError(t, serverCtx.LoadCatalog())

	r := gin.New()
	RegisterAPIRoutes(r, serverCtx)
	return r, serverCtx, root
}

func TestHealthz(t *testing.T) {
	r, _, _ := newTestServer(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	require.Equal(t, http.StatusOK, w.Code)
}

func TestReadyz_ReadyOnceCatalogLoaded(t *testing.T) {
	r, _, _ := newTestServer(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusOK, w.Code)
}

func TestReadyz_NotReadyBeforeLoad(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "catalog", "circuits"), 0o750))
	gin.SetMode(gin.TestMode)
	serverCtx := NewServerContext(logging.DefaultLogger(), os.DirFS(root), "https://zk-circuits.test")
	// Deliberately NOT calling LoadCatalog().
	r := gin.New()
	RegisterAPIRoutes(r, serverCtx)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)

	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/v1/manifest.json", nil))
	require.Equal(t, http.StatusServiceUnavailable, w2.Code)
	require.Equal(t, problemContentType, w2.Header().Get("Content-Type"))
}

func TestManifest_FetchAndConditionalRevalidate(t *testing.T) {
	r, _, _ := newTestServer(t)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/manifest.json", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "application/json", w.Header().Get("Content-Type"))
	etag := w.Header().Get("ETag")
	require.NotEmpty(t, etag)
	require.Contains(t, w.Header().Get("Cache-Control"), "max-age=300")
	require.NotEmpty(t, w.Header().Get("X-Catalog-Generated-At"))
	require.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"), "circuits.siros.org must be able to fetch this cross-origin")

	var m catalog.Manifest
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
	require.Len(t, m.Circuits, 1)
	require.Nil(t, m.Next)

	// Conditional revalidation — the normal client path after first fetch (spec §3.3).
	req := httptest.NewRequest(http.MethodGet, "/v1/manifest.json", nil)
	req.Header.Set("If-None-Match", etag)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req)
	require.Equal(t, http.StatusNotModified, w2.Code)
	require.Empty(t, w2.Body.Bytes())
}

func TestManifest_HeadHasNoBody(t *testing.T) {
	r, _, _ := newTestServer(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodHead, "/v1/manifest.json", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Empty(t, w.Body.Bytes())
	require.NotEmpty(t, w.Header().Get("Content-Length"))
}

func TestCircuit_ByCanonicalID(t *testing.T) {
	r, _, _ := newTestServer(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/circuits/longfellow-libzk-v1_8_2_4307_2945.json", nil))
	require.Equal(t, http.StatusOK, w.Code)

	var entry catalog.CircuitDescriptor
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entry))
	require.Equal(t, "longfellow-libzk-v1_8_2_4307_2945", entry.ID)
	// Bare object, not wrapped in a manifest envelope (spec §3.4).
	require.False(t, jsonHasKey(w.Body.Bytes(), "circuits"))
}

func TestCircuit_AliasRedirects(t *testing.T) {
	r, _, _ := newTestServer(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/circuits/longfellow-8-2.json", nil))
	require.Equal(t, http.StatusMovedPermanently, w.Code)
	require.Equal(t, "/v1/circuits/longfellow-libzk-v1_8_2_4307_2945.json", w.Header().Get("Location"))
}

func TestCircuit_UnknownIDIs404WithProblemBody(t *testing.T) {
	r, _, _ := newTestServer(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/circuits/does-not-exist.json", nil))
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Equal(t, problemContentType, w.Header().Get("Content-Type"))
	var p Problem
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p))
	require.Equal(t, 404, p.Status)
}

func TestCircuit_InvalidIDCharsetIs400(t *testing.T) {
	r, _, _ := newTestServer(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/circuits/has%20space.json", nil))
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCircuitsFilter_DefaultsToActiveOnly(t *testing.T) {
	r, serverCtx, root := newTestServer(t)
	require.NoError(t, publish.Deprecate(root, "longfellow-libzk-v1_8_2_4307_2945", "test"))
	require.NoError(t, publish.RegenerateManifest(root, "2026-08-13T21:40:11Z"))
	require.NoError(t, serverCtx.LoadCatalog())

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/circuits", nil))
	var m catalog.Manifest
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
	require.Empty(t, m.Circuits, "deprecated entries must not show by default")

	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/v1/circuits?status=all", nil))
	var m2 catalog.Manifest
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &m2))
	require.Len(t, m2.Circuits, 1)
}

func TestCircuitsFilter_UnknownParamIsIgnoredNotRejected(t *testing.T) {
	r, _, _ := newTestServer(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/circuits?someFutureFilter=x", nil))
	require.Equal(t, http.StatusOK, w.Code)
}

func TestArtifact_DownloadAndVerify(t *testing.T) {
	r, _, root := newTestServer(t)
	entries, err := publish.LoadCatalogEntries(root)
	require.NoError(t, err)
	hexDigest := entries[0].Artifact.Hash[len("sha256:"):]

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/artifacts/sha256/"+hexDigest, nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "application/zstd", w.Header().Get("Content-Type"))
	require.Equal(t, `"sha256:`+hexDigest+`"`, w.Header().Get("ETag"))
	require.Contains(t, w.Header().Get("Cache-Control"), "immutable")
	require.Equal(t, hexDigest, w.Header().Get("X-Content-SHA256"))
	require.Equal(t, publish.HashHex(w.Body.Bytes()), hexDigest)
}

func TestArtifact_RangeRequest(t *testing.T) {
	r, _, root := newTestServer(t)
	entries, err := publish.LoadCatalogEntries(root)
	require.NoError(t, err)
	hexDigest := entries[0].Artifact.Hash[len("sha256:"):]

	req := httptest.NewRequest(http.MethodGet, "/v1/artifacts/sha256/"+hexDigest, nil)
	req.Header.Set("Range", "bytes=0-3")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusPartialContent, w.Code)
	require.Equal(t, 4, w.Body.Len())
}

func TestUnpublishedEntry_FullyInvisible(t *testing.T) {
	root := t.TempDir()

	enc, err := zstd.NewWriter(nil)
	require.NoError(t, err)
	defer func() { _ = enc.Close() }()

	// A published circuit (2 attributes) and an unpublished one (3
	// attributes, distinct id) in the same catalog — the published one
	// must keep working normally while the unpublished one is invisible
	// end to end, including its raw bytes at the content-addressed path.
	publishedFile := filepath.Join(t.TempDir(), "8_2_4307_2945_deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	require.NoError(t, os.WriteFile(publishedFile, enc.EncodeAll([]byte("published circuit bytes"), nil), 0o600))
	_, err = publish.Add(root, publish.AddOptions{InputFile: publishedFile, System: "longfellow", Origin: "o", AddedBy: "a"})
	require.NoError(t, err)

	unpublishedFile := filepath.Join(t.TempDir(), "8_3_4463_2945_deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	require.NoError(t, os.WriteFile(unpublishedFile, enc.EncodeAll([]byte("unpublished circuit bytes"), nil), 0o600))
	unpubResult, err := publish.Add(root, publish.AddOptions{
		InputFile: unpublishedFile, System: "longfellow", Origin: "o", AddedBy: "a", Unpublished: true,
	})
	require.NoError(t, err)
	unpubHex := unpubResult.Entry.Artifact.Hash[len("sha256:"):]

	require.NoError(t, publish.RegenerateManifest(root, "2026-08-13T21:40:11Z"))

	gin.SetMode(gin.TestMode)
	serverCtx := NewServerContext(logging.DefaultLogger(), os.DirFS(root), "https://zk-circuits.test")
	require.NoError(t, serverCtx.LoadCatalog())
	r := gin.New()
	RegisterAPIRoutes(r, serverCtx)

	// Absent from the manifest entirely.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/manifest.json", nil))
	var m catalog.Manifest
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
	require.Len(t, m.Circuits, 1, "only the published entry should appear")
	require.Equal(t, "longfellow-libzk-v1_8_2_4307_2945", m.Circuits[0].ID)

	// Its own per-entry lookup 404s, exactly like an id that never existed.
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/v1/circuits/"+unpubResult.Entry.ID+".json", nil))
	require.Equal(t, http.StatusNotFound, w2.Code)

	// Its bytes are actually present on disk (go:embed would have compiled
	// them in for real) — but MUST still be unreachable via the
	// content-addressed endpoint, precisely because that's the loophole an
	// unpublished flag would be pointless without closing.
	_, statErr := os.Stat(filepath.Join(root, "artifacts", "sha256", unpubHex))
	require.NoError(t, statErr, "sanity check: the bytes really are on disk")

	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, httptest.NewRequest(http.MethodGet, "/v1/artifacts/sha256/"+unpubHex, nil))
	require.Equal(t, http.StatusNotFound, w3.Code, "unpublished artifact bytes must not be downloadable even though they exist on disk")
	require.Equal(t, problemContentType, w3.Header().Get("Content-Type"))

	// The published entry's own artifact must be entirely unaffected.
	pubHex := m.Circuits[0].Artifact.Hash[len("sha256:"):]
	w4 := httptest.NewRecorder()
	r.ServeHTTP(w4, httptest.NewRequest(http.MethodGet, "/v1/artifacts/sha256/"+pubHex, nil))
	require.Equal(t, http.StatusOK, w4.Code)
}

func TestArtifact_UnknownHashIs404(t *testing.T) {
	r, _, _ := newTestServer(t)
	w := httptest.NewRecorder()
	unknown := "0000000000000000000000000000000000000000000000000000000000000000"[:64]
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/artifacts/sha256/"+unknown, nil))
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Equal(t, problemContentType, w.Header().Get("Content-Type"))
}

func TestArtifact_MalformedHexIs400(t *testing.T) {
	r, _, _ := newTestServer(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/artifacts/sha256/not-hex", nil))
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestArtifact_UnsupportedAlgorithmIs400(t *testing.T) {
	r, _, _ := newTestServer(t)
	w := httptest.NewRecorder()
	hex64 := "1111111111111111111111111111111111111111111111111111111111111111"[:64]
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/artifacts/sha384/"+hex64, nil))
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUnsupportedMethodIs405WithAllowHeader(t *testing.T) {
	r, _, _ := newTestServer(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/manifest.json", nil))
	require.Equal(t, http.StatusMethodNotAllowed, w.Code)
	require.Equal(t, "GET, HEAD", w.Header().Get("Allow"))
}

func TestRateLimiter_RejectsOverBudget(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "catalog", "circuits"), 0o750))
	require.NoError(t, publish.RegenerateManifest(root, "2026-08-13T21:40:11Z"))

	gin.SetMode(gin.TestMode)
	serverCtx := NewServerContext(logging.DefaultLogger(), os.DirFS(root), "https://zk-circuits.test")
	require.NoError(t, serverCtx.LoadCatalog())
	serverCtx.RateLimiter = NewRateLimiter(1, 1)

	r := gin.New()
	RegisterAPIRoutes(r, serverCtx)

	var lastStatus int
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/manifest.json", nil))
		lastStatus = w.Code
		if lastStatus == http.StatusTooManyRequests {
			break
		}
	}
	require.Equal(t, http.StatusTooManyRequests, lastStatus)

	// /healthz and /readyz must never be rate limited (spec §8.5).
	for i := 0; i < 10; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		require.Equal(t, http.StatusOK, w.Code)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "catalog", "circuits"), 0o750))
	require.NoError(t, publish.RegenerateManifest(root, "2026-08-13T21:40:11Z"))

	gin.SetMode(gin.TestMode)
	serverCtx := NewServerContext(logging.DefaultLogger(), os.DirFS(root), "https://zk-circuits.test")
	require.NoError(t, serverCtx.LoadCatalog())
	serverCtx.Metrics = NewMetrics()

	r := gin.New()
	RegisterMetricsEndpoint(r, serverCtx.Metrics)
	RegisterAPIRoutes(r, serverCtx)

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/manifest.json", nil))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "zkc_manifest_requests_total")
}

func jsonHasKey(body []byte, key string) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil {
		return false
	}
	_, ok := m[key]
	return ok
}
