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

package webhook

import (
	"context"
	"crypto/x509"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

// This file implements mTLS client-certificate revocation checking (CRL) for
// the webhook gateway's kubezap.io/webhook-mtls-ca-secret auth path. See
// docs/design/client-cert-revocation-checking.md for the full design record.
//
// TriggerWatcher (watcher.go) owns the write side: its crcache.Cache runs a
// third informer on ConfigMap, and handleCRLConfigMap below parses and
// publishes updates to the shared crlHolder. cmd/webhook-gateway/main.go owns
// the read side via VerifyClientCertNotRevoked below, called from
// tls.Config.VerifyPeerCertificate on every handshake.

// crlConfigMapKey is the fixed key within the CRL ConfigMap that holds the
// DER-encoded Certificate Revocation List — checked in BinaryData first, then
// Data as a fallback (kubectl auto-detects DER's non-UTF8 content and stores
// it in BinaryData, but a hand-authored ConfigMap might use either). Matches
// this project's convention of fixed keys for well-known Secret/ConfigMap
// content (tls.crt/tls.key/ca.crt) rather than a configurable key name.
const crlConfigMapKey = "crl.der"

// crlStalenessCheckInterval is how often the background goroutine started
// from Start (see watchCRLStaleness) re-evaluates whether the currently
// loaded CRL has gone stale (its nextUpdate has passed), independent of
// whether any new ConfigMap event or client connection has occurred. This is
// what makes a broken CRL refresh pipeline observable during a quiet period
// with no incoming traffic, not just at the moment a client happens to
// connect.
const crlStalenessCheckInterval = 30 * time.Second

// labelNamespace is the Prometheus label / JSON key name for a namespace,
// shared across this file's metrics and handler.go's FlowRun-creation
// response body to satisfy golangci-lint's goconst check on the repeated
// "namespace" string literal.
const labelNamespace = "namespace"

// Prometheus metrics for mTLS CRL observability. Defined here (rather than in
// internal/metrics, where this project's other cross-cutting metrics live)
// because this story's file footprint is limited to this package — see
// docs/design/client-cert-revocation-checking.md's Tradeoffs section.
var (
	// crlParseErrorsTotal counts failed x509.ParseRevocationList attempts on an
	// updated CRL ConfigMap. A previously loaded good CRL is never cleared on
	// a parse failure (see handleCRLConfigMap), so this metric is the primary
	// signal that an operator's CRL refresh pipeline is producing bad output.
	crlParseErrorsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kubezap_webhook_mtls_crl_parse_errors_total",
		Help: "Total number of times the webhook gateway failed to parse an updated mTLS CRL ConfigMap, by namespace.",
	}, []string{labelNamespace})

	// crlStale reports whether the currently loaded mTLS CRL's nextUpdate has
	// passed (1) or not (0). Only ever set when a CRL ConfigMap is configured;
	// a namespace with no crlConfigMapRef never reports this metric at all.
	crlStale = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubezap_webhook_mtls_crl_stale",
		Help: "1 if the loaded mTLS CRL's nextUpdate has passed (all client-cert auth is failing closed), 0 if fresh, by namespace.",
	}, []string{labelNamespace})

	// crlNextUpdateTimestamp exposes the loaded CRL's nextUpdate as a unix
	// timestamp, so staleness can be predicted ahead of the transition (e.g.
	// alert when nextUpdate is within N hours) rather than only reacting to it.
	crlNextUpdateTimestamp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubezap_webhook_mtls_crl_next_update_timestamp_seconds",
		Help: "Unix timestamp of the currently loaded mTLS CRL's nextUpdate field, by namespace.",
	}, []string{labelNamespace})
)

func init() {
	ctrlmetrics.Registry.MustRegister(crlParseErrorsTotal, crlStale, crlNextUpdateTimestamp)
}

// handleCRLConfigMap parses the mTLS CRL ConfigMap on add/update and, on
// success, atomically publishes it to w.crlHolder for
// cmd/webhook-gateway/main.go's VerifyPeerCertificate callback (built via
// VerifyClientCertNotRevoked) to read.
//
// A parse failure (missing key, malformed DER) never clears a previously
// loaded good CRL — it is logged and counted via crlParseErrorsTotal instead,
// so a broken CRL refresh pipeline degrades to "stale, then fail-closed" on
// its own schedule rather than immediately and silently disabling revocation
// checking.
func (w *TriggerWatcher) handleCRLConfigMap(obj interface{}) {
	cm, ok := obj.(*corev1.ConfigMap)
	if !ok {
		return
	}
	if cm.Name != w.crlConfigMapName {
		return
	}

	der := cm.BinaryData[crlConfigMapKey]
	if len(der) == 0 {
		if s := cm.Data[crlConfigMapKey]; s != "" {
			der = []byte(s)
		}
	}
	if len(der) == 0 {
		w.log.Error(fmt.Errorf("configmap %q missing key %q", cm.Name, crlConfigMapKey),
			"mTLS CRL ConfigMap has no usable content; keeping previously loaded CRL (if any)",
			"configmap", cm.Name, "namespace", cm.Namespace)
		crlParseErrorsTotal.WithLabelValues(w.namespace).Inc()
		return
	}

	crl, err := x509.ParseRevocationList(der)
	if err != nil {
		w.log.Error(err, "failed to parse mTLS CRL ConfigMap; keeping previously loaded CRL (if any)",
			"configmap", cm.Name, "namespace", cm.Namespace)
		crlParseErrorsTotal.WithLabelValues(w.namespace).Inc()
		return
	}

	w.crlHolder.Store(crl)
	w.log.Info("loaded updated mTLS CRL",
		"configmap", cm.Name, "namespace", cm.Namespace,
		"revokedCount", len(crl.RevokedCertificateEntries),
		"thisUpdate", crl.ThisUpdate, "nextUpdate", crl.NextUpdate)
	w.updateCRLMetrics(crl)
}

// watchCRLStaleness re-evaluates the loaded CRL's staleness on a fixed timer
// (crlStalenessCheckInterval), independent of ConfigMap events or incoming
// connections, so a broken refresh pipeline is observable during a quiet
// period rather than only discovered at the next client handshake or the next
// CRL update. Logs once on the fresh-to-stale transition, not on every tick,
// to avoid log spam for the (potentially long) remainder of an outage.
func (w *TriggerWatcher) watchCRLStaleness(ctx context.Context) {
	ticker := time.NewTicker(crlStalenessCheckInterval)
	defer ticker.Stop()

	wasStale := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			crl := w.crlHolder.Load()
			if crl == nil {
				continue
			}
			stale := w.updateCRLMetrics(crl)
			if stale && !wasStale {
				w.log.Error(fmt.Errorf("mTLS CRL nextUpdate has passed"),
					"webhook gateway mTLS CRL is stale; all client-certificate authentication will be "+
						"rejected fail-closed until a fresh CRL is provided",
					"namespace", w.namespace, "nextUpdate", crl.NextUpdate)
			}
			wasStale = stale
		}
	}
}

// updateCRLMetrics refreshes the crlStale/crlNextUpdateTimestamp gauges for
// this watcher's namespace and returns whether the CRL is currently stale. A
// zero-value NextUpdate (a CRL that never set the optional RFC 5280 field) is
// treated as immediately stale — this project requires operators' CRL
// issuance pipelines to always set it, since its absence means staleness can
// never be proven. Called both right after a successful parse and on every
// watchCRLStaleness tick, so the gauge reflects reality promptly on update
// and also drifts to stale on its own once nextUpdate passes with no new
// ConfigMap event.
func (w *TriggerWatcher) updateCRLMetrics(crl *x509.RevocationList) bool {
	stale := isCRLStale(crl)
	if stale {
		crlStale.WithLabelValues(w.namespace).Set(1)
	} else {
		crlStale.WithLabelValues(w.namespace).Set(0)
	}
	crlNextUpdateTimestamp.WithLabelValues(w.namespace).Set(float64(crl.NextUpdate.Unix()))
	return stale
}

// isCRLStale reports whether crl's nextUpdate has passed (or was never set —
// see updateCRLMetrics's doc comment on the zero-value case).
func isCRLStale(crl *x509.RevocationList) bool {
	return time.Now().After(crl.NextUpdate)
}

// VerifyClientCertNotRevoked returns a tls.Config.VerifyPeerCertificate
// callback that rejects any presented client certificate whose serial number
// appears in the CRL currently held by crlHolder, and fails closed once the
// loaded CRL's nextUpdate has passed. A nil value in crlHolder (no CRL
// configured, or not loaded yet) is a no-op — identical to today's behavior
// of not setting VerifyPeerCertificate at all.
//
// This runs after Go's TLS stack has already built and verified a chain from
// the presented client certificate up to a CA in tls.Config.ClientCAs
// (tls.RequireAndVerifyClientCert) — it only adds revocation/staleness
// checking on top of that, it never replaces the chain-of-trust check.
// crlHolder is read fresh on every handshake, so an updated CRL (loaded by
// handleCRLConfigMap) takes effect on the very next connection with no
// gateway restart. See docs/design/client-cert-revocation-checking.md.
func VerifyClientCertNotRevoked(crlHolder *atomic.Pointer[x509.RevocationList], log logr.Logger) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
		crl := crlHolder.Load()
		if crl == nil {
			return nil
		}

		if isCRLStale(crl) {
			// Fail closed: a stale CRL can no longer be trusted to reflect
			// current revocation status, so every client-cert connection is
			// rejected until a fresh CRL is provided. The periodic staleness
			// check (watchCRLStaleness) is the primary observability signal
			// for this condition; this path intentionally does not also log
			// on every rejected handshake, to avoid log-volume amplification
			// during an outage.
			return fmt.Errorf("client certificate rejected: mTLS CRL is stale (nextUpdate %s has passed)",
				crl.NextUpdate.UTC().Format(time.RFC3339))
		}

		for _, chain := range verifiedChains {
			if len(chain) == 0 {
				continue
			}
			leaf := chain[0]
			for _, revoked := range crl.RevokedCertificateEntries {
				if leaf.SerialNumber != nil && revoked.SerialNumber != nil &&
					leaf.SerialNumber.Cmp(revoked.SerialNumber) == 0 {
					log.Info("rejecting revoked client certificate",
						"serial", leaf.SerialNumber.String(), "subject", leaf.Subject.String())
					return fmt.Errorf("client certificate rejected: serial %s is revoked", leaf.SerialNumber.String())
				}
			}
		}
		return nil
	}
}
