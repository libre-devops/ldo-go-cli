// Package auth holds access tokens and token providers.
//
// A token provider is anything with GetToken(ctx, resource, tenantID). Each vendor layer
// supplies its own credentials (the Microsoft layer's for Entra ID), and its service
// packages accept any provider, so none of them depends on where a token comes from.
package auth

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// AccessToken is a bearer token. Its String leaves the token out, so it cannot leak into
// a log or an error.
type AccessToken struct {
	Token     string
	ExpiresOn time.Time
	TenantID  string
	Resource  string
}

func (t AccessToken) String() string {
	return fmt.Sprintf("AccessToken(resource=%q, tenant=%q, expires=%s)",
		t.Resource, t.TenantID, t.ExpiresOn.Format(time.RFC3339))
}

// GoString keeps the token out of %#v too.
func (t AccessToken) GoString() string { return t.String() }

// ExpiresWithin reports whether the token expires within window of now.
func (t AccessToken) ExpiresWithin(window time.Duration, now time.Time) bool {
	return t.ExpiresOn.Sub(now) <= window
}

// TokenProvider is anything that can produce an access token for a resource in a tenant.
type TokenProvider interface {
	GetToken(ctx context.Context, resource, tenantID string) (AccessToken, error)
}

// Invalidator is a provider that caches, and can forget a cached token.
type Invalidator interface {
	Invalidate(resource, tenantID string)
}

// Caching wraps a provider and reuses each token until it is close to expiry. The cache
// is in memory only; tokens are never written to disk. It is safe to share between
// goroutines, and a token is fetched once even when several ask at once.
type Caching struct {
	inner         TokenProvider
	refreshBefore time.Duration
	clock         func() time.Time
	mu            sync.Mutex
	cache         map[string]AccessToken
}

// NewCaching is a caching provider over inner, fetching anew five minutes before expiry.
// clock is time.Now when nil.
func NewCaching(inner TokenProvider, clock func() time.Time) *Caching {
	if clock == nil {
		clock = time.Now
	}
	return &Caching{inner: inner, refreshBefore: 5 * time.Minute, clock: clock, cache: map[string]AccessToken{}}
}

func cacheKey(resource, tenantID string) string {
	return resource + "|" + strings.ToLower(tenantID)
}

// GetToken is a cached token for resource and tenantID, or a new one when it is about to
// expire.
func (c *Caching) GetToken(ctx context.Context, resource, tenantID string) (AccessToken, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := cacheKey(resource, tenantID)
	cached, found := c.cache[key]
	if found && !cached.ExpiresWithin(c.refreshBefore, c.clock()) {
		return cached, nil
	}
	token, err := c.inner.GetToken(ctx, resource, tenantID)
	if err != nil {
		return AccessToken{}, err
	}
	c.cache[key] = token
	return token, nil
}

// Invalidate forgets the cached token, so the next request fetches a new one.
func (c *Caching) Invalidate(resource, tenantID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cache, cacheKey(resource, tenantID))
}

// Bearer is a current bearer token for one resource in one tenant, each time it is asked.
type Bearer struct {
	Provider TokenProvider
	Resource string
	TenantID string
}

// Token is the token itself, for an Authorization header.
func (b Bearer) Token(ctx context.Context) (string, error) {
	token, err := b.Provider.GetToken(ctx, b.Resource, b.TenantID)
	return token.Token, err
}

// Refresh drops the provider's cached token (when it caches), so the next call fetches a
// new one. The HTTP client calls it once when an API answers 401.
func (b Bearer) Refresh() {
	if invalidator, ok := b.Provider.(Invalidator); ok {
		invalidator.Invalidate(b.Resource, b.TenantID)
	}
}
