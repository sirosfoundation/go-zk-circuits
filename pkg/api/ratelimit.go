package api

// Per-IP token-bucket rate limiting, copied from go-trust's pkg/api/ratelimit.go
// (spec Appendix C: "confirmed real ... copy that shape").

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// RateLimiter provides per-IP rate limiting for API endpoints, using the
// token bucket algorithm from golang.org/x/time/rate (spec §8.5).
type RateLimiter struct {
	limiters   map[string]*rate.Limiter
	lastAccess map[string]time.Time
	mu         sync.RWMutex
	rps        int
	burst      int
}

// NewRateLimiter creates a limiter allowing rps requests/sec per IP, with bursts up to burst.
func NewRateLimiter(rps, burst int) *RateLimiter {
	return &RateLimiter{
		limiters:   make(map[string]*rate.Limiter),
		lastAccess: make(map[string]time.Time),
		rps:        rps,
		burst:      burst,
	}
}

func (rl *RateLimiter) getLimiter(ip string) *rate.Limiter {
	rl.mu.RLock()
	limiter, exists := rl.limiters[ip]
	rl.mu.RUnlock()
	if exists {
		rl.mu.Lock()
		rl.lastAccess[ip] = time.Now()
		rl.mu.Unlock()
		return limiter
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()
	if limiter, exists := rl.limiters[ip]; exists {
		rl.lastAccess[ip] = time.Now()
		return limiter
	}
	limiter = rate.NewLimiter(rate.Limit(rl.rps), rl.burst)
	rl.limiters[ip] = limiter
	rl.lastAccess[ip] = time.Now()
	return limiter
}

// Middleware enforces the per-IP rate limit. A rejected request gets 429
// with Retry-After (spec §8.5) — never treated as an integrity failure by
// a conforming client.
func (rl *RateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !rl.getLimiter(c.ClientIP()).Allow() {
			c.Header("Retry-After", "1")
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			c.Abort()
			return
		}
		c.Next()
	}
}

// CleanupOldLimiters removes limiters for IPs idle longer than maxAge, so the map doesn't grow unbounded.
func (rl *RateLimiter) CleanupOldLimiters(maxAge time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	cutoff := time.Now().Add(-maxAge)
	for ip, lastSeen := range rl.lastAccess {
		if lastSeen.Before(cutoff) {
			delete(rl.limiters, ip)
			delete(rl.lastAccess, ip)
		}
	}
}

// StartCleanupLoop runs CleanupOldLimiters periodically until done is closed.
func (rl *RateLimiter) StartCleanupLoop(interval, maxAge time.Duration, done <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				rl.CleanupOldLimiters(maxAge)
			case <-done:
				return
			}
		}
	}()
}
