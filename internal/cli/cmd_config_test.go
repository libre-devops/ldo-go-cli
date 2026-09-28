package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/fakes/azfake"
)

func TestConfigInitWritesATemplateOnlyYouCanRead(t *testing.T) {
	h := newHarness(t, nil)
	h.rt.ConfigPath = filepath.Join(t.TempDir(), "nested", "config.toml")
	h.ok("config", "init")
	info, err := os.Stat(h.rt.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", info.Mode().Perm())
	}
	text, _ := os.ReadFile(h.rt.ConfigPath)
	contains(t, string(text), "[microsoft.profiles.prod]")
	contains(t, h.fails(1, "config", "init"), "already exists", "--force")
	h.ok("config", "init", "--force")
}

func TestConfigPathIsTheOneInUse(t *testing.T) {
	h := newHarness(t, nil)
	if strings.TrimSpace(h.ok("config", "path")) != h.rt.ConfigPath {
		t.Error(h.out.String())
	}
	h.rt.ConfigPath = ""
	h.rt.Getenv = func(name string) string { return map[string]string{"LDO_CONFIG": "/elsewhere/config.toml"}[name] }
	t.Setenv("LDO_CONFIG", "/elsewhere/config.toml")
	if strings.TrimSpace(h.ok("config", "path")) != "/elsewhere/config.toml" {
		t.Error(h.out.String())
	}
}

func TestProfilesShowWhichIsActiveAndSignedIn(t *testing.T) {
	h := newHarness(t, nil)
	out := h.ok("profiles")
	contains(t, out, "microsoft  *  dev (default)  subscription "+testSubscription, "azure-cli    yes",
		"other          tenant "+otherTenant, "device-code  n/a")
	var records []map[string]any
	if err := json.Unmarshal([]byte(h.ok("profiles", "-o", "json")), &records); err != nil {
		t.Fatal(err)
	}
	if records[0]["name"] != "dev" || records[0]["active"] != true || records[0]["default"] != true ||
		records[0]["signed_in"] != true || records[1]["signed_in"] != false {
		t.Errorf("%v", records)
	}
}

func TestProfilesWithoutAzSayWhy(t *testing.T) {
	h := newHarness(t, nil)
	h.az = azfake.New()
	h.rt.AzRunner = h.az
	contains(t, h.ok("profiles"), "azure-cli    ?")
	contains(t, h.err.String(), "cannot read Azure CLI accounts")
}

func TestProfilesWarnAboutPlaceholdersAndAnEmptyFile(t *testing.T) {
	h := newHarness(t, nil)
	h.config("[microsoft.profiles.new]\ntenant_id = \"00000000-0000-0000-0000-000000000000\"\n")
	h.ok("profiles")
	contains(t, h.err.String(), "placeholder ids in new")
	h.config("")
	h.ok("profiles")
	contains(t, h.err.String(), "no profiles configured")
}

func TestProfilesWithoutAConfigFile(t *testing.T) {
	h := newHarness(t, nil)
	h.rt.ConfigPath = filepath.Join(t.TempDir(), "missing.toml")
	contains(t, h.fails(1, "profiles"), "missing.toml")
}

func TestWithNoProfileTheAzureCLIsActiveAccountIsUsed(t *testing.T) {
	h := newHarness(t, nil)
	h.config("")
	h.ok("graph", "whoami")
	contains(t, h.out.String(), "Profile       az-active (azure-cli)")
	h.az = azfake.SignedIn()
	h.rt.AzRunner = h.az
	contains(t, h.fails(1, "graph", "whoami"), "the Azure CLI is not signed in")
}

func TestAnUnknownProfileListsTheKnownOnes(t *testing.T) {
	h := newHarness(t, nil)
	contains(t, h.fails(1, "graph", "whoami", "-p", "prod"), "unknown Microsoft profile \"prod\" (configured: dev, other)")
	h.rt.Getenv = func(name string) string { return map[string]string{"LDO_PROFILE": "nope"}[name] }
	contains(t, h.fails(1, "graph", "whoami"), `unknown Microsoft profile "nope"`)
}

func TestWelcomeVersionAndHelp(t *testing.T) {
	h := newHarness(t, nil)
	contains(t, h.ok("welcome"), "Version  ldo-go ", "Config   "+h.rt.ConfigPath+"\n", "Next\nldo-go profiles ", "ldo-go --help ")
	contains(t, h.err.String(), "Libre DevOps Helpers  ldo-go")
	h.rt.ConfigPath = filepath.Join(t.TempDir(), "none.toml")
	contains(t, h.ok("welcome"), "none.toml (not created yet)", "ldo-go config init  write a config file to fill in")
	contains(t, h.ok("--version"), "ldo-go ")
	contains(t, h.ok(), "Available Commands:")
	contains(t, h.ok("graph"), "whoami")
}

func TestUsageErrorsExitTwo(t *testing.T) {
	h := newHarness(t, nil)
	contains(t, usageError(h.fails(2, "bogus")), `unknown command "bogus"`)
	contains(t, usageError(h.fails(2, "graph", "whoami", "--nope")), "unknown flag: --nope")
	contains(t, usageError(h.fails(2, "graph", "whoami", "-o", "xml")), `"xml" is not one of`)
	contains(t, usageError(h.fails(2, "graph", "get", "users", "--sort", ":desc")), "cannot sort by")
	contains(t, usageError(h.fails(2, "--log-format", "xml", "welcome")), "--log-format")
	contains(t, h.fails(2, "graph", "get"), "hint: see 'ldo-go graph get --help'")
}

func TestStructuredLogsMakeErrorsRecords(t *testing.T) {
	h := newHarness(t, nil)
	h.fails(1, "--log-format", "json", "graph", "hunt")
	var record map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(h.err.String())), &record); err != nil {
		t.Fatalf("%v: %s", err, h.err)
	}
	if record["level"] != "error" || record["message"] != "no query given" || record["hint"] == nil {
		t.Errorf("%v", record)
	}
}

func TestHTMLIsOnePageOnStdoutWhenPiped(t *testing.T) {
	h := newHarness(t, nil)
	page := h.ok("profiles", "-o", "html")
	contains(t, page, "<!doctype html>", "Content-Security-Policy", "<td>dev (default)</td>", `class="pill ok">yes`,
		"<b>2</b><span>rows</span>", "ldo-go profiles -o html")
	if strings.Contains(page, "<script src") || strings.Contains(page, "https://cdn") {
		t.Error("the page loads something")
	}
}
