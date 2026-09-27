package webhook

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus/testutil"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// generateTestCRL builds and DER-encodes a CRL signed by a freshly generated
// throwaway CA, listing revokedSerials as revoked. Used to exercise
// handleCRLConfigMap/VerifyClientCertNotRevoked without needing real
// operator-issued certificate material.
func generateTestCRL(t *testing.T, revokedSerials []int64, thisUpdate, nextUpdate time.Time) []byte {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating CA key: %v", err)
	}

	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-crl-issuer"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCRLSign | x509.KeyUsageCertSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
		SubjectKeyId:          []byte{1, 2, 3, 4},
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("creating CA certificate: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parsing CA certificate: %v", err)
	}

	entries := make([]x509.RevocationListEntry, len(revokedSerials))
	for i, s := range revokedSerials {
		entries[i] = x509.RevocationListEntry{
			SerialNumber:   big.NewInt(s),
			RevocationTime: time.Now(),
		}
	}

	crlTemplate := &x509.RevocationList{
		RevokedCertificateEntries: entries,
		Number:                    big.NewInt(1),
		ThisUpdate:                thisUpdate,
		NextUpdate:                nextUpdate,
	}
	der, err := x509.CreateRevocationList(rand.Reader, crlTemplate, caCert, caKey)
	if err != nil {
		t.Fatalf("creating CRL: %v", err)
	}
	return der
}

// testLeafCertSerial is the fixed serial number generateTestLeafCert always
// uses -- every VerifyClientCertNotRevoked test below presents a certificate
// with this serial and instead varies which serials the loaded CRL revokes.
const testLeafCertSerial = 123

// generateTestLeafCert builds a throwaway (self-signed, unrelated-to-any-CRL-issuer)
// certificate with serial number testLeafCertSerial, standing in for a
// presented client cert.
func generateTestLeafCert(t *testing.T) *x509.Certificate {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating leaf key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(testLeafCertSerial),
		Subject:      pkix.Name{CommonName: "test-client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating leaf certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsing leaf certificate: %v", err)
	}
	return cert
}

// testCRLConfigMapName is the fixed ConfigMap name every newCRLTestWatcher
// instance below is configured to watch for.
const testCRLConfigMapName = "webhook-mtls-crl"

// newCRLTestWatcher builds a minimal TriggerWatcher for exercising
// handleCRLConfigMap/updateCRLMetrics, with a distinct namespace per test so
// the process-global Prometheus metrics in crl.go don't leak values between
// test cases sharing the same test binary.
func newCRLTestWatcher(t *testing.T, namespace string) *TriggerWatcher {
	t.Helper()
	w := newMinimalWatcher(t)
	w.namespace = namespace
	w.crlConfigMapName = testCRLConfigMapName
	w.crlHolder = new(atomic.Pointer[x509.RevocationList])
	return w
}

func newCRLConfigMap(name, namespace string, der []byte) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		BinaryData: map[string][]byte{crlConfigMapKey: der},
	}
}

// TestHandleCRLConfigMap_ParsesAndStoresCRL verifies the happy path: a
// matching ConfigMap add/update event is parsed and published to crlHolder.
func TestHandleCRLConfigMap_ParsesAndStoresCRL(t *testing.T) {
	w := newCRLTestWatcher(t, "ns-parse-ok")
	der := generateTestCRL(t, []int64{7, 42}, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))

	w.handleCRLConfigMap(newCRLConfigMap("webhook-mtls-crl", "ns-parse-ok", der))

	crl := w.crlHolder.Load()
	if crl == nil {
		t.Fatalf("expected CRL to be stored, got nil")
	}
	if len(crl.RevokedCertificateEntries) != 2 {
		t.Errorf("expected 2 revoked entries, got %d", len(crl.RevokedCertificateEntries))
	}
}

// TestHandleCRLConfigMap_IgnoresNonMatchingName verifies a ConfigMap event for
// a differently-named object (some unrelated ConfigMap in the same namespace)
// is ignored, leaving the holder untouched.
func TestHandleCRLConfigMap_IgnoresNonMatchingName(t *testing.T) {
	w := newCRLTestWatcher(t, "ns-name-mismatch")
	der := generateTestCRL(t, nil, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))

	w.handleCRLConfigMap(newCRLConfigMap("some-other-configmap", "ns-name-mismatch", der))

	if crl := w.crlHolder.Load(); crl != nil {
		t.Fatalf("expected holder to remain nil for a non-matching ConfigMap name, got %+v", crl)
	}
}

// TestHandleCRLConfigMap_MalformedDER_KeepsPreviousGoodCRL verifies that a
// parse failure never clears a previously loaded good CRL — per the design
// record, a broken refresh pipeline must degrade to "stale, then
// fail-closed" on its own schedule, not immediately disable revocation
// checking by wiping the last known-good CRL.
func TestHandleCRLConfigMap_MalformedDER_KeepsPreviousGoodCRL(t *testing.T) {
	ns := "ns-malformed-keeps-good"
	w := newCRLTestWatcher(t, ns)
	goodDER := generateTestCRL(t, []int64{99}, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	w.handleCRLConfigMap(newCRLConfigMap("webhook-mtls-crl", ns, goodDER))

	loadedBefore := w.crlHolder.Load()
	if loadedBefore == nil {
		t.Fatalf("setup failed: expected a good CRL to be loaded before the malformed update")
	}
	errorsBefore := testutil.ToFloat64(crlParseErrorsTotal.WithLabelValues(ns))

	w.handleCRLConfigMap(newCRLConfigMap("webhook-mtls-crl", ns, []byte("not a valid DER-encoded CRL")))

	loadedAfter := w.crlHolder.Load()
	if loadedAfter != loadedBefore {
		t.Errorf("expected the previously loaded good CRL to be retained after a malformed update, got a different value")
	}
	errorsAfter := testutil.ToFloat64(crlParseErrorsTotal.WithLabelValues(ns))
	if errorsAfter != errorsBefore+1 {
		t.Errorf("expected crlParseErrorsTotal to increment by 1, went from %v to %v", errorsBefore, errorsAfter)
	}
}

// TestHandleCRLConfigMap_MissingKey verifies a ConfigMap with neither
// binaryData nor data under the fixed "crl.der" key is treated the same as a
// parse failure (counted, logged, previous CRL retained) rather than panicking.
func TestHandleCRLConfigMap_MissingKey(t *testing.T) {
	ns := "ns-missing-key"
	w := newCRLTestWatcher(t, ns)
	errorsBefore := testutil.ToFloat64(crlParseErrorsTotal.WithLabelValues(ns))

	empty := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "webhook-mtls-crl", Namespace: ns}}
	w.handleCRLConfigMap(empty)

	if crl := w.crlHolder.Load(); crl != nil {
		t.Errorf("expected no CRL to be loaded from an empty ConfigMap, got %+v", crl)
	}
	errorsAfter := testutil.ToFloat64(crlParseErrorsTotal.WithLabelValues(ns))
	if errorsAfter != errorsBefore+1 {
		t.Errorf("expected crlParseErrorsTotal to increment by 1, went from %v to %v", errorsBefore, errorsAfter)
	}
}

// TestUpdateCRLMetrics_StaleVsFresh verifies the crlStale gauge reflects
// whether nextUpdate has passed.
func TestUpdateCRLMetrics_StaleVsFresh(t *testing.T) {
	freshNS := "ns-metrics-fresh"
	wFresh := newCRLTestWatcher(t, freshNS)
	freshCRL := &x509.RevocationList{NextUpdate: time.Now().Add(time.Hour)}
	if stale := wFresh.updateCRLMetrics(freshCRL); stale {
		t.Errorf("expected a CRL with a future nextUpdate to be reported fresh")
	}
	if got := testutil.ToFloat64(crlStale.WithLabelValues(freshNS)); got != 0 {
		t.Errorf("expected crlStale gauge to be 0, got %v", got)
	}

	staleNS := "ns-metrics-stale"
	wStale := newCRLTestWatcher(t, staleNS)
	staleCRL := &x509.RevocationList{NextUpdate: time.Now().Add(-time.Hour)}
	if stale := wStale.updateCRLMetrics(staleCRL); !stale {
		t.Errorf("expected a CRL with a past nextUpdate to be reported stale")
	}
	if got := testutil.ToFloat64(crlStale.WithLabelValues(staleNS)); got != 1 {
		t.Errorf("expected crlStale gauge to be 1, got %v", got)
	}
}

// TestVerifyClientCertNotRevoked_NoCRLConfigured_NoOp is the regression test
// for the "No CRL configured → behavior identical to today" acceptance
// criterion: a crlHolder that has never been written to (nil Load()) must
// never reject a connection, regardless of what chains are presented.
func TestVerifyClientCertNotRevoked_NoCRLConfigured_NoOp(t *testing.T) {
	var holder atomic.Pointer[x509.RevocationList]
	verify := VerifyClientCertNotRevoked(&holder, testLogger(t))

	leaf := generateTestLeafCert(t)
	if err := verify(nil, [][]*x509.Certificate{{leaf}}); err != nil {
		t.Errorf("expected no-op (nil error) with no CRL configured, got: %v", err)
	}
}

// TestVerifyClientCertNotRevoked_RevokedSerial_Rejected verifies a client
// certificate whose serial number appears in the loaded CRL is rejected.
func TestVerifyClientCertNotRevoked_RevokedSerial_Rejected(t *testing.T) {
	var holder atomic.Pointer[x509.RevocationList]
	holder.Store(&x509.RevocationList{
		NextUpdate: time.Now().Add(time.Hour),
		RevokedCertificateEntries: []x509.RevocationListEntry{
			{SerialNumber: big.NewInt(123), RevocationTime: time.Now()},
		},
	})
	verify := VerifyClientCertNotRevoked(&holder, testLogger(t))

	leaf := generateTestLeafCert(t)
	if err := verify(nil, [][]*x509.Certificate{{leaf}}); err == nil {
		t.Errorf("expected a revoked serial to be rejected, got nil error")
	}
}

// TestVerifyClientCertNotRevoked_ValidSerial_Allowed verifies a client
// certificate whose serial is not in the loaded CRL is allowed through.
func TestVerifyClientCertNotRevoked_ValidSerial_Allowed(t *testing.T) {
	var holder atomic.Pointer[x509.RevocationList]
	holder.Store(&x509.RevocationList{
		NextUpdate: time.Now().Add(time.Hour),
		RevokedCertificateEntries: []x509.RevocationListEntry{
			{SerialNumber: big.NewInt(999), RevocationTime: time.Now()},
		},
	})
	verify := VerifyClientCertNotRevoked(&holder, testLogger(t))

	leaf := generateTestLeafCert(t)
	if err := verify(nil, [][]*x509.Certificate{{leaf}}); err != nil {
		t.Errorf("expected a non-revoked serial to be allowed, got: %v", err)
	}
}

// TestVerifyClientCertNotRevoked_StaleCRL_FailsClosed verifies that once the
// loaded CRL's nextUpdate has passed, every client-cert connection is
// rejected — even one presenting a certificate whose serial isn't revoked.
func TestVerifyClientCertNotRevoked_StaleCRL_FailsClosed(t *testing.T) {
	var holder atomic.Pointer[x509.RevocationList]
	holder.Store(&x509.RevocationList{
		NextUpdate:                time.Now().Add(-time.Minute), // already stale
		RevokedCertificateEntries: nil,
	})
	verify := VerifyClientCertNotRevoked(&holder, testLogger(t))

	leaf := generateTestLeafCert(t)
	if err := verify(nil, [][]*x509.Certificate{{leaf}}); err == nil {
		t.Errorf("expected a stale CRL to fail closed (reject) even a non-revoked serial, got nil error")
	}
}

// testLogger returns a discard logr.Logger suitable for tests that don't
// assert on log output.
func testLogger(t *testing.T) logr.Logger {
	t.Helper()
	return logr.Discard()
}
