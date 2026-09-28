// Package microsoft is the shared Microsoft layer: clouds, profiles, resource ids,
// workspaces, tokens, and the Graph and Resource Manager client bases every feature
// package reads through.
package microsoft

import (
	"slices"
	"strings"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
)

// Cloud is the endpoints of one Microsoft cloud. MDEURL is empty where Defender is
// absent.
type Cloud struct {
	Name        string
	Description string
	LoginURL    string
	GraphURL    string
	ARMURL      string
	// ARMClassicURL is the older Resource Manager audience some tokens still carry.
	ARMClassicURL   string
	MDEURL          string
	LogAnalyticsURL string
	KeyVaultSuffix  string
}

// RequireMDE is the Defender for Endpoint API URL, or a Config error where this cloud
// has none.
func (c Cloud) RequireMDE() (string, error) {
	if c.MDEURL == "" {
		return "", errs.Configf("Defender for Endpoint is not available in the %s cloud", c.Name)
	}
	return c.MDEURL, nil
}

// ARMAudience is Resource Manager's token audience: its URL with the trailing slash.
func (c Cloud) ARMAudience() string { return strings.TrimRight(c.ARMURL, "/") + "/" }

// The clouds.
var (
	Public = Cloud{
		Name: "public", Description: "Azure public cloud",
		LoginURL: "https://login.microsoftonline.com", GraphURL: "https://graph.microsoft.com",
		ARMURL: "https://management.azure.com", ARMClassicURL: "https://management.core.windows.net",
		MDEURL: "https://api.securitycenter.microsoft.com", LogAnalyticsURL: "https://api.loganalytics.io",
		KeyVaultSuffix: "vault.azure.net",
	}
	USGov = Cloud{
		Name: "usgov", Description: "Azure US Government (GCC High)",
		LoginURL: "https://login.microsoftonline.us", GraphURL: "https://graph.microsoft.us",
		ARMURL: "https://management.usgovcloudapi.net", ARMClassicURL: "https://management.core.usgovcloudapi.net",
		MDEURL: "https://api-gov.securitycenter.microsoft.us", LogAnalyticsURL: "https://api.loganalytics.us",
		KeyVaultSuffix: "vault.usgovcloudapi.net",
	}
	China = Cloud{
		Name: "china", Description: "Azure operated by 21Vianet",
		LoginURL: "https://login.chinacloudapi.cn", GraphURL: "https://microsoftgraph.chinacloudapi.cn",
		ARMURL: "https://management.chinacloudapi.cn", ARMClassicURL: "https://management.core.chinacloudapi.cn",
		LogAnalyticsURL: "https://api.loganalytics.azure.cn", KeyVaultSuffix: "vault.azure.cn",
	}
)

// Clouds are every cloud, by name.
var Clouds = []Cloud{Public, USGov, China}

// CloudNames are the clouds' names, in order.
func CloudNames() []string {
	names := make([]string, len(Clouds))
	for index, cloud := range Clouds {
		names[index] = cloud.Name
	}
	return names
}

// GetCloud is a cloud by name, or a Config error listing the known names.
func GetCloud(name string) (Cloud, error) {
	wanted := strings.ToLower(strings.TrimSpace(name))
	index := slices.IndexFunc(Clouds, func(cloud Cloud) bool { return cloud.Name == wanted })
	if index < 0 {
		return Cloud{}, errs.Configf("unknown cloud %q", name).WithHint("use one of %s", strings.Join(CloudNames(), ", "))
	}
	return Clouds[index], nil
}
