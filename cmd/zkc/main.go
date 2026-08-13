// zkc is the go-zk-circuits read API service (spec §3, §4). All of its data
// comes from either the go:embed'd catalog (default) or a directory on
// disk (ZKC_ARTIFACT_DIR, spec §4.5's escape hatch for the volume storage
// strategy and for local dev). The service must run correctly with no
// configuration at all — every env var below has a sane default (spec §4.6).
package main

import (
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirosfoundation/g119612/pkg/logging"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	zkcircuits "github.com/sirosfoundation/go-zk-circuits"
	_ "github.com/sirosfoundation/go-zk-circuits/docs/swagger" // import for side effects: registers generated swagger.json
	"github.com/sirosfoundation/go-zk-circuits/pkg/api"
)

// @title go-zk-circuits API
// @version 0.1.0
// @description Public, read-only catalog and content-addressed file host for
// @description zero-knowledge-proof circuit artifacts and their metadata.
// @description See circuit-distribution-service-spec.md for the full contract.
// @description
// @description Every endpoint here MUST be implementable by a dumb static
// @description file server (spec §3.1) — the hosting decision stays reversible.
// @termsOfService https://github.com/sirosfoundation/go-zk-circuits

// @contact.name sirosfoundation
// @contact.url https://github.com/sirosfoundation/go-zk-circuits

// @license.name BSD-2-Clause
// @license.url https://opensource.org/licenses/BSD-2-Clause

// @host localhost:8080
// @BasePath /

// @schemes http https

// @tag.name Manifest
// @tag.description The full circuit catalog

// @tag.name Circuits
// @tag.description Single-entry lookup and optional filtered listing

// @tag.name Artifacts
// @tag.description Content-addressed circuit artifact downloads

// @tag.name Health
// @tag.description Liveness and readiness probes

// Version is set at build time via -ldflags.
var Version = "0.1.0-dev"

func main() {
	listen := envOr("ZKC_LISTEN", ":8080")
	baseURL := os.Getenv("ZKC_BASE_URL")
	logLevelStr := envOr("ZKC_LOG_LEVEL", "info")
	rateLimitRPS := envInt("ZKC_RATE_LIMIT_RPS", 20)
	rateLimitBurst := envInt("ZKC_RATE_LIMIT_BURST", 60)
	artifactDir := os.Getenv("ZKC_ARTIFACT_DIR")

	logger := newLogger(logLevelStr)
	api.Version = Version

	fsys, source := resolveFS(artifactDir)
	logger.Info("Data source configured", logging.F("source", source))

	if baseURL == "" {
		baseURL = "http://" + hostOnly(listen)
	}

	serverCtx := api.NewServerContext(logger, fsys, baseURL)
	if err := serverCtx.LoadCatalog(); err != nil {
		logger.Fatal("Failed to load catalog", logging.F("error", err.Error()))
	}

	metrics := api.NewMetrics()
	serverCtx.Metrics = metrics
	serverCtx.RateLimiter = api.NewRateLimiter(rateLimitRPS, rateLimitBurst)

	done := make(chan struct{})
	serverCtx.RateLimiter.StartCleanupLoop(time.Hour, 24*time.Hour, done)
	defer close(done)

	if logLevelStr != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.LoggerWithConfig(gin.LoggerConfig{
		Skip: func(c *gin.Context) bool {
			return c.Request.URL.Path == "/healthz" && c.Writer.Status() == 200
		},
	}))
	r.Use(gin.Recovery())

	api.RegisterMetricsEndpoint(r, metrics)
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	api.RegisterAPIRoutes(r, serverCtx)

	logger.Info("Starting go-zk-circuits server",
		logging.F("version", Version),
		logging.F("listen", listen),
		logging.F("baseURL", baseURL))

	srv := &http.Server{
		Addr:              listen,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second, // guards against a Slowloris-style slow-header attack
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("API server error", logging.F("error", err.Error()))
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	logger.Info("Shutting down")
}

// resolveFS picks the embedded catalog or a real directory (spec §4.5/§4.6).
func resolveFS(artifactDir string) (fs.FS, string) {
	if artifactDir != "" {
		return os.DirFS(artifactDir), "directory:" + artifactDir
	}
	return zkcircuits.EmbeddedFS, "embedded"
}

func newLogger(level string) logging.Logger {
	var l logging.LogLevel
	switch level {
	case "debug":
		l = logging.DebugLevel
	case "warn":
		l = logging.WarnLevel
	case "error":
		l = logging.ErrorLevel
	default:
		l = logging.InfoLevel
	}
	return logging.NewLogger(l)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		slog.Warn("invalid integer env var, using default", "key", key, "value", v, "default", def)
		return def
	}
	return n
}

// hostOnly strips a leading ":" from a listen address like ":8080" so the
// derived default base URL reads as "http://localhost:8080" rather than
// "http://:8080" when ZKC_BASE_URL is unset (spec §4.6 table).
func hostOnly(listen string) string {
	if len(listen) > 0 && listen[0] == ':' {
		return "localhost" + listen
	}
	return listen
}
