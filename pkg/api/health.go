package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/sirosfoundation/go-zk-circuits/pkg/catalog"
)

// HealthResponse is returned by GET /healthz.
type HealthResponse struct {
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
}

// ReadinessResponse is returned by GET /readyz.
type ReadinessResponse struct {
	Status        string    `json:"status"`
	Timestamp     time.Time `json:"timestamp"`
	CatalogLoaded bool      `json:"catalogLoaded"`
	CircuitCount  int       `json:"circuitCount"`
	Message       string    `json:"message,omitempty"`
}

// RegisterHealthEndpoints registers /healthz and /readyz (spec §3.2's
// "Operational" endpoints), matching the org's go-trust convention.
func RegisterHealthEndpoints(r gin.IRouter, serverCtx *ServerContext) {
	r.GET("/healthz", HealthHandler(serverCtx))
	r.GET("/readyz", ReadinessHandler(serverCtx))
}

// HealthHandler is a pure liveness probe: 200 if the process can handle requests at all.
// @Summary Liveness check
// @Tags Health
// @Produce json
// @Success 200 {object} HealthResponse
// @Router /healthz [get]
func HealthHandler(serverCtx *ServerContext) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, HealthResponse{Status: "ok", Timestamp: time.Now()})
	}
}

// ReadinessHandler reports 200 only once a valid catalog has been loaded —
// serving stale/no data would mean every client request 404s or 500s, which is not "ready".
// @Summary Readiness check
// @Tags Health
// @Produce json
// @Success 200 {object} ReadinessResponse
// @Failure 503 {object} ReadinessResponse
// @Router /readyz [get]
func ReadinessHandler(serverCtx *ServerContext) gin.HandlerFunc {
	return func(c *gin.Context) {
		manifestRaw, _, byID, _, _, _, _ := serverCtx.snapshot()
		resp := ReadinessResponse{
			Timestamp:     time.Now(),
			CatalogLoaded: manifestRaw != nil,
			CircuitCount:  len(byID),
		}
		if manifestRaw == nil {
			resp.Status = "not_ready"
			resp.Message = "catalog not loaded"
			c.JSON(http.StatusServiceUnavailable, resp)
			return
		}
		if missing := firstUnreachableArtifact(serverCtx, byID); missing != "" {
			resp.Status = "not_ready"
			resp.Message = "artifact referenced by manifest is not reachable: " + missing
			c.JSON(http.StatusServiceUnavailable, resp)
			return
		}
		resp.Status = "ready"
		c.JSON(http.StatusOK, resp)
	}
}

// firstUnreachableArtifact opens (not just stats) every artifact referenced
// by the currently loaded manifest, returning the first hash that isn't
// actually servable. A manifest can be well-formed and fully loaded while
// still referencing a hash whose blob never made it into the image (a build
// bug, not a data bug) — /readyz should catch that before traffic does,
// rather than only confirming a manifest object exists in memory.
func firstUnreachableArtifact(serverCtx *ServerContext, byID map[string]*catalog.CircuitDescriptor) string {
	for _, e := range byID {
		if e.Artifact == nil {
			continue
		}
		hexDigest, ok := strings.CutPrefix(e.Artifact.Hash, "sha256:")
		if !ok {
			continue
		}
		f, err := serverCtx.FS.Open("artifacts/sha256/" + hexDigest)
		if err != nil {
			return e.Artifact.Hash
		}
		_ = f.Close()
	}
	return ""
}
