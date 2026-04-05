package webhook

import (
	"context"
	"fmt"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

const (
	// jwksRefreshInterval is how often the background goroutine re-fetches each JWKS URL.
	// Must be larger than jwksCacheWindow (the frequency at which the cache checks for due refreshes).
	jwksRefreshInterval = 15 * time.Minute

	// jwksMinRefreshInterval is the floor enforced even when HTTP Cache-Control headers
	// indicate a shorter TTL.
	jwksMinRefreshInterval = 30 * time.Second

	// jwksCacheWindow is how often jwk.Cache checks whether any registered URLs are due
	// for a background refresh. Must be <= jwksRefreshInterval.
	jwksCacheWindow = time.Minute
)

// oidcValidator validates JWT tokens against a remote JWKS endpoint.
// It uses a shared jwk.Cache for background refresh and deduplication across
// multiple routes that share the same JWKS URL.
type oidcValidator struct {
	jwksURL  string
	issuer   string
	audience string
	cache    *jwk.Cache
}

// newOIDCValidator creates an oidcValidator backed by the provided shared jwk.Cache.
// The cache must already have the JWKS URL registered (see RegisterJWKSURL).
func newOIDCValidator(jwksURL, issuer, audience string, cache *jwk.Cache) *oidcValidator {
	return &oidcValidator{
		jwksURL:  jwksURL,
		issuer:   issuer,
		audience: audience,
		cache:    cache,
	}
}

// NewJWKSCache creates the shared jwk.Cache used by all oidcValidators in this gateway.
// The context must remain live for the duration of the gateway; cancelling it stops background refreshes.
func NewJWKSCache(ctx context.Context) *jwk.Cache {
	return jwk.NewCache(ctx, jwk.WithRefreshWindow(jwksCacheWindow))
}

// RegisterJWKSURL registers a JWKS URL with the shared cache if not already registered.
// Safe to call multiple times for the same URL — jwk.Cache is idempotent on re-registration.
// The initial fetch is best-effort: if the IdP is temporarily unavailable at registration time
// the route is still registered and the cache will populate on the first request.
func RegisterJWKSURL(ctx context.Context, cache *jwk.Cache, jwksURL string) error {
	if err := cache.Register(jwksURL,
		jwk.WithRefreshInterval(jwksRefreshInterval),
		jwk.WithMinRefreshInterval(jwksMinRefreshInterval),
	); err != nil {
		return fmt.Errorf("registering JWKS URL %s: %w", jwksURL, err)
	}
	// Attempt an initial fetch to warm the cache. Log but do not fail on error — the IdP
	// may not be reachable yet (e.g. Dex starting up) and the cache will retry on first use.
	if _, err := cache.Refresh(ctx, jwksURL); err != nil {
		// Caller should log this as a warning; we surface it via a wrapped sentinel
		// so the caller can distinguish a hard error from a soft pre-warm failure.
		return &jwksPrewarmError{url: jwksURL, cause: err}
	}
	return nil
}

// jwksPrewarmError is returned when JWKS registration succeeds but the initial cache warm
// fails. Routes should still be registered — the cache will populate on the first request.
type jwksPrewarmError struct {
	url   string
	cause error
}

func (e *jwksPrewarmError) Error() string {
	return fmt.Sprintf("initial JWKS fetch from %s (route still registered): %v", e.url, e.cause)
}

func (e *jwksPrewarmError) Unwrap() error { return e.cause }

// validate validates a JWT token string. It verifies the signature against the JWKS
// retrieved from the shared cache, and checks iss/aud claims if configured.
// Expiry is validated automatically by jwt.Parse.
// On signature verification failure the cache is force-refreshed once to handle key rotation.
func (v *oidcValidator) validate(ctx context.Context, tokenString string) error {
	keyset, err := v.cache.Get(ctx, v.jwksURL)
	if err != nil {
		return fmt.Errorf("unable to load JWKS: %w", err)
	}

	parseOpts := v.buildParseOpts(keyset)
	_, err = jwt.ParseString(tokenString, parseOpts...)
	if err != nil {
		// On failure, force-refresh once to handle key rotation then retry.
		freshSet, refreshErr := v.cache.Refresh(ctx, v.jwksURL)
		if refreshErr != nil {
			// Return the original parse error — the refresh is a best-effort rotation attempt.
			return fmt.Errorf("token validation failed: %w", err)
		}
		if _, retryErr := jwt.ParseString(tokenString, v.buildParseOpts(freshSet)...); retryErr != nil {
			return fmt.Errorf("token validation failed: %w", retryErr)
		}
	}
	return nil
}

// buildParseOpts assembles the jwt.ParseOption slice for the given keyset.
func (v *oidcValidator) buildParseOpts(keyset jwk.Set) []jwt.ParseOption {
	opts := []jwt.ParseOption{
		jwt.WithKeySet(keyset),
		jwt.WithValidate(true),
	}
	if v.issuer != "" {
		opts = append(opts, jwt.WithIssuer(v.issuer))
	}
	if v.audience != "" {
		opts = append(opts, jwt.WithAudience(v.audience))
	}
	return opts
}
