//go:build integration

// Real-process integration tests (spec §10 Phase 1 exit criterion: "the
// deployed service passes every curl in §3"). Unlike pkg/api's in-process
// gin.Engine tests, this builds and runs the actual zkc binary as a
// separate process, over a real TCP listener, exercising main.go's own env
// var wiring (ZKC_LISTEN, ZKC_ARTIFACT_DIR) and graceful shutdown — the
// parts pkg/api's tests structurally cannot reach.
package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"

	"github.com/sirosfoundation/go-zk-circuits/pkg/publish"
)

type testServer struct {
	baseURL string
	cmd     *exec.Cmd
}

func startTestServer(t *testing.T) *testServer {
	t.Helper()

	// Build a small real catalog on disk — hermetic, doesn't depend on
	// whatever the embedded build happens to contain.
	root := t.TempDir()
	enc, err := zstd.NewWriter(nil)
	require.NoError(t, err)
	raw := []byte("fake circuit bytes for zkc integration test")
	compressed := enc.EncodeAll(raw, nil)
	require.NoError(t, enc.Close())
	inputDir := t.TempDir()
	inputFile := filepath.Join(inputDir, "8_2_4307_2945_deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	require.NoError(t, os.WriteFile(inputFile, compressed, 0o600)) //#nosec G304 -- fixed test fixture path under t.TempDir()

	_, err = publish.Add(root, publish.AddOptions{
		InputFile: inputFile, System: "longfellow", Origin: "o",
	})
	require.NoError(t, err)
	require.NoError(t, publish.RegenerateManifest(root, "2026-08-13T21:40:11Z"))

	port := freePort(t)
	baseURL := "http://127.0.0.1:" + strconv.Itoa(port)

	// Exec the built binary directly rather than "go run ." — go run spawns
	// the real binary as a grandchild, and this test's own SIGTERM to the
	// "go run" wrapper does not reliably propagate to it, leaving an
	// orphaned process holding stdout/stderr open (observed as "Test I/O
	// incomplete" / "WaitDelay expired" failures from the test binary).
	binPath := buildZkcBinary(t)
	cmd := exec.Command(binPath)
	cmd.Env = append(os.Environ(),
		"ZKC_LISTEN=:"+strconv.Itoa(port),
		"ZKC_ARTIFACT_DIR="+root,
		"ZKC_LOG_LEVEL=debug",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())

	waitForHealthy(t, baseURL)
	return &testServer{baseURL: baseURL, cmd: cmd}
}

func (s *testServer) stop(t *testing.T) {
	t.Helper()
	if s.cmd.Process == nil {
		return
	}
	_ = s.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- s.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Log("zkc did not exit within 5s of SIGTERM, killing")
		_ = s.cmd.Process.Kill()
		<-done
	}
}

func buildZkcBinary(t *testing.T) string {
	t.Helper()
	binPath := filepath.Join(t.TempDir(), "zkc")
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "failed to build zkc binary for integration test")
	return binPath
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func waitForHealthy(t *testing.T, baseURL string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/healthz") //#nosec G107 -- baseURL is built from a locally-chosen free port, not remote input
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("zkc did not become healthy in time")
}

func TestIntegration_ManifestAndArtifactRoundTrip(t *testing.T) {
	s := startTestServer(t)
	defer s.stop(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// GET /v1/manifest.json (spec §3.3)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/v1/manifest.json", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var manifest struct {
		Circuits []struct {
			ID       string `json:"id"`
			Artifact struct {
				Hash string `json:"hash"`
			} `json:"artifact"`
		} `json:"circuits"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&manifest))
	require.Len(t, manifest.Circuits, 1)
	hexDigest := manifest.Circuits[0].Artifact.Hash[len("sha256:"):]

	// GET /v1/circuits/{id}.json (spec §3.4)
	req2, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/v1/circuits/"+manifest.Circuits[0].ID+".json", nil)
	resp2, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	defer resp2.Body.Close()
	require.Equal(t, http.StatusOK, resp2.StatusCode)

	// GET /v1/artifacts/sha256/{hex} and verify the hash end to end (spec §3.6)
	req3, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/v1/artifacts/sha256/"+hexDigest, nil)
	resp3, err := http.DefaultClient.Do(req3)
	require.NoError(t, err)
	defer resp3.Body.Close()
	require.Equal(t, http.StatusOK, resp3.StatusCode)
	require.Contains(t, resp3.Header.Get("Cache-Control"), "immutable")

	body := make([]byte, 0)
	buf := make([]byte, 4096)
	for {
		n, err := resp3.Body.Read(buf)
		body = append(body, buf[:n]...)
		if err != nil {
			break
		}
	}
	require.Equal(t, hexDigest, publish.HashHex(body))

	// GET /readyz (spec §3.2)
	req4, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/readyz", nil)
	resp4, err := http.DefaultClient.Do(req4)
	require.NoError(t, err)
	defer resp4.Body.Close()
	require.Equal(t, http.StatusOK, resp4.StatusCode)

	// GET /metrics (spec §3.2, §4.6)
	req5, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/metrics", nil)
	resp5, err := http.DefaultClient.Do(req5)
	require.NoError(t, err)
	defer resp5.Body.Close()
	require.Equal(t, http.StatusOK, resp5.StatusCode)
}

func TestIntegration_GracefulShutdown(t *testing.T) {
	s := startTestServer(t)
	start := time.Now()
	s.stop(t)
	require.Less(t, time.Since(start), 5*time.Second, "SIGTERM should stop the server promptly")
}
