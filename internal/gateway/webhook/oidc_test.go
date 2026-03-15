package webhook

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

// testKeyPair holds an RSA key pair and the corresponding public JWK set for tests.
type testKeyPair struct {
	privateKey *rsa.PrivateKey
	jwks       jwk.Set
}

// generateTestKeyPair creates a throwaway RSA-2048 key pair and a JWKS containing the public key.
func generateTestKeyPair(t *testing.T) testKeyPair {
	t.Helper()

	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}

	// Build JWK from the public key and assign a key ID.
	pubJWK, err := jwk.FromRaw(privKey.Public())
	if err != nil {
		t.Fatalf("building public JWK: %v", err)
	}
	if err := pubJWK.Set(jwk.KeyIDKey, "test-key-1"); err != nil {
		t.Fatalf("setting kid: %v", err)
	}
	if err := pubJWK.Set(jwk.AlgorithmKey, jwa.RS256); err != nil {
		t.Fatalf("setting alg: %v", err)
	}

	set := jwk.NewSet()
	if err := set.AddKey(pubJWK); err != nil {
		t.Fatalf("adding key to set: %v", err)
	}

	return testKeyPair{privateKey: privKey, jwks: set}
}

// signToken creates a signed RS256 JWT from an already-built jwt.Token using the test private key.
func signToken(t *testing.T, kp testKeyPair, tok jwt.Token) string {
	t.Helper()

	privJWK, err := jwk.FromRaw(kp.privateKey)
	if err != nil {
		t.Fatalf("building private JWK: %v", err)
	}
	if err := privJWK.Set(jwk.KeyIDKey, "test-key-1"); err != nil {
		t.Fatalf("setting kid on private JWK: %v", err)
	}

	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256, privJWK))
	if err != nil {
		t.Fatalf("signing token: %v", err)
	}
	return string(signed)
}

// buildToken is a convenience wrapper that builds a jwt.Token from a builder pointer,
// failing the test immediately on any error.
func buildToken(t *testing.T, b *jwt.Builder) jwt.Token {
	t.Helper()
	tok, err := b.Build()
	if err != nil {
		t.Fatalf("building token: %v", err)
	}
	return tok
}

// newJWKSServer starts an httptest server that serves the given JWKS as JSON.
func newJWKSServer(t *testing.T, set jwk.Set) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		data, err := json.Marshal(set)
		if err != nil {
			http.Error(w, "marshal error", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(data)
	}))
}

// TestOIDCValidToken verifies that a valid RS256 token with matching issuer and audience passes validation.
func TestOIDCValidToken(t *testing.T) {
	kp := generateTestKeyPair(t)
	srv := newJWKSServer(t, kp.jwks)
	defer srv.Close()

	issuer := "https://issuer.example.com"
	audience := "kubezap"

	v := newOIDCValidator(srv.URL, issuer, audience)

	tok := buildToken(t, jwt.NewBuilder().
		Issuer(issuer).
		Audience([]string{audience}).
		Subject("user-123").
		Expiration(time.Now().Add(time.Hour)))

	tokenStr := signToken(t, kp, tok)

	if err := v.validate(t.Context(), tokenStr); err != nil {
		t.Errorf("expected valid token to pass, got error: %v", err)
	}
}

// TestOIDCExpiredToken verifies that an expired token is rejected.
func TestOIDCExpiredToken(t *testing.T) {
	kp := generateTestKeyPair(t)
	srv := newJWKSServer(t, kp.jwks)
	defer srv.Close()

	issuer := "https://issuer.example.com"
	audience := "kubezap"

	v := newOIDCValidator(srv.URL, issuer, audience)

	tok := buildToken(t, jwt.NewBuilder().
		Issuer(issuer).
		Audience([]string{audience}).
		Subject("user-123").
		Expiration(time.Now().Add(-time.Hour))) // expired 1 hour ago

	tokenStr := signToken(t, kp, tok)

	err := v.validate(t.Context(), tokenStr)
	if err == nil {
		t.Error("expected expired token to be rejected, got nil error")
	}
}

// TestOIDCWrongIssuer verifies that a token with an incorrect issuer claim is rejected.
func TestOIDCWrongIssuer(t *testing.T) {
	kp := generateTestKeyPair(t)
	srv := newJWKSServer(t, kp.jwks)
	defer srv.Close()

	v := newOIDCValidator(srv.URL, "https://expected-issuer.example.com", "kubezap")

	tok := buildToken(t, jwt.NewBuilder().
		Issuer("https://wrong-issuer.example.com").
		Audience([]string{"kubezap"}).
		Subject("user-123").
		Expiration(time.Now().Add(time.Hour)))

	tokenStr := signToken(t, kp, tok)

	if err := v.validate(t.Context(), tokenStr); err == nil {
		t.Error("expected wrong issuer to be rejected, got nil error")
	}
}

// TestOIDCWrongAudience verifies that a token with an incorrect audience claim is rejected.
func TestOIDCWrongAudience(t *testing.T) {
	kp := generateTestKeyPair(t)
	srv := newJWKSServer(t, kp.jwks)
	defer srv.Close()

	v := newOIDCValidator(srv.URL, "https://issuer.example.com", "expected-audience")

	tok := buildToken(t, jwt.NewBuilder().
		Issuer("https://issuer.example.com").
		Audience([]string{"wrong-audience"}).
		Subject("user-123").
		Expiration(time.Now().Add(time.Hour)))

	tokenStr := signToken(t, kp, tok)

	if err := v.validate(t.Context(), tokenStr); err == nil {
		t.Error("expected wrong audience to be rejected, got nil error")
	}
}

// TestOIDCInvalidSignature verifies that a token signed with a different key is rejected.
func TestOIDCInvalidSignature(t *testing.T) {
	kp := generateTestKeyPair(t)          // key pair used in JWKS
	differentKP := generateTestKeyPair(t) // key pair used for signing (not in JWKS)
	srv := newJWKSServer(t, kp.jwks)
	defer srv.Close()

	issuer := "https://issuer.example.com"
	audience := "kubezap"

	v := newOIDCValidator(srv.URL, issuer, audience)

	// Sign with the key that is NOT in the JWKS served by the mock server.
	tok := buildToken(t, jwt.NewBuilder().
		Issuer(issuer).
		Audience([]string{audience}).
		Subject("user-123").
		Expiration(time.Now().Add(time.Hour)))

	tokenStr := signToken(t, differentKP, tok)

	if err := v.validate(t.Context(), tokenStr); err == nil {
		t.Error("expected invalid signature to be rejected, got nil error")
	}
}
