package awsmessaging

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/go-logr/logr"
)

// tlsReloader serves the publisher TLS config from disk, re-reading the cert,
// key and CA whenever their mtime changes. The operator rotates all three
// roughly every 23h by updating the mounted Secret in place
// (docs/api/plugin-contract.md#controller-side-mtls); this picks the rotation
// up without a pod restart. On a failed reload the last good material is kept.
type tlsReloader struct {
	certFile, keyFile, caFile string

	mu      sync.Mutex
	mtimes  [3]time.Time
	cert    *tls.Certificate
	clients *x509.CertPool
}

func newTLSReloader(certFile, keyFile, caFile string) (*tlsReloader, error) {
	r := &tlsReloader{certFile: certFile, keyFile: keyFile, caFile: caFile}
	if err := r.reload(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *tlsReloader) stat() ([3]time.Time, error) {
	var out [3]time.Time
	for i, f := range []string{r.certFile, r.keyFile, r.caFile} {
		fi, err := os.Stat(f)
		if err != nil {
			return out, err
		}
		out[i] = fi.ModTime()
	}
	return out, nil
}

// reload takes the lock and (re)reads all three files.
func (r *tlsReloader) reload() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reloadLocked()
}

func (r *tlsReloader) reloadLocked() error {
	mt, err := r.stat()
	if err != nil {
		return fmt.Errorf("mTLS files: %w", err)
	}
	cert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return fmt.Errorf("loading server cert/key: %w", err)
	}
	caPEM, err := os.ReadFile(r.caFile)
	if err != nil {
		return fmt.Errorf("reading CA file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return errors.New("CA file contains no valid PEM certificates")
	}
	r.cert, r.clients, r.mtimes = &cert, pool, mt
	return nil
}

// current returns the freshest valid material, reloading if files changed.
func (r *tlsReloader) current() (*tls.Certificate, *x509.CertPool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if mt, err := r.stat(); err == nil && mt != r.mtimes {
		// Keep serving the previous material if the new files are mid-update
		// or invalid.
		_ = r.reloadLocked()
	}
	return r.cert, r.clients
}

// ServerTLSConfig returns a tls.Config that requires and verifies a client
// certificate against the CA file, as the contract requires, with hot reload
// of cert, key and CA.
func ServerTLSConfig(certFile, keyFile, caFile string) (*tls.Config, error) {
	r, err := newTLSReloader(certFile, keyFile, caFile)
	if err != nil {
		return nil, err
	}
	build := func() *tls.Config {
		cert, pool := r.current()
		return &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{*cert},
			ClientCAs:    pool,
			ClientAuth:   tls.RequireAndVerifyClientCert,
		}
	}
	base := build()
	base.GetConfigForClient = func(*tls.ClientHelloInfo) (*tls.Config, error) { return build(), nil }
	return base, nil
}

// Servers are the plugin's HTTP listeners.
type Servers struct {
	// Publisher serves POST /publish (and /healthz when mTLS is off).
	Publisher *http.Server
	// Health serves /healthz over plain HTTP on KUBEZAP_MTLS_HEALTH_PORT; nil
	// unless mTLS is enabled.
	Health *http.Server
	// TLS reports whether Publisher terminates mutual TLS.
	TLS bool
}

// NewServers builds the listeners per the contract.
//
// mTLS off: one plain-HTTP server on PublisherPort serving /publish and
// /healthz.
// mTLS on: PublisherPort serves ONLY /publish over TLS with required client
// certs; /healthz moves to a separate plain-HTTP server on HealthPort, since
// kubelet probes cannot present a client certificate.
func NewServers(cfg Config, pub *Publisher, health *Health) (*Servers, error) {
	newSrv := func(port int, h http.Handler) *http.Server {
		return &http.Server{
			Addr:              fmt.Sprintf(":%d", port),
			Handler:           h,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      publishTimeout + 10*time.Second,
			IdleTimeout:       60 * time.Second,
		}
	}
	if !cfg.MTLSEnabled {
		mux := pub.Mux()
		mux.Handle("/healthz", health)
		return &Servers{Publisher: newSrv(cfg.PublisherPort, mux)}, nil
	}
	tlsCfg, err := ServerTLSConfig(cfg.MTLSCertFile, cfg.MTLSKeyFile, cfg.MTLSCAFile)
	if err != nil {
		return nil, err
	}
	ps := newSrv(cfg.PublisherPort, pub.Mux())
	ps.TLSConfig = tlsCfg
	return &Servers{Publisher: ps, Health: newSrv(cfg.HealthPort, health.Mux()), TLS: true}, nil
}

// Start launches the listeners in goroutines; fatal serve errors are sent on
// the returned channel (http.ErrServerClosed is not an error).
func (s *Servers) Start(log logr.Logger) <-chan error {
	errCh := make(chan error, 2)
	serve := func(name string, srv *http.Server, useTLS bool) {
		log.Info("starting HTTP server", "server", name, "addr", srv.Addr, "tls", useTLS)
		var err error
		if useTLS {
			err = srv.ListenAndServeTLS("", "")
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("%s server: %w", name, err)
		}
	}
	go serve("publisher", s.Publisher, s.TLS)
	if s.Health != nil {
		go serve("health", s.Health, false)
	}
	return errCh
}

// Shutdown gracefully drains in-flight publishes and probes.
func (s *Servers) Shutdown(ctx context.Context) error {
	err := s.Publisher.Shutdown(ctx)
	if s.Health != nil {
		err = errors.Join(err, s.Health.Shutdown(ctx))
	}
	return err
}
