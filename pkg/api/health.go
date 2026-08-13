package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
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
		ready := manifestRaw != nil
		resp := ReadinessResponse{
			Timestamp:     time.Now(),
			CatalogLoaded: ready,
			CircuitCount:  len(byID),
		}
		if ready {
			resp.Status = "ready"
			c.JSON(http.StatusOK, resp)
		} else {
			resp.Status = "not_ready"
			resp.Message = "catalog not loaded"
			c.JSON(http.StatusServiceUnavailable, resp)
		}
	}
}
