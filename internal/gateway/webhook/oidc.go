package webhook

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

const jwksCacheTTL = 5 * time.Minute

// oidcValidator validates JWT tokens against a remote JWKS endpoint.
// It caches the JWKS keyset for jwksCacheTTL to avoid fetching on every request.
type oidcValidator struct {
	jwksURL     string
	issuer      string
	audience    string
	cachedSet   jwk.Set
	cacheExpiry time.Time
	mu          sync.Mutex
}

// newOIDCValidator creates a new oidcValidator for the given JWKS URL, issuer, and audience.
// The issuer and audience are optional but recommended for production use.
func newOIDCValidator(jwksURL, issuer, audience string) *oidcValidator {
	return &oidcValidator{
		jwksURL:  jwksURL,
		issuer:   issuer,
		audience: audience,
	}
}

// fetchJWKS fetches the JWKS from the remote endpoint and caches the result.
// The caller must hold v.mu.
func (v *oidcValidator) fetchJWKS(ctx context.Context) error {
	set, err := jwk.Fetch(ctx, v.jwksURL)
	if err != nil {
		return fmt.Errorf("fetching JWKS from %s: %w", v.jwksURL, err)
	}
	v.cachedSet = set
	v.cacheExpiry = time.Now().Add(jwksCacheTTL)
	return nil
}

// getKeyset returns the cached keyset, refreshing it if expired or not yet loaded.
func (v *oidcValidator) getKeyset(ctx context.Context) (jwk.Set, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.cachedSet == nil || time.Now().After(v.cacheExpiry) {
		if err := v.fetchJWKS(ctx); err != nil {
			return nil, err
		}
	}
	return v.cachedSet, nil
}

// validate validates a JWT token string. It verifies the signature against the JWKS,
// and checks iss/aud claims if configured. Expiry is validated automatically by jwt.Parse.
// On signature verification failure the JWKS is re-fetched once to handle key rotation.
func (v *oidcValidator) validate(ctx context.Context, tokenString string) error {
	keyset, err := v.getKeyset(ctx)
	if err != nil {
		return fmt.Errorf("unable to load JWKS: %w", err)
	}

	parseOpts := []jwt.ParseOption{
		jwt.WithKeySet(keyset),
		jwt.WithValidate(true),
	}
	if v.issuer != "" {
		parseOpts = append(parseOpts, jwt.WithIssuer(v.issuer))
	}
	if v.audience != "" {
		parseOpts = append(parseOpts, jwt.WithAudience(v.audience))
	}

	token, err := jwt.ParseString(tokenString, parseOpts...)
	if err != nil {
		// On failure, attempt a single key-rotation retry with a fresh JWKS fetch.
		v.mu.Lock()
		fetchErr := v.fetchJWKS(ctx)
		freshSet := v.cachedSet
		v.mu.Unlock()

		if fetchErr != nil {
			// Return the original parse error — the refetch is a best-effort rotation attempt.
			return fmt.Errorf("token validation failed: %w", err)
		}

		retryOpts := []jwt.ParseOption{
			jwt.WithKeySet(freshSet),
			jwt.WithValidate(true),
		}
		if v.issuer != "" {
			retryOpts = append(retryOpts, jwt.WithIssuer(v.issuer))
		}
		if v.audience != "" {
			retryOpts = append(retryOpts, jwt.WithAudience(v.audience))
		}

		token, err = jwt.ParseString(tokenString, retryOpts...)
		if err != nil {
			return fmt.Errorf("token validation failed: %w", err)
		}
	}

	// Suppress unused variable warning — token is parsed and validated above.
	_ = token
	return nil
}
