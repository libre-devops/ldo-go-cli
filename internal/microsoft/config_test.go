package microsoft

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/libre-devops/ldo-go-cli/internal/core/config"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
)

func parse(t *testing.T, text string) (*Config, error) {
	t.Helper()
	var data config.Table
	if _, err := toml.Decode(text, &data); err != nil {
		t.Fatal(err)
	}
	file, err := config.Parse(data, "config.toml", []string{Section})
	if err != nil {
		t.Fatal(err)
	}
	return FromFile(file)
}

func TestProfilesAreRead(t *testing.T) {
	ms, err := parse(t, `
[microsoft]
default_profile = "dev"
[microsoft.profiles.dev]
tenant_id = "`+tenant+`"
subscription_id = "`+sub+`"
description = "Development"
workspace = "law-soc"
[microsoft.profiles.gov]
tenant_id = "`+tenant+`"
cloud = "usgov"
auth = "device-code"
token_cache = "memory"
mde_url = "https://api-gov.securitycenter.microsoft.us"
`)
	if err != nil {
		t.Fatal(err)
	}
	dev, _ := ms.Get("dev")
	gov, _ := ms.Get("gov")
	if ms.DefaultProfile != "dev" || dev.Kind() != "subscription" || dev.Auth != "azure-cli" || dev.Workspace != "law-soc" ||
		dev.Delegated() || dev.TokenCache != "file" {
		t.Errorf("%+v", dev)
	}
	if gov.Cloud.Name != "usgov" || gov.Kind() != "tenant" || !gov.Delegated() || gov.TokenCache != "memory" {
		t.Errorf("%+v", gov)
	}
	if strings.Join(ms.Names(), ",") != "dev,gov" {
		t.Error(ms.Names())
	}
	if _, err := ms.Get("prod"); !errs.Is(err, errs.Config) || !strings.Contains(err.Error(), "configured: dev, gov") {
		t.Errorf("%v", err)
	}
}

func TestProfileMistakesAreConfigErrors(t *testing.T) {
	base := "[microsoft.profiles.p]\ntenant_id = \"" + tenant + "\"\n"
	cases := map[string]string{
		"no section":        "",
		"no profiles":       "[microsoft]\n",
		"unknown key":       base + "tenantid = \"x\"\n",
		"no tenant":         "[microsoft.profiles.p]\ndescription = \"x\"\n",
		"bad guid":          "[microsoft.profiles.p]\ntenant_id = \"nope\"\n",
		"bad auth":          base + "auth = \"password\"\n",
		"secret no client":  base + "auth = \"client-secret\"\n",
		"cache for az":      base + "token_cache = \"memory\"\n",
		"bad cache":         base + "auth = \"interactive\"\ntoken_cache = \"disk\"\n",
		"bad cloud":         base + "cloud = \"mars\"\n",
		"http mde":          base + "mde_url = \"http://example.test\"\n",
		"both workspaces":   base + "workspace = \"law-soc\"\nworkspace_id = \"" + tenant + "\"\n",
		"workspace as arm":  base + "workspace_id = \"/subscriptions/" + sub + "/resourceGroups/rg\"\n",
		"bad workspace":     base + "workspace = \"-\"\n",
		"unknown default":   "[microsoft]\ndefault_profile = \"q\"\n" + base,
		"bad profile name":  "[microsoft.profiles.\"has space\"]\ntenant_id = \"" + tenant + "\"\n",
		"profile not table": "[microsoft]\nprofiles = { p = 1 }\n",
	}
	for name, text := range cases {
		if _, err := parse(t, text); !errs.Is(err, errs.Config) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestPlaceholderIDsMustBeReplaced(t *testing.T) {
	ms, err := parse(t, "[microsoft.profiles.p]\ntenant_id = \""+PlaceholderID+"\"\n")
	if err != nil {
		t.Fatal(err)
	}
	profile, _ := ms.Get("p")
	if !profile.HasPlaceholderIDs() || !errs.Is(profile.RequireRealIDs(), errs.Config) {
		t.Errorf("%+v", profile)
	}
}

func TestTheTemplateParses(t *testing.T) {
	ms, err := parse(t, ConfigTemplate)
	if err != nil || ms.DefaultProfile != "prod-tenant" || len(ms.Profiles) != 4 {
		t.Errorf("%v %v", ms, err)
	}
}
