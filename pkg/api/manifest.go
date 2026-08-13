package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// serveJSONWithCaching implements the shared caching contract from spec
// §3.3/§3.4: strong ETag, conditional 304 via If-None-Match, HEAD support
// with no body but identical headers.
func serveJSONWithCaching(c *gin.Context, body []byte, etag, cacheControl string, lastModified time.Time) {
	c.Header("ETag", etag)
	c.Header("Cache-Control", cacheControl)
	if !lastModified.IsZero() {
		c.Header("Last-Modified", lastModified.UTC().Format(http.TimeFormat))
	}
	if match := c.GetHeader("If-None-Match"); match != "" && match == etag {
		c.Status(http.StatusNotModified)
		return
	}
	c.Header("Content-Type", "application/json")
	c.Header("Content-Length", strconv.Itoa(len(body)))
	c.Status(http.StatusOK)
	if c.Request.Method != http.MethodHead {
		_, _ = c.Writer.Write(body)
	}
}

// ManifestHandler serves GET/HEAD /v1/manifest.json (spec §3.3).
// @Summary Full circuit catalog manifest
// @Tags Manifest
// @Produce json
// @Success 200 {object} catalog.Manifest
// @Success 304
// @Router /v1/manifest.json [get]
func ManifestHandler(serverCtx *ServerContext) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, etag, _, _, _, _, generatedAt := serverCtx.snapshot()
		if raw == nil {
			writeProblem(c, serverCtx.BaseURL, http.StatusServiceUnavailable, "catalog-not-loaded", "Catalog not loaded", "The service has not finished loading its catalog.")
			if serverCtx.Metrics != nil {
				serverCtx.Metrics.ManifestRequestsTotal.WithLabelValues(strconv.Itoa(c.Writer.Status())).Inc()
			}
			return
		}
		c.Header("X-Catalog-Generated-At", generatedAt)
		var lastMod time.Time
		if t, err := time.Parse(time.RFC3339, generatedAt); err == nil {
			lastMod = t
		}
		serveJSONWithCaching(c, raw, etag, "public, max-age=300, stale-while-revalidate=86400", lastMod)
		if serverCtx.Metrics != nil {
			serverCtx.Metrics.ManifestRequestsTotal.WithLabelValues(strconv.Itoa(c.Writer.Status())).Inc()
		}
	}
}
