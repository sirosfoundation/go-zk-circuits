package api

import (
	"bytes"
	"io"
	"net/http"
	"regexp"
	"strconv"

	"github.com/gin-gonic/gin"
)

// artifactPathPattern matches {alg}/{hex}: sha256 is the only algorithm for
// v1 (spec §3.6); the path segment exists so sha384/blake3 could be added
// later without a new URL shape.
var artifactPathPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ArtifactHandler serves GET/HEAD /v1/artifacts/{alg}/{hex} (spec §3.6): the
// content-addressed download. This has no precedent to copy from go-trust
// (spec Appendix C) — it is the one handler in this service actually
// serving large immutable blobs, via net/http's ServeContent for correct
// ETag/Range/304/HEAD handling essentially for free.
// @Summary Content-addressed artifact download
// @Tags Artifacts
// @Produce application/octet-stream
// @Param alg path string true "hash algorithm, currently only sha256"
// @Param hex path string true "lowercase hex digest, 64 chars for sha256"
// @Success 200
// @Success 206
// @Success 304
// @Failure 400 {object} Problem
// @Failure 404 {object} Problem
// @Failure 416 {object} Problem
// @Router /v1/artifacts/{alg}/{hex} [get]
func ArtifactHandler(serverCtx *ServerContext) gin.HandlerFunc {
	return func(c *gin.Context) {
		alg := c.Param("alg")
		hexDigest := c.Param("hex")

		if alg != "sha256" {
			badRequest(c, serverCtx.BaseURL, "unsupported-algorithm", "Unsupported algorithm", "only sha256 is supported in /v1")
			recordArtifactMetric(serverCtx, hexDigest, c)
			return
		}
		if !artifactPathPattern.MatchString(hexDigest) {
			badRequest(c, serverCtx.BaseURL, "invalid-digest", "Invalid digest", "hex must be 64 lowercase hex characters")
			recordArtifactMetric(serverCtx, hexDigest, c)
			return
		}

		// byID is built from the currently loaded manifest, which
		// RegenerateManifest already filters to published=true entries
		// (spec §2.4.1) — so "not referenced by any entry here" covers both
		// "genuinely unknown hash" and "known but unpublished" identically.
		// Without this check, an unpublished entry's bytes would still be
		// fetchable by anyone who already knew or guessed the hash, even
		// though the entry itself is invisible everywhere else — the whole
		// point of "unpublished" is that the bytes aren't reachable either.
		_, _, byID, _, _, _, _ := serverCtx.snapshot()
		var mediaType string
		found := false
		for _, e := range byID {
			if e.Artifact != nil && e.Artifact.Hash == "sha256:"+hexDigest {
				mediaType = e.Artifact.MediaType
				found = true
				break
			}
		}
		if !found {
			notFound(c, serverCtx.BaseURL, "artifact-not-found", "Artifact not found", "No artifact with sha256:"+hexDigest+" is present in this catalog.")
			recordArtifactMetric(serverCtx, hexDigest, c)
			return
		}

		artifactPath := "artifacts/sha256/" + hexDigest
		f, err := serverCtx.FS.Open(artifactPath)
		if err != nil {
			notFound(c, serverCtx.BaseURL, "artifact-not-found", "Artifact not found", "No artifact with sha256:"+hexDigest+" is present in this catalog.")
			recordArtifactMetric(serverCtx, hexDigest, c)
			return
		}
		defer func() { _ = f.Close() }()

		info, err := f.Stat()
		if err != nil {
			writeProblem(c, serverCtx.BaseURL, http.StatusInternalServerError, "internal-error", "Internal error", "")
			recordArtifactMetric(serverCtx, hexDigest, c)
			return
		}

		var content io.ReadSeeker
		if seeker, ok := f.(io.ReadSeeker); ok {
			content = seeker
		} else {
			data, err := io.ReadAll(f)
			if err != nil {
				writeProblem(c, serverCtx.BaseURL, http.StatusInternalServerError, "internal-error", "Internal error", "")
				recordArtifactMetric(serverCtx, hexDigest, c)
				return
			}
			content = bytes.NewReader(data)
		}

		c.Header("Content-Type", mediaType)
		c.Header("ETag", `"sha256:`+hexDigest+`"`)
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
		c.Header("Accept-Ranges", "bytes")
		c.Header("X-Content-SHA256", hexDigest)

		http.ServeContent(c.Writer, c.Request, info.Name(), info.ModTime(), content)
		if serverCtx.Metrics != nil {
			serverCtx.Metrics.BytesServedTotal.Add(float64(info.Size()))
		}
		recordArtifactMetric(serverCtx, hexDigest, c)
	}
}

func recordArtifactMetric(serverCtx *ServerContext, hexDigest string, c *gin.Context) {
	if serverCtx.Metrics == nil {
		return
	}
	serverCtx.Metrics.ArtifactRequestsTotal.WithLabelValues(hexDigest, strconv.Itoa(c.Writer.Status())).Inc()
}
