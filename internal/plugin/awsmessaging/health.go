package awsmessaging

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/types"
)

// Health tracks readiness for GET /healthz. The plugin is ready when the
// initial Trigger sync has completed and every active subscription has an
// established SQS session (a successful GetQueueAttributes/ReceiveMessage
// against its queue). A plugin with no active subscriptions is idle and
// reports ready, since there is no session that could be failing; otherwise
// the Integration's Deployment could never become Ready before its first
// Trigger exists.
type Health struct {
	mu       sync.Mutex
	started  bool
	sessions map[types.NamespacedName]sessionState
}

type sessionState struct {
	ok  bool
	msg string
}

// NewHealth returns a Health that is not yet ready.
func NewHealth() *Health {
	return &Health{sessions: map[types.NamespacedName]sessionState{}}
}

// SetStarted marks the initial Trigger cache sync as complete.
func (h *Health) SetStarted() {
	h.mu.Lock()
	h.started = true
	h.mu.Unlock()
}

// SetSession records the SQS session state for a Trigger's subscription.
func (h *Health) SetSession(key types.NamespacedName, ok bool, msg string) {
	h.mu.Lock()
	h.sessions[key] = sessionState{ok: ok, msg: msg}
	h.mu.Unlock()
}

// Forget drops a Trigger's session state (subscription removed).
func (h *Health) Forget(key types.NamespacedName) {
	h.mu.Lock()
	delete(h.sessions, key)
	h.mu.Unlock()
}

// Check returns nil when ready, else an error describing why not.
func (h *Health) Check() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.started {
		return fmt.Errorf("initial Trigger sync not complete")
	}
	var bad []string
	for k, s := range h.sessions {
		if !s.ok {
			bad = append(bad, fmt.Sprintf("%s: %s", k.Name, s.msg))
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return fmt.Errorf("no SQS session for: %s", strings.Join(bad, "; "))
	}
	return nil
}

// ServeHTTP implements GET /healthz: 200 "ok" when ready, 503 otherwise.
func (h *Health) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	if err := h.Check(); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// Mux returns a mux serving only /healthz.
func (h *Health) Mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/healthz", h)
	return mux
}
