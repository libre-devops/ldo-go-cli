// Package tokenfake is a token provider for tests, and JWTs built from claims, so a test
// never signs in anywhere.
package tokenfake

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sync"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/auth"
)

// Provider gives Token (or a JWT of Claims) for every resource, and records each ask.
type Provider struct {
	Token   string
	Claims  map[string]any
	Expires time.Time
	Err     error
	mu      sync.Mutex
	asked   []string
}

// GetToken is the token, recording resource and tenant.
func (p *Provider) GetToken(_ context.Context, resource, tenantID string) (auth.AccessToken, error) {
	p.mu.Lock()
	p.asked = append(p.asked, resource+" "+tenantID)
	p.mu.Unlock()
	if p.Err != nil {
		return auth.AccessToken{}, p.Err
	}
	token := p.Token
	if p.Claims != nil {
		token = JWT(p.Claims)
	}
	if token == "" {
		token = "fake-token"
	}
	expires := p.Expires
	if expires.IsZero() {
		expires = time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return auth.AccessToken{Token: token, ExpiresOn: expires, TenantID: tenantID, Resource: resource}, nil
}

// Asked is every "resource tenant" a token was asked for, in order.
func (p *Provider) Asked() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.asked...)
}

// JWT is an unsigned token carrying claims, as the decoder reads one.
func JWT(claims map[string]any) string {
	header, _ := json.Marshal(map[string]any{"alg": "none", "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	encode := base64.RawURLEncoding.EncodeToString
	return encode(header) + "." + encode(payload) + ".signature"
}
