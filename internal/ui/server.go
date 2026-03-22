/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package ui

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

//go:embed all:dist
var distFS embed.FS

// Server holds the mux and Kubernetes client for the dashboard HTTP server.
type Server struct {
	client client.Client
	mux    *http.ServeMux
}

// NewServer creates a dashboard HTTP server. bearerToken may be empty (no auth).
func NewServer(c client.Client, bearerToken string) *Server {
	s := &Server{client: c, mux: http.NewServeMux()}
	s.registerRoutes(bearerToken)
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// Start begins serving on the given address until ctx is cancelled.
func (s *Server) Start(ctx context.Context, addr string) error {
	srv := &http.Server{Addr: addr, Handler: s}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	log.Printf("dashboard UI listening on %s/ui/", addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) registerRoutes(bearerToken string) {
	var mw func(http.Handler) http.Handler
	if bearerToken != "" {
		mw = bearerTokenMiddleware(bearerToken)
	} else {
		mw = func(h http.Handler) http.Handler { return h }
	}

	// API routes.
	s.mux.Handle("GET /api/v1/namespaces", mw(http.HandlerFunc(s.handleNamespaces)))
	s.mux.Handle("GET /api/v1/{namespace}/flowruns", mw(http.HandlerFunc(s.handleFlowRunList)))
	s.mux.Handle("GET /api/v1/{namespace}/flowruns/{name}", mw(http.HandlerFunc(s.handleFlowRunDetail)))
	s.mux.Handle("GET /api/v1/{namespace}/triggers", mw(http.HandlerFunc(s.handleTriggerList)))
	s.mux.Handle("GET /api/v1/{namespace}/flows", mw(http.HandlerFunc(s.handleFlowList)))
	s.mux.Handle("GET /api/v1/events", mw(http.HandlerFunc(s.handleSSE)))

	// Vue SPA — serve dist/ for /ui/* with SPA fallback.
	distSubFS, err := fs.Sub(distFS, "dist")
	if err != nil {
		// dist/ is the placeholder directory; serve the dev page.
		s.mux.HandleFunc("GET /ui/", devPlaceholder)
		return
	}
	fileServer := http.FileServer(http.FS(distSubFS))
	s.mux.HandleFunc("GET /ui/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/ui")
		if path == "" {
			path = "/"
		}
		stripped := strings.TrimPrefix(path, "/")
		if stripped == "" {
			stripped = "index.html"
		}
		// SPA fallback: serve index.html for paths that don't match a static file.
		if _, statErr := fs.Stat(distSubFS, stripped); statErr != nil {
			r.URL.Path = "/index.html"
			fileServer.ServeHTTP(w, r)
			return
		}
		r.URL.Path = path
		fileServer.ServeHTTP(w, r)
	})
}

func devPlaceholder(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<!doctype html><html><body>
<h2>KubeZap Dashboard</h2>
<p>UI not built. Run <code>make ui</code> and restart the operator.</p>
</body></html>`))
}

func bearerTokenMiddleware(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+token {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
