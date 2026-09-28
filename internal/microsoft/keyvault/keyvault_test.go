package keyvault

import (
	"context"
	"testing"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/tokenfake"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

var now = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

func TestVaultURLsStayOnTheCloudsDomain(t *testing.T) {
	for value, want := range map[string]string{"KV-App-Prd": "https://kv-app-prd.vault.azure.net",
		"https://kv1.vault.azure.net/secrets": "https://kv1.vault.azure.net"} {
		if got, err := VaultURL(value, microsoft.Public); err != nil || got != want {
			t.Errorf("%s: %s %v", value, got, err)
		}
	}
	for _, bad := range []string{"https://kv1.vault.azure.net.example.test", "http://kv1.vault.azure.net", "a", "kv_1", "https://kv1.vault.azure.cn"} {
		if _, err := VaultURL(bad, microsoft.Public); !errs.Is(err, errs.Input) {
			t.Errorf("%s: %v", bad, err)
		}
	}
	if got, _ := VaultURL("kv1", microsoft.China); got != "https://kv1.vault.azure.cn" {
		t.Error(got)
	}
}

func TestItemsLeaveOutManagedOnesAndNeverAValue(t *testing.T) {
	soon := float64(now.Add(10 * 24 * time.Hour).Unix())
	httpClient, _ := httpfake.Client(httpfake.Routes(
		httpfake.Route{Match: "GET /secrets", Reply: httpfake.JSON(map[string]any{"value": []any{
			map[string]any{"id": "https://kv1.vault.azure.net/secrets/Zeta", "attributes": map[string]any{"exp": soon, "enabled": true}},
			map[string]any{"id": "https://kv1.vault.azure.net/secrets/cert1", "managed": true},
			map[string]any{"id": "https://kv1.vault.azure.net/secrets/alpha/", "contentType": "text/plain", "attributes": map[string]any{
				"exp": float64(now.Add(-36 * time.Hour).Unix()), "enabled": false}},
		}})},
		httpfake.Route{Match: "GET /keys", Reply: httpfake.JSON(map[string]any{"value": []any{
			map[string]any{"kid": "https://kv1.vault.azure.net/keys/k1", "attributes": map[string]any{"exp": float64(now.Add(-36 * time.Hour).Unix())}},
		}})},
	))
	tokens := &tokenfake.Provider{}
	client, err := New(microsoft.API{Tokens: tokens, TenantID: "t", Cloud: microsoft.Public, HTTPClient: httpClient}, "kv1")
	if err != nil {
		t.Fatal(err)
	}
	items, err := client.Items(context.Background(), []string{"secret", "key"})
	if err != nil || len(items) != 3 || items[0].Name != "alpha" || items[2].Kind != "key" || items[0].ContentType != "text/plain" {
		t.Fatalf("%+v %v", items, err)
	}
	if tokens.Asked()[0] != "https://vault.azure.net t" {
		t.Error(tokens.Asked())
	}
	soonest := Expiring(items, 30*24*time.Hour, now, true, false)
	if len(soonest) != 2 || soonest[0].Name != "k1" {
		t.Errorf("%+v", soonest)
	}
	if days, _ := soonest[0].DaysLeft(now); days != -2 {
		t.Error(days)
	}
	if len(Expiring(items, 30*24*time.Hour, now, true, true)) != 3 || len(Expiring(items, 30*24*time.Hour, now, false, false)) != 1 {
		t.Error("filters")
	}
	if _, ok := (Item{}).DaysLeft(now); ok {
		t.Error("no expiry has days")
	}
}
