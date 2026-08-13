package api

import "github.com/gin-gonic/gin"

// corsMiddleware adds a permissive Access-Control-Allow-Origin to the
// public read API (spec §3.2's /v1/* routes only — not /healthz, /readyz,
// /metrics, which no browser page needs to fetch cross-origin). This is
// what lets circuits.siros.org (spec §1.6) fetch the manifest client-side
// from api.circuits.siros.org.
//
// No OPTIONS/preflight handling: a plain `fetch(url)` with no custom
// headers is a CORS "simple request" for GET, so browsers never send a
// preflight for this API's actual use case — an OPTIONS route would be
// unreachable dead code here anyway, since unregistered methods on a
// matched path fall through to the router-level NoMethod handler (spec
// §3.2's 405), not this route group's middleware chain.
//
// Safe to be wide open: every /v1/* response is public, read-only, and
// carries no cookies/auth (spec §8.2, §8.4) — there is no session or
// secret a malicious origin could exfiltrate by reading these responses.
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, HEAD")
		c.Next()
	}
}
