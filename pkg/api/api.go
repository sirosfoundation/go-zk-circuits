package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// InfoResponse is returned by GET /info — basic build/catalog metadata, not
// to be confused with go-trust's deprecated /info (registry listing); this
// one is fresh to this service and not deprecated.
type InfoResponse struct {
	Version      string `json:"version"`
	Catalog      string `json:"catalog"`
	CircuitCount int    `json:"circuitCount"`
	GeneratedAt  string `json:"generatedAt"`
}

// Version is set at build time via -ldflags, mirroring cmd/circuitctl and go-trust's convention.
var Version = "0.1.0-dev"

// InfoHandler serves GET /info.
// @Summary Build and catalog info
// @Tags Info
// @Produce json
// @Success 200 {object} InfoResponse
// @Router /info [get]
func InfoHandler(serverCtx *ServerContext) gin.HandlerFunc {
	return func(c *gin.Context) {
		_, _, byID, _, _, _, generatedAt := serverCtx.snapshot()
		c.JSON(http.StatusOK, InfoResponse{
			Version:      Version,
			Catalog:      "siros-zk-circuits",
			CircuitCount: len(byID),
			GeneratedAt:  generatedAt,
		})
	}
}

// RegisterAPIRoutes wires every route in spec §3.2 onto r. If serverCtx has
// a RateLimiter configured, it applies to all routes except /healthz and
// /readyz (spec §8.5: rate limiting MUST NOT apply to those).
func RegisterAPIRoutes(r *gin.Engine, serverCtx *ServerContext) {
	RegisterHealthEndpoints(r, serverCtx)

	if serverCtx.RateLimiter != nil {
		r.Use(func(c *gin.Context) {
			switch c.Request.URL.Path {
			case "/healthz", "/readyz":
				c.Next()
			default:
				serverCtx.RateLimiter.Middleware()(c)
			}
		})
		serverCtx.Logger.Info("Rate limiting enabled")
	}

	r.GET("/info", InfoHandler(serverCtx))

	v1 := r.Group("/v1")
	v1.Use(corsMiddleware())
	{
		v1.GET("/manifest.json", ManifestHandler(serverCtx))
		v1.HEAD("/manifest.json", ManifestHandler(serverCtx))

		v1.GET("/circuits/:idfile", CircuitHandler(serverCtx))
		v1.HEAD("/circuits/:idfile", CircuitHandler(serverCtx))

		// Optional convenience endpoint (spec §3.5) — no client may depend on it.
		v1.GET("/circuits", CircuitsFilterHandler(serverCtx))
		v1.HEAD("/circuits", CircuitsFilterHandler(serverCtx))

		v1.GET("/artifacts/:alg/:hex", ArtifactHandler(serverCtx))
		v1.HEAD("/artifacts/:alg/:hex", ArtifactHandler(serverCtx))
	}

	// Only GET/HEAD are supported on the read API (spec §3.2): everything
	// else on a known path is 405 with an Allow header, not 404.
	r.HandleMethodNotAllowed = true
	r.NoMethod(func(c *gin.Context) {
		c.Header("Allow", "GET, HEAD")
		writeProblem(c, serverCtx.BaseURL, http.StatusMethodNotAllowed, "method-not-allowed", "Method not allowed", "This endpoint only supports GET and HEAD.")
	})
}
