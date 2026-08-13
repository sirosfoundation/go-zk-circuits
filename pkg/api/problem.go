package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Problem is an RFC 9457 application/problem+json error body (spec §3.8).
// Clients MUST treat the HTTP status code as authoritative and this body as
// advisory-only — the static-host hosting option (spec §4.2) can never
// produce this shape, only whatever the bucket/CDN returns for a miss.
type Problem struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
}

const problemContentType = "application/problem+json"

// writeProblem writes an RFC 9457 error body and aborts the gin context.
func writeProblem(c *gin.Context, baseURL string, status int, problemSlug, title, detail string) {
	p := Problem{
		Type:     baseURL + "/problems/" + problemSlug,
		Title:    title,
		Status:   status,
		Detail:   detail,
		Instance: c.Request.URL.Path,
	}
	c.Header("Content-Type", problemContentType)
	c.AbortWithStatusJSON(status, p)
}

func notFound(c *gin.Context, baseURL, slug, title, detail string) {
	writeProblem(c, baseURL, http.StatusNotFound, slug, title, detail)
}

func badRequest(c *gin.Context, baseURL, slug, title, detail string) {
	writeProblem(c, baseURL, http.StatusBadRequest, slug, title, detail)
}
