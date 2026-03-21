package webhook

import (
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
)

// RouteEntry represents an HTTP route backed by a Trigger and Flow.
type RouteEntry struct {
	TriggerName      string
	TriggerNamespace string
	FlowRef          string
	FlowNamespace    string
	AllowedMethod    string // "POST" or "PUT"

	// Auth fields — pre-loaded at route registration time.
	AuthType      string   // "hmac", "bearer", "apiKey", "ipAllowlist", "oidc", "basic", "" (none)
	HMACSecret    string   // pre-loaded HMAC secret value
	BearerToken   string   // pre-loaded bearer token value
	BasicUsername string   // pre-loaded Basic auth username
	BasicPassword string   // pre-loaded Basic auth password
	APIKey        string   // pre-loaded API key value
	APIKeyHeader  string   // header name for API key (default "X-Api-Key")
	IPAllowlist   []string // CIDR blocks or IP addresses

	// OIDC/JWT auth fields
	OIDCValidator *oidcValidator // non-nil when AuthType == "oidc"

	// Cooldown fields — pre-loaded from Trigger.Spec.Cooldown at registration time.
	MaxInvocations int32         // 0 means no limit
	CooldownWindow time.Duration // window for counting invocations
}

// RouteRegistry is a thread-safe in-memory registry for webhook routes.
type RouteRegistry struct {
	mu     sync.RWMutex
	routes map[string]RouteEntry // key: HTTP path (e.g. "/hooks/github")
	synced bool
	logger logr.Logger
}

// NewRouteRegistry creates a new RouteRegistry instance.
func NewRouteRegistry(logger logr.Logger) *RouteRegistry {
	return &RouteRegistry{routes: make(map[string]RouteEntry), logger: logger}
}

// normalizePath ensures path always begins with /hooks/.
func normalizePath(path string) string {
	path = strings.TrimSpace(path)
	if !strings.HasPrefix(path, "/hooks/") {
		path = "/hooks/" + strings.TrimPrefix(path, "/")
	}
	return path
}

// Register adds or updates a route entry.
func (r *RouteRegistry) Register(path string, entry RouteEntry) {
	path = normalizePath(path)

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.routes[path]; ok {
		r.logger.Info("updating route", "path", path, "entry", entry)
	} else {
		r.logger.Info("registering route", "path", path, "entry", entry)
	}
	entry.AllowedMethod = strings.ToUpper(entry.AllowedMethod)
	r.routes[path] = entry
}

// Deregister removes a route entry.
func (r *RouteRegistry) Deregister(path string) {
	path = normalizePath(path)

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.routes[path]; ok {
		delete(r.routes, path)
		r.logger.Info("deregistered route", "path", path)
	} else {
		r.logger.Info("route not found for deregister", "path", path)
	}
}

// Lookup finds a route entry by path.
func (r *RouteRegistry) Lookup(path string) (RouteEntry, bool) {
	path = normalizePath(path)

	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.routes[path]
	return entry, ok
}

// MarkSynced marks the registry as synced.
func (r *RouteRegistry) MarkSynced() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.synced = true
}

// IsSynced returns whether the registry has completed an initial sync.
func (r *RouteRegistry) IsSynced() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.synced
}
