package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRateLimiter_CleanupOldLimitersRemovesIdleEntries(t *testing.T) {
	rl := NewRateLimiter(20, 60)
	rl.getLimiter("1.2.3.4")
	rl.getLimiter("5.6.7.8")
	require.Len(t, rl.limiters, 2)

	// Backdate one IP's last access so it looks idle past maxAge.
	rl.mu.Lock()
	rl.lastAccess["1.2.3.4"] = time.Now().Add(-time.Hour)
	rl.mu.Unlock()

	rl.CleanupOldLimiters(time.Minute)

	rl.mu.RLock()
	defer rl.mu.RUnlock()
	require.NotContains(t, rl.limiters, "1.2.3.4")
	require.Contains(t, rl.limiters, "5.6.7.8")
}

func TestRateLimiter_GetLimiterReusesExistingEntry(t *testing.T) {
	rl := NewRateLimiter(20, 60)
	first := rl.getLimiter("1.2.3.4")
	second := rl.getLimiter("1.2.3.4")
	require.Same(t, first, second, "the same IP must reuse its existing limiter, not get a fresh bucket")
}

func TestRateLimiter_StartCleanupLoopRunsAndStops(t *testing.T) {
	rl := NewRateLimiter(20, 60)
	rl.getLimiter("1.2.3.4")
	rl.mu.Lock()
	rl.lastAccess["1.2.3.4"] = time.Now().Add(-time.Hour)
	rl.mu.Unlock()

	done := make(chan struct{})
	rl.StartCleanupLoop(10*time.Millisecond, time.Minute, done)

	require.Eventually(t, func() bool {
		rl.mu.RLock()
		defer rl.mu.RUnlock()
		_, exists := rl.limiters["1.2.3.4"]
		return !exists
	}, time.Second, 10*time.Millisecond, "cleanup loop should have removed the idle entry")

	close(done)
}
