package atlassian

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/libre-devops/ldo-go-cli/internal/core/config"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/atlassianfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
)

func section(t *testing.T, body map[string]any) (*Config, error) {
	t.Helper()
	file, err := config.Parse(config.Table{"atlassian": body}, "config.toml", []string{Section})
	if err != nil {
		t.Fatal(err)
	}
	return FromFile(file)
}

func env(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func TestASiteIsItsHTTPSAddressOrItsName(t *testing.T) {
	for value, want := range map[string]string{"contoso": "https://contoso.atlassian.net", "https://Contoso.Atlassian.net/": "https://contoso.atlassian.net"} {
		if got, err := SiteURL(value, "x"); err != nil || got != want {
			t.Errorf("%s: %s %v", value, got, err)
		}
	}
	for _, bad := range []string{"http://contoso.atlassian.net", "https://contoso.atlassian.net/wiki", "ftp://x", "https://x/?a"} {
		if _, err := SiteURL(bad, "x"); !errs.Is(err, errs.Config) {
			t.Errorf("%s: %v", bad, err)
		}
	}
}

func TestProfilesAreReadWithTheirTokenVariable(t *testing.T) {
	parsed, err := section(t, map[string]any{"default_profile": "work", "profiles": map[string]any{
		"work": map[string]any{"site": "contoso", "email": "ana@corp.example"},
		"lab":  map[string]any{"site": "https://lab.atlassian.net", "email": "ben@corp.example", "token_env": "LAB_TOKEN"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	work, _ := parsed.Get("work")
	lab, _ := parsed.Get("lab")
	if parsed.DefaultProfile != "work" || work.Site != "https://contoso.atlassian.net" || work.TokenEnv != "JIRA_TOKEN" ||
		lab.TokenEnv != "LAB_TOKEN" || work.Host() != "contoso.atlassian.net" {
		t.Errorf("%+v %+v", work, lab)
	}
	if _, err := parsed.Get("nope"); err == nil || !strings.Contains(err.Error(), "configured: lab, work") {
		t.Error(err)
	}
	if _, err := (&Config{}).Get("nope"); err == nil || !strings.Contains(err.Error(), "configured: none") {
		t.Error(err)
	}
	file, _ := config.Parse(config.Table{}, "config.toml", []string{Section})
	if none, err := FromFile(file); none != nil || err != nil {
		t.Error(none, err)
	}
}

func TestAProfileThatCannotBeUsedSaysWhy(t *testing.T) {
	for _, test := range []struct {
		profile map[string]any
		message string
	}{
		{map[string]any{"site": "contoso"}, "needs a site and an email"},
		{map[string]any{"site": "contoso", "email": "not-an-email"}, "not an email address"},
		{map[string]any{"site": "contoso", "email": "a@corp.example", "token_env": "1BAD"}, "environment variable"},
		{map[string]any{"site": "contoso", "email": "a@corp.example", "token": "x"}, "token"},
		{map[string]any{"site": "http://contoso.atlassian.net", "email": "a@corp.example"}, "site must be"},
		{map[string]any{"site": 5, "email": "a@corp.example"}, "site must be a string"},
	} {
		if _, err := section(t, map[string]any{"profiles": map[string]any{"work": test.profile}}); err == nil || !strings.Contains(err.Error(), test.message) {
			t.Errorf("%v: %v", test.profile, err)
		}
	}
	for _, body := range []map[string]any{
		{"profiles": map[string]any{"Work": map[string]any{}}},
		{"profiles": map[string]any{"work": "contoso"}},
		{"profiles": "work"},
		{"colour": "blue"},
		{"default_profile": "lab", "profiles": map[string]any{"work": map[string]any{"site": "contoso", "email": "a@corp.example"}}},
	} {
		if _, err := section(t, body); !errs.Is(err, errs.Config) {
			t.Errorf("%v: %v", body, err)
		}
	}
}

func TestTheEnvironmentMakesTheEnvProfile(t *testing.T) {
	profile, found, err := ProfileFromEnv(env(map[string]string{"JIRA_INSTANCE": "contoso", "JIRA_EMAIL": "ana@corp.example"}))
	if !found || err != nil || profile.Name != "env" || profile.Site != "https://contoso.atlassian.net" || profile.Email != "ana@corp.example" {
		t.Error(profile, err)
	}
	if _, found, _ := ProfileFromEnv(env(nil)); found {
		t.Error("a profile from nothing")
	}
	if _, _, err := ProfileFromEnv(env(map[string]string{"JIRA_INSTANCE": "contoso"})); err == nil || !strings.Contains(err.Error(), "JIRA_EMAIL is not") {
		t.Error(err)
	}
	for _, bad := range []map[string]string{{"JIRA_INSTANCE": "http://x", "JIRA_EMAIL": "a@corp.example"}, {"JIRA_INSTANCE": "x", "JIRA_EMAIL": "a"}} {
		if _, _, err := ProfileFromEnv(env(bad)); !errs.Is(err, errs.Config) {
			t.Error(err)
		}
	}
}

func TestTheTemplateParsesAndItsPlaceholderSiteIsRefused(t *testing.T) {
	var data map[string]any
	if _, err := toml.Decode(ConfigTemplate, &data); err != nil {
		t.Fatal(err)
	}
	file, _ := config.Parse(data, "config.toml", []string{Section})
	parsed, err := FromFile(file)
	if err != nil {
		t.Fatal(err)
	}
	work, _ := parsed.Get("work")
	if err := work.RequireRealSite(); err == nil || !strings.Contains(err.Error(), "placeholder") {
		t.Error(err)
	}
	if _, err := NewClient("Jira", work, env(nil), nil); err == nil {
		t.Error("the placeholder site was used")
	}
}

func TestTheAPITokenGoesWithItsEmailAndIsNeverShown(t *testing.T) {
	token, err := NewAPIToken("ana@corp.example", "atl-token")
	if err != nil || token.Authorization() != "YW5hQGNvcnAuZXhhbXBsZTphdGwtdG9rZW4=" {
		t.Fatal(token, err)
	}
	if shown := fmt.Sprintf("%v %#v", token, token); strings.Contains(shown, "atl-token") || strings.Contains(shown, "YW5h") {
		t.Error(shown)
	}
	if _, err := NewAPIToken("", "x"); err == nil {
		t.Error("a token without an email")
	}
	if _, err := NewAPIToken("a@corp.example", ""); err == nil {
		t.Error("an empty token")
	}
	profile := Profile{Name: "work", Site: atlassianfake.Site, Email: atlassianfake.Email, TokenEnv: "LAB_TOKEN"}
	if _, err := TokenFor(profile, env(nil)); !errs.Is(err, errs.Auth) || !strings.Contains(errs.As(err).Hint, "export LAB_TOKEN=<token>") {
		t.Error(err)
	}
}

func TestTheClientSignsEachRequestAndExplainsARefusal(t *testing.T) {
	client, _ := httpfake.Client(atlassianfake.New().Handler)
	profile := Profile{Name: "work", Site: atlassianfake.Site, Email: atlassianfake.Email, TokenEnv: TokenEnv}
	api, err := NewClient("Jira", profile, env(atlassianfake.Env), client)
	if err != nil {
		t.Fatal(err)
	}
	if me, err := api.Get(context.Background(), "/rest/api/3/myself", nil); err != nil || me["accountId"] != "acc1" {
		t.Fatal(me, err)
	}
	wrong, _ := NewClient("Jira", profile, env(map[string]string{"JIRA_TOKEN": "old"}), client)
	_, err = wrong.Get(context.Background(), "/rest/api/3/myself", nil)
	if found := errs.As(err); found == nil || found.Status != 401 || !strings.Contains(found.Hint, "email and API token together") {
		t.Error(err)
	}
}
