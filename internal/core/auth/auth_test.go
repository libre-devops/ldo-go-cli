package auth

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

type counting struct{ calls int }

func (c *counting) GetToken(_ context.Context, resource, tenantID string) (AccessToken, error) {
	c.calls++
	return AccessToken{Token: fmt.Sprintf("t%d", c.calls), ExpiresOn: time.Unix(1000, 0).Add(time.Hour),
		TenantID: tenantID, Resource: resource}, nil
}

func TestTokensAreReusedUntilCloseToExpiry(t *testing.T) {
	inner := &counting{}
	now := time.Unix(1000, 0)
	cache := NewCaching(inner, func() time.Time { return now })
	ctx := context.Background()
	first, _ := cache.GetToken(ctx, "https://graph.microsoft.com", "TENANT")
	again, _ := cache.GetToken(ctx, "https://graph.microsoft.com", "tenant")
	if first.Token != again.Token || inner.calls != 1 {
		t.Fatalf("not reused: %d calls", inner.calls)
	}
	now = now.Add(56 * time.Minute)
	if later, _ := cache.GetToken(ctx, "https://graph.microsoft.com", "tenant"); later.Token == first.Token {
		t.Fatal("reused within five minutes of expiry")
	}
	bearer := Bearer{Provider: cache, Resource: "https://graph.microsoft.com", TenantID: "tenant"}
	bearer.Refresh()
	if token, _ := bearer.Token(ctx); token != "t3" {
		t.Fatalf("refresh did not drop the token: %s", token)
	}
}

func TestATokenNeverPrints(t *testing.T) {
	token := AccessToken{Token: "eyJ.secret", ExpiresOn: time.Unix(0, 0), TenantID: "t", Resource: "r"}
	for _, shown := range []string{fmt.Sprint(token), fmt.Sprintf("%v %+v %#v", token, token, token)} {
		if strings.Contains(shown, "secret") {
			t.Fatal(shown)
		}
	}
}
