package servicenow

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/libre-devops/ldo-go-cli/internal/core/config"
)

func parse(t *testing.T, profile map[string]any) (*Config, error) {
	t.Helper()
	settings := map[string]any{"instance": "dev12345"}
	for key, value := range profile {
		settings[key] = value
	}
	data := config.Table{"servicenow": map[string]any{"profiles": map[string]any{"dev": settings}}}
	file, err := config.Parse(data, "config.toml", []string{Section})
	if err != nil {
		t.Fatal(err)
	}
	return FromFile(file)
}

func TestAProfileDefaultsToOAuthInABrowserWithAKeptSignIn(t *testing.T) {
	parsed, err := parse(t, map[string]any{"client_id": "abc"})
	if err != nil {
		t.Fatal(err)
	}
	dev := parsed.Profiles["dev"]
	want := Profile{Name: "dev", Instance: "https://dev12345.service-now.com", Auth: "oauth", ClientID: "abc", SignIn: "browser",
		RedirectURI: DefaultRedirectURI, TokenCache: "file", PasswordEnv: "SNOW_INSTANCE_PASSWORD", ClientSecretEnv: "SNOW_CLIENT_SECRET"}
	if dev != want {
		t.Errorf("%+v", dev)
	}
}

func TestEveryKeyIsRead(t *testing.T) {
	parsed, err := parse(t, map[string]any{
		"username": "ana", "auth": "oauth", "sign_in": "password", "client_id": "abc",
		"redirect_uri": "https://tools.corp.example/callback", "token_cache": "keychain", "password_env": "WORK_SNOW_PASSWORD",
		"client_secret_env": "WORK_SNOW_SECRET", "description": "work",
	})
	if err != nil {
		t.Fatal(err)
	}
	dev := parsed.Profiles["dev"]
	if dev.Username != "ana" || dev.SignIn != "password" || dev.TokenCache != "keychain" || dev.RedirectURI != "https://tools.corp.example/callback" ||
		dev.PasswordEnv != "WORK_SNOW_PASSWORD" || dev.ClientSecretEnv != "WORK_SNOW_SECRET" || dev.Description != "work" {
		t.Errorf("%+v", dev)
	}
}

func TestInstanceAddressesAreNormalised(t *testing.T) {
	for value, want := range map[string]string{
		"dev12345":                          "https://dev12345.service-now.com",
		"https://Dev12345.service-now.com/": "https://dev12345.service-now.com",
		"https://itsm.corp.example":         "https://itsm.corp.example",
	} {
		if got, err := InstanceURL(value, "here"); err != nil || got != want {
			t.Errorf("%s: %s %v", value, got, err)
		}
	}
	for _, value := range []string{"http://dev12345.service-now.com", "https://dev12345.service-now.com/now/nav", "ftp://x", "https://x/?a=1"} {
		if _, err := InstanceURL(value, "here"); err == nil || !strings.Contains(err.Error(), "instance must be") {
			t.Errorf("%s: %v", value, err)
		}
	}
}

func TestBadProfilesAreRefused(t *testing.T) {
	for _, test := range []struct {
		profile map[string]any
		message string
	}{
		{map[string]any{"auth": "saml"}, "auth must be one of oauth, basic"},
		{map[string]any{"sign_in": "magic"}, "sign_in must be one of browser, password"},
		{map[string]any{"redirect_uri": "http://corp.example/cb"}, "redirect_uri must be"},
		{map[string]any{"auth": "basic", "sign_in": "browser"}, `apply to auth = "oauth"`},
		{map[string]any{"auth": "basic", "token_cache": "file"}, `token_cache applies to auth = "oauth"`},
		{map[string]any{"token_cache": "disk"}, "token_cache must be one of"},
		{map[string]any{"password_env": "not a name"}, "must be an environment variable name"},
		{map[string]any{"client_secret_env": "1BAD"}, "must be an environment variable name"},
		{map[string]any{"colour": "blue"}, "unknown key"},
		{map[string]any{"username": 5}, "username must be a string"},
		{map[string]any{"instance": ""}, "instance is required"},
	} {
		if _, err := parse(t, test.profile); err == nil || !strings.Contains(err.Error(), test.message) {
			t.Errorf("%v: %v", test.profile, err)
		}
	}
}

func TestTheSectionsOwnMistakes(t *testing.T) {
	for _, test := range []struct {
		section map[string]any
		message string
	}{
		{map[string]any{"default_profile": "prod", "profiles": map[string]any{"dev": map[string]any{"instance": "d1"}}}, "default_profile \"prod\""},
		{map[string]any{"profiles": "dev"}, "expected a table"},
		{map[string]any{"profiles": map[string]any{"Dev": map[string]any{"instance": "d1"}}}, "lowercase"},
		{map[string]any{"profiles": map[string]any{"dev": "d1"}}, "expected a table"},
		{map[string]any{"colour": "blue"}, "unknown key"},
	} {
		file, _ := config.Parse(config.Table{"servicenow": test.section}, "config.toml", []string{Section})
		if _, err := FromFile(file); err == nil || !strings.Contains(err.Error(), test.message) {
			t.Errorf("%v: %v", test.section, err)
		}
	}
	file, _ := config.Parse(config.Table{}, "config.toml", []string{Section})
	if parsed, err := FromFile(file); parsed != nil || err != nil {
		t.Error(parsed, err)
	}
}

func TestTheEnvironmentMakesAProfile(t *testing.T) {
	env := func(values map[string]string) func(string) string {
		return func(name string) string { return values[name] }
	}
	if _, found, _ := ProfileFromEnv(env(nil)); found {
		t.Error("a profile from nothing")
	}
	basic, _, _ := ProfileFromEnv(env(map[string]string{"SNOW_INSTANCE_URL": "https://dev1.service-now.com", "SNOW_INSTANCE_USERNAME": "admin"}))
	if basic.Name != "env" || basic.Auth != "basic" || basic.Username != "admin" {
		t.Errorf("%+v", basic)
	}
	oauth, _, _ := ProfileFromEnv(env(map[string]string{"SNOW_INSTANCE_URL": "dev1", "SNOW_CLIENT_ID": "abc", "SNOW_INSTANCE_PASSWORD": "x"}))
	if oauth.Auth != "oauth" || oauth.SignIn != "password" || oauth.Instance != "https://dev1.service-now.com" {
		t.Errorf("%+v", oauth)
	}
	browser, _, _ := ProfileFromEnv(env(map[string]string{"SNOW_INSTANCE_URL": "dev1", "SNOW_CLIENT_ID": "abc"}))
	if browser.SignIn != "browser" {
		t.Errorf("%+v", browser)
	}
	if _, _, err := ProfileFromEnv(env(map[string]string{"SNOW_INSTANCE_URL": "http://dev1.service-now.com"})); err == nil {
		t.Error("plain http was taken")
	}
}

func TestTheTemplateParsesAndItsPlaceholderIsRefused(t *testing.T) {
	var data map[string]any
	if _, err := toml.Decode(ConfigTemplate, &data); err != nil {
		t.Fatal(err)
	}
	file, err := config.Parse(data, "config.toml", []string{Section})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := FromFile(file)
	if err != nil {
		t.Fatal(err)
	}
	dev, _ := parsed.Get("dev")
	if !dev.HasPlaceholder() || dev.Host() != PlaceholderHost {
		t.Errorf("%+v", dev)
	}
	if err := dev.RequireRealInstance(); err == nil || !strings.Contains(err.Error(), "placeholder") {
		t.Error(err)
	}
	if _, err := parsed.Get("prod"); err == nil || err.Error() != `unknown ServiceNow profile "prod" (configured: dev)` {
		t.Error(err)
	}
	if err := (Profile{Instance: "https://dev1.service-now.com"}).RequireRealInstance(); err != nil {
		t.Error(err)
	}
	if _, err := (&Config{}).Get("dev"); err == nil || !strings.Contains(err.Error(), "configured: none") {
		t.Error(err)
	}
}

func TestRoleRequirementsAcceptAdminOrAnyOfTheirRoles(t *testing.T) {
	requirement := RoleRequirement{Feature: "incidents", AnyOf: []string{"sn_si.analyst"}}
	if !requirement.MetBy([]string{"ADMIN"}) || !requirement.MetBy([]string{"itil", "sn_si.Analyst"}) || requirement.MetBy([]string{"itil"}) {
		t.Error("roles")
	}
}
