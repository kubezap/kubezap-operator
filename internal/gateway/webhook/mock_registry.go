package webhook

import (
	"strings"
	"sync"

	automationv1alpha1 "github.com/borfswitch/kubezap/api/v1alpha1"
)

// MockEntry stores configuration for a registered mock route.
type MockEntry struct {
	Name             string
	Namespace        string
	Response         *automationv1alpha1.MockResponse
	ResponseSequence []automationv1alpha1.MockResponse
	responseIndex    int
	MaxHistory       int32
}

// MockRegistry is a thread-safe in-memory registry for mock routes.
type MockRegistry struct {
	mu      sync.Mutex
	entries map[string]MockEntry // keyed by path e.g. "/mock/my-endpoint"
}

// NewMockRegistry creates a new MockRegistry instance.
func NewMockRegistry() *MockRegistry {
	return &MockRegistry{entries: make(map[string]MockEntry)}
}

// normalizeMockPath ensures path always begins with /mock/.
func normalizeMockPath(path string) string {
	path = strings.TrimSpace(path)
	if !strings.HasPrefix(path, "/mock/") {
		path = "/mock/" + strings.TrimPrefix(path, "/")
	}
	return path
}

// Register adds or replaces a mock route.
func (r *MockRegistry) Register(path string, entry MockEntry) {
	path = normalizeMockPath(path)

	r.mu.Lock()
	defer r.mu.Unlock()

	// Preserve responseIndex if the entry already exists (cycling continuity).
	if existing, ok := r.entries[path]; ok {
		entry.responseIndex = existing.responseIndex
	}
	r.entries[path] = entry
}

// Deregister removes a mock route.
func (r *MockRegistry) Deregister(path string) {
	path = normalizeMockPath(path)

	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.entries, path)
}

// Lookup returns the entry for a path.
func (r *MockRegistry) Lookup(path string) (MockEntry, bool) {
	path = normalizeMockPath(path)

	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.entries[path]
	return entry, ok
}

// NextResponse returns the next response for a path, advancing responseIndex and wrapping around.
// If ResponseSequence is non-empty, cycles through it. Otherwise returns Response.
// Returns nil if neither is set.
func (r *MockRegistry) NextResponse(path string) *automationv1alpha1.MockResponse {
	path = normalizeMockPath(path)

	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.entries[path]
	if !ok {
		return nil
	}

	if len(entry.ResponseSequence) > 0 {
		resp := entry.ResponseSequence[entry.responseIndex%len(entry.ResponseSequence)]
		entry.responseIndex++
		r.entries[path] = entry
		return &resp
	}

	return entry.Response
}
