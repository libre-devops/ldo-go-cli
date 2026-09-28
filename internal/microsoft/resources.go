package microsoft

import (
	"slices"
	"sort"
	"strings"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
)

// First-party application ids: the same in every cloud, and a token may carry one as its
// audience instead of the URL.
const (
	GraphAppID        = "00000003-0000-0000-c000-000000000000"
	MDEAppID          = "fc780465-2017-40d4-a0c5-307022471b92"
	ARMAppID          = "797f4846-ba00-4fd7-ba43-dac1f8f63013"
	LogAnalyticsAppID = "ca7f3f0b-7d91-482c-8e09-c5d840d0eac5"
	KeyVaultAppID     = "cfa8b339-82a2-471a-a3c9-0fc0be7a4093"
)

// NormaliseAudience compares audiences without case or a trailing slash.
func NormaliseAudience(value string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(value), "/"))
}

// Resource is an API as a token sees it: URL is what a token is asked for, Audiences the
// aud claims that mean it (normalised).
type Resource struct {
	Key         string
	URL         string
	Audiences   []string
	Description string
}

// Accepts reports whether aud means this resource.
func (r Resource) Accepts(aud string) bool {
	return slices.Contains(r.Audiences, NormaliseAudience(aud))
}

// Requirement is what one feature needs from a token for the resource Resource: every
// group in AllOf must be met, each by any one of its scopes or roles.
type Requirement struct {
	Feature  string
	Resource string
	AllOf    [][]string
}

func resource(key, url, description string, audiences ...string) Resource {
	normalised := []string{NormaliseAudience(url)}
	for _, audience := range audiences {
		normalised = append(normalised, NormaliseAudience(audience))
	}
	return Resource{Key: key, URL: url, Audiences: normalised, Description: description}
}

// Resources are every API this tool can call in cloud, by key.
func Resources(cloud Cloud) map[string]Resource {
	found := []Resource{
		resource("graph", cloud.GraphURL, "Microsoft Graph (Entra ID and Intune)", GraphAppID),
		resource("arm", cloud.ARMAudience(), "Azure Resource Manager", cloud.ARMClassicURL, ARMAppID),
		resource("loganalytics", cloud.LogAnalyticsURL, "Log Analytics query API", LogAnalyticsAppID),
		resource("keyvault", "https://"+cloud.KeyVaultSuffix, "Key Vault data plane", KeyVaultAppID),
	}
	if cloud.MDEURL != "" {
		found = append(found, resource("mde", cloud.MDEURL, "Defender for Endpoint API",
			"https://securitycenter.onmicrosoft.com/windowsatpservice", MDEAppID))
	}
	byKey := map[string]Resource{}
	for _, item := range found {
		byKey[item.Key] = item
	}
	return byKey
}

// ResolveResource is a resource by key (graph, mde, ...) or by an https URL. An unknown
// https URL is an ad hoc resource whose only audience is the URL itself.
func ResolveResource(value string, cloud Cloud) (Resource, error) {
	known := Resources(cloud)
	key := strings.TrimSpace(value)
	if found, ok := known[strings.ToLower(key)]; ok {
		return found, nil
	}
	if strings.HasPrefix(key, "https://") {
		for _, found := range known {
			if found.Accepts(key) {
				return found, nil
			}
		}
		return Resource{Key: key, URL: key, Audiences: []string{NormaliseAudience(key)}}, nil
	}
	keys := make([]string, 0, len(known))
	for name := range known {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	return Resource{}, errs.Inputf("unknown resource %q", value).
		WithHint("use one of %s, or an https:// resource URL", strings.Join(keys, ", "))
}
