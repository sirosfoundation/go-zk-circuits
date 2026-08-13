package api

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/sirosfoundation/go-zk-circuits/pkg/catalog"
)

// idPathPattern mirrors catalog's id charset (spec §2.3): a non-conforming id is a 400, not a 404.
var idPathPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// CircuitHandler serves GET/HEAD /v1/circuits/{id}.json (spec §3.4).
// An alias resolves via a 301 to the canonical path; a client MUST follow
// up to 3 redirects and MUST treat a 200 at an alias path as authoritative
// too (relevant for the static-hosting option, which duplicates the file
// instead of redirecting — this Go implementation always redirects).
// @Summary Single circuit descriptor
// @Tags Circuits
// @Produce json
// @Param id path string true "circuit id or alias"
// @Success 200 {object} catalog.CircuitDescriptor
// @Success 301
// @Failure 400 {object} Problem
// @Failure 404 {object} Problem
// @Router /v1/circuits/{id}.json [get]
func CircuitHandler(serverCtx *ServerContext) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Gin's router matches a full path segment as one param — it does
		// NOT support mixing a literal suffix into the same segment as a
		// param (confirmed experimentally: "/circuits/:id.json" matches but
		// captures an empty id). So the route is registered as
		// "/circuits/:idfile" and the ".json" suffix is stripped here.
		idfile := c.Param("idfile")
		id, ok := strings.CutSuffix(idfile, ".json")
		if !ok {
			notFound(c, serverCtx.BaseURL, "circuit-not-found", "Circuit not found", "Path must end in .json")
			return
		}
		if !idPathPattern.MatchString(id) {
			badRequest(c, serverCtx.BaseURL, "invalid-id", "Invalid id", "id must match "+idPathPattern.String())
			return
		}

		_, _, byID, aliasToID, entryRaw, entryETag, _ := serverCtx.snapshot()

		if canonical, isAlias := aliasToID[id]; isAlias {
			c.Header("Location", "/v1/circuits/"+canonical+".json")
			c.Status(http.StatusMovedPermanently)
			return
		}

		entry, ok := byID[id]
		if !ok {
			notFound(c, serverCtx.BaseURL, "circuit-not-found", "Circuit not found", "No circuit with id \""+id+"\" is present in this catalog.")
			return
		}

		var lastMod time.Time
		if t, err := time.Parse(time.RFC3339, entry.PublishedAt); err == nil {
			lastMod = t
		}
		serveJSONWithCaching(c, entryRaw[id], entryETag[id], "public, max-age=3600", lastMod)
	}
}

// circuitsFilterParams are all optional and AND-combined (spec §3.5).
// Unknown query parameters MUST be ignored — a client filters locally
// anyway, so fail-open here is correct, not a compromise.
func matchesFilter(e *catalog.CircuitDescriptor, c *gin.Context) bool {
	if v := c.Query("system"); v != "" && e.System != v {
		return false
	}
	if v := c.Query("version"); v != "" && e.SystemVersion != v {
		return false
	}
	if v := c.Query("numAttributes"); v != "" {
		got, ok := e.Params["num_attributes"]
		if !ok {
			return false
		}
		f, isFloat := got.(float64)
		if !isFloat || strconv.FormatInt(int64(f), 10) != v {
			return false
		}
	}
	if v := c.Query("docType"); v != "" {
		found := false
		for _, dt := range e.DocTypes {
			if dt == v {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	status := c.Query("status")
	if status == "" {
		status = catalog.StatusActive // default per spec §3.5
	}
	if status != "all" && e.Status != status {
		return false
	}
	return true
}

// CircuitsFilterHandler serves the optional GET /v1/circuits?... convenience
// endpoint (spec §3.5). No client may depend on it — it exists for humans
// and curl-driven debugging, and is the first thing to cut under time pressure.
// @Summary Filtered circuit listing (optional convenience endpoint)
// @Tags Circuits
// @Produce json
// @Param system query string false "exact match on system"
// @Param version query string false "exact match on systemVersion"
// @Param numAttributes query string false "exact match on params.num_attributes"
// @Param docType query string false "membership in docTypes"
// @Param status query string false "exact match on status; default active; status=all returns everything"
// @Success 200 {object} catalog.Manifest
// @Router /v1/circuits [get]
func CircuitsFilterHandler(serverCtx *ServerContext) gin.HandlerFunc {
	return func(c *gin.Context) {
		_, _, byID, _, _, _, generatedAt := serverCtx.snapshot()

		var filtered []catalog.CircuitDescriptor
		for _, e := range byID {
			if matchesFilter(e, c) {
				filtered = append(filtered, *e)
			}
		}
		m := catalog.BuildManifest(filtered, generatedAt)
		body, err := catalog.MarshalDeterministic(m)
		if err != nil {
			writeProblem(c, serverCtx.BaseURL, http.StatusInternalServerError, "internal-error", "Internal error", "")
			return
		}
		c.Header("Content-Type", "application/json")
		c.Status(http.StatusOK)
		if c.Request.Method != http.MethodHead {
			_, _ = c.Writer.Write(body)
		}
	}
}
