package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"sync"

	"github.com/sirosfoundation/g119612/pkg/logging"
	"github.com/sirosfoundation/go-zk-circuits/pkg/catalog"
)

// ServerContext holds the shared, thread-safe state for the API server
// (mirrors go-trust's pkg/api.ServerContext shape). FS is the root of the
// catalog data — either the go:embed'd tree compiled into the binary, or an
// os.DirFS pointed at ZKC_ARTIFACT_DIR (spec §4.5's volume-strategy escape
// hatch, §4.6's runtime config table).
type ServerContext struct {
	mu          sync.RWMutex
	Logger      logging.Logger
	RateLimiter *RateLimiter
	Metrics     *Metrics
	BaseURL     string
	FS          fs.FS

	manifest      *catalog.Manifest
	manifestRaw   []byte
	manifestETag  string
	byCanonicalID map[string]*catalog.CircuitDescriptor // id -> entry (never alias)
	aliasToID     map[string]string                     // alias -> canonical id
	entryRaw      map[string][]byte                     // canonical id -> marshaled bare CircuitDescriptor
	entryETag     map[string]string
}

// NewServerContext creates a ServerContext with a guaranteed non-nil logger,
// matching go-trust's NewServerContext convention.
func NewServerContext(logger logging.Logger, fsys fs.FS, baseURL string) *ServerContext {
	if logger == nil {
		logger = logging.DefaultLogger()
	}
	return &ServerContext{Logger: logger, FS: fsys, BaseURL: baseURL}
}

// LoadCatalog reads catalog/manifest.json from FS, validates it, and builds
// the id/alias lookup indices. Call it once at startup; a future
// live-reload path (not needed for Phase 1/2 — the service restarts on
// every deploy, spec §4.6) would call it again under Lock().
func (s *ServerContext) LoadCatalog() error {
	m, err := catalog.LoadManifestFile(s.FS, "catalog/manifest.json")
	if err != nil {
		return fmt.Errorf("load manifest: %w", err)
	}
	if err := catalog.ValidateManifest(m); err != nil {
		return fmt.Errorf("validate manifest: %w", err)
	}
	raw, err := catalog.MarshalDeterministic(m)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}

	byID := map[string]*catalog.CircuitDescriptor{}
	aliasToID := map[string]string{}
	entryRaw := map[string][]byte{}
	entryETag := map[string]string{}
	for i := range m.Circuits {
		e := &m.Circuits[i]
		byID[e.ID] = e
		for _, a := range e.Aliases {
			aliasToID[a] = e.ID
		}
		eb, err := marshalEntry(e)
		if err != nil {
			return fmt.Errorf("marshal entry %s: %w", e.ID, err)
		}
		entryRaw[e.ID] = eb
		entryETag[e.ID] = weakETag(eb)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.manifest = m
	s.manifestRaw = raw
	s.manifestETag = weakETag(raw)
	s.byCanonicalID = byID
	s.aliasToID = aliasToID
	s.entryRaw = entryRaw
	s.entryETag = entryETag
	return nil
}

// snapshot returns a consistent, read-locked view of the currently loaded catalog state.
func (s *ServerContext) snapshot() (manifestRaw []byte, manifestETag string, byCanonicalID map[string]*catalog.CircuitDescriptor, aliasToID map[string]string, entryRaw map[string][]byte, entryETag map[string]string, generatedAt string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	genAt := ""
	if s.manifest != nil {
		genAt = s.manifest.GeneratedAt
	}
	return s.manifestRaw, s.manifestETag, s.byCanonicalID, s.aliasToID, s.entryRaw, s.entryETag, genAt
}

// marshalEntry renders a bare CircuitDescriptor, not wrapped in a manifest
// envelope (spec §3.4: "the bare object ..., not wrapped in a manifest envelope").
func marshalEntry(e *catalog.CircuitDescriptor) ([]byte, error) {
	buf, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(buf, '\n'), nil
}

// weakETag renders the strong ETag form spec §3.3 specifies: "sha256-<first16hex>" of the content bytes.
func weakETag(content []byte) string {
	sum := sha256.Sum256(content)
	return `"sha256-` + hex.EncodeToString(sum[:])[:16] + `"`
}
