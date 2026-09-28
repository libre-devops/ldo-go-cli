package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
)

func write(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTheSharedSettingsAreRead(t *testing.T) {
	path := write(t, `proxy = "127.0.0.1:3128"
no_proxy = "localhost, .corp.example"
ca_bundle = "~/certs/ca.pem"
[microsoft]
default_profile = "dev"
`)
	file, err := Load(path, []string{"microsoft"})
	if err != nil {
		t.Fatal(err)
	}
	if file.Proxy != "127.0.0.1:3128" || len(file.NoProxy) != 2 || file.NoProxy[1] != ".corp.example" {
		t.Fatalf("%+v", file)
	}
	if !strings.HasSuffix(file.CABundle, filepath.Join("certs", "ca.pem")) || strings.HasPrefix(file.CABundle, "~") {
		t.Fatalf("ca bundle %q", file.CABundle)
	}
	section, err := file.Section("microsoft")
	if err != nil || section["default_profile"] != "dev" {
		t.Fatalf("section %v, %v", section, err)
	}
	if missing, _ := file.Section("servicenow"); missing != nil {
		t.Fatal("a missing section")
	}
}

func TestMistakesFailLoudly(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "none.toml"), nil); !errs.Is(err, errs.ConfigNotFound) || errs.HintOf(err) == "" {
		t.Fatalf("missing file: %v", err)
	}
	if _, err := Load(write(t, "proxy = "), nil); !errs.Is(err, errs.Config) {
		t.Fatalf("bad TOML: %v", err)
	}
	_, err := Load(write(t, "[microsfot]\n"), []string{"microsoft"})
	if !errs.Is(err, errs.Config) || !strings.Contains(err.Error(), "unknown key(s) microsfot") {
		t.Fatalf("typo: %v", err)
	}
	if _, err := Load(write(t, "microsoft = 3\n"), nil); err != nil {
		t.Fatal(err)
	}
	file, _ := Load(write(t, "microsoft = 3\n"), nil)
	if _, err := file.Section("microsoft"); !errs.Is(err, errs.Config) {
		t.Fatal("a section that is not a table")
	}
}

func TestFieldReaders(t *testing.T) {
	data := Table{"id": "7C917DB0-71F2-438E-9554-388FFCAB8764", "bad": "x", "url": "https://x.example/",
		"plain": "http://x.example", "n": 3, "list": []any{"a", " b "}}
	if got, err := GUID(data, "id", "w"); err != nil || got != "7c917db0-71f2-438e-9554-388ffcab8764" {
		t.Fatalf("guid %q %v", got, err)
	}
	if _, err := GUID(data, "bad", "w"); err == nil {
		t.Fatal("bad guid")
	}
	if got, _ := GUID(data, "missing", "w"); got != "" {
		t.Fatal("missing guid")
	}
	if got, err := HTTPSURL(data, "url", "w"); err != nil || got != "https://x.example" {
		t.Fatalf("url %q %v", got, err)
	}
	if _, err := HTTPSURL(data, "plain", "w"); err == nil {
		t.Fatal("plain http")
	}
	if _, err := Text(data, "n", "w"); err == nil {
		t.Fatal("a number as text")
	}
	if got, err := List(data, "list", "w"); err != nil || got[1] != "b" {
		t.Fatalf("list %v %v", got, err)
	}
	if err := CheckName("Prod", "w"); err == nil {
		t.Fatal("upper case name")
	}
	if err := CheckName("prod-tenant_2", "w"); err != nil {
		t.Fatal(err)
	}
}

func TestTheDefaultPathFollowsTheEnvironment(t *testing.T) {
	t.Setenv("LDO_CONFIG", "~/elsewhere.toml")
	if got := DefaultPath(); strings.HasPrefix(got, "~") || !strings.HasSuffix(got, "elsewhere.toml") {
		t.Fatal(got)
	}
	t.Setenv("LDO_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg")
	if got := DefaultPath(); got != filepath.Join("/tmp/xdg", "ldo", "config.toml") && os.PathSeparator == '/' {
		t.Fatal(got)
	}
}
