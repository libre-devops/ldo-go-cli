// Package keyvault reads Key Vault secret, certificate and key metadata, for expiry
// checks.
//
// Only list operations are used, and they return attributes (dates, enabled, content
// type) but never a secret value. Reading them needs a data-plane role such as Key Vault
// Reader, or a list permission in an access policy; there are no token scopes to declare.
// Finding vaults across subscriptions is Resource Graph's job: this takes vault names.
package keyvault

import (
	"context"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/httpx"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

// APIVersion is the Key Vault data-plane version read.
const APIVersion = "7.4"

var vaultName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9-]{1,22}[a-zA-Z0-9]$`)

// Kinds are the kinds of item a vault holds, in the order they are listed.
var Kinds = []string{"secret", "certificate", "key"}

// VaultURL is the data-plane URL for a vault given by name or by URL, checked against the
// cloud: a URL must be on the cloud's Key Vault domain, so a token is never sent elsewhere.
func VaultURL(vault string, cloud microsoft.Cloud) (string, error) {
	value := strings.TrimSpace(vault)
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		host := ""
		if err == nil {
			host = strings.ToLower(parsed.Hostname())
		}
		if err != nil || parsed.Scheme != "https" || !strings.HasSuffix(host, "."+cloud.KeyVaultSuffix) {
			return "", errs.Inputf("'%s' is not a Key Vault URL in the %s cloud", vault, cloud.Name).
				WithHint("expected https://<name>.%s", cloud.KeyVaultSuffix)
		}
		return "https://" + host, nil
	}
	if !vaultName.MatchString(value) {
		return "", errs.Inputf("not a Key Vault name: '%s'", vault)
	}
	return "https://" + strings.ToLower(value) + "." + cloud.KeyVaultSuffix, nil
}

// Item is one secret, certificate or key, by its attributes. It never holds a value.
type Item struct {
	Vault       string
	Kind        string
	Name        string
	Enabled     *bool
	Expires     time.Time
	NotBefore   time.Time
	Updated     time.Time
	ContentType string
	Raw         fields.Object
}

// DaysLeft is whole days until expiry (negative once expired), and false with none set.
func (i Item) DaysLeft(now time.Time) (int, bool) {
	if i.Expires.IsZero() {
		return 0, false
	}
	span := i.Expires.Sub(now)
	days := int(span / (24 * time.Hour))
	// Whole days rounded down, as Python's timedelta.days is.
	if span < 0 && span%(24*time.Hour) != 0 {
		days--
	}
	return days, true
}

func epoch(value any) time.Time {
	if seconds, ok := value.(float64); ok {
		return time.Unix(int64(seconds), 0).UTC()
	}
	return time.Time{}
}

// ItemFrom is an item's metadata as Key Vault lists it; never its value.
func ItemFrom(vault, kind string, data fields.Object) Item {
	attributes := fields.Map(data["attributes"])
	identifier := fields.Text(data, "kid")
	if identifier == "" {
		identifier = fields.Text(data, "id")
	}
	identifier = strings.TrimRight(identifier, "/")
	var enabled *bool
	if flag, ok := attributes["enabled"].(bool); ok {
		enabled = &flag
	}
	return Item{Vault: vault, Kind: kind, Name: identifier[strings.LastIndex(identifier, "/")+1:], Enabled: enabled,
		Expires: epoch(attributes["exp"]), NotBefore: epoch(attributes["nbf"]), Updated: epoch(attributes["updated"]),
		ContentType: fields.Text(data, "contentType"), Raw: data}
}

// Client lists one vault's metadata.
type Client struct {
	API   *httpx.Client
	Vault string
}

// New is a client for one vault (a name or URL) in api's tenant and cloud.
func New(api microsoft.API, vault string) (*Client, error) {
	address, err := VaultURL(vault, api.Cloud)
	if err != nil {
		return nil, err
	}
	host := strings.TrimPrefix(address, "https://")
	client, err := api.Client("Key Vault "+host, address, "https://"+api.Cloud.KeyVaultSuffix, nil)
	if err != nil {
		return nil, err
	}
	name, _, _ := strings.Cut(host, ".")
	return &Client{API: client, Vault: name}, nil
}

// Items is the secrets, certificates and keys in the vault, by kind then name. A
// certificate's backing secret and key are managed, and left out, so each certificate is
// listed once.
func (c *Client) Items(ctx context.Context, kinds []string) ([]Item, error) {
	var found []Item
	for _, kind := range kinds {
		items, err := httpx.Collect(c.API.All(ctx, "/"+kind+"s", url.Values{"api-version": {APIVersion}}, "nextLink"))
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if item["managed"] != true {
				found = append(found, ItemFrom(c.Vault, kind, item))
			}
		}
	}
	sort.SliceStable(found, func(a, b int) bool {
		kindA, kindB := slices.Index(Kinds, found[a].Kind), slices.Index(Kinds, found[b].Kind)
		if kindA != kindB {
			return kindA < kindB
		}
		return strings.ToLower(found[a].Name) < strings.ToLower(found[b].Name)
	})
	return found, nil
}

// Expiring is the items that expire within within of now (and expired ones), soonest
// first. Disabled items are left out unless asked for: nothing can use them anyway.
func Expiring(items []Item, within time.Duration, now time.Time, includeExpired, includeDisabled bool) []Item {
	var selected []Item
	for _, item := range items {
		if !item.Expires.IsZero() && item.Expires.Sub(now) <= within && (includeExpired || !item.Expires.Before(now)) &&
			(includeDisabled || item.Enabled == nil || *item.Enabled) {
			selected = append(selected, item)
		}
	}
	sort.SliceStable(selected, func(a, b int) bool { return selected[a].Expires.Before(selected[b].Expires) })
	return selected
}
