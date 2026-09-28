package cli

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/fakes/snowfake"
)

var (
	snowBasic = map[string]string{"SNOW_INSTANCE_URL": snowfake.Instance, "SNOW_INSTANCE_USERNAME": snowfake.Username,
		"SNOW_INSTANCE_PASSWORD": snowfake.Password}
	snowOAuth = map[string]string{"SNOW_INSTANCE_URL": snowfake.Instance, "SNOW_INSTANCE_USERNAME": snowfake.Username,
		"SNOW_INSTANCE_PASSWORD": snowfake.Password, "SNOW_CLIENT_ID": snowfake.ClientID, "SNOW_CLIENT_SECRET": snowfake.ClientSecret}
)

// snowHarness is a harness whose HTTP goes to instance, with env set.
func snowHarness(t *testing.T, instance *snowfake.Fake, env map[string]string) *harness {
	t.Helper()
	h := newHarness(t, instance.Handler)
	for key, value := range env {
		h.env[key] = value
	}
	return h
}

func TestSnowWhoamiWithBasicFromTheEnvironment(t *testing.T) {
	h := snowHarness(t, snowfake.New(), snowBasic)
	out := h.ok("snow", "whoami")
	contains(t, out, "User      ana (Ana Analyst)", "Roles     admin, itil", "Sign-in   basic\n",
		"instance release and applications  yes")
}

func TestSnowWhoamiAsJSONSaysWhichFeaturesTheRolesCover(t *testing.T) {
	h := snowHarness(t, snowfake.New(), snowBasic)
	var record map[string]any
	if err := json.Unmarshal([]byte(h.ok("snow", "whoami", "-o", "json")), &record); err != nil {
		t.Fatal(err)
	}
	roles, _ := json.Marshal(record["roles"])
	covers, _ := json.Marshal(record["covers"])
	if string(roles) != `["admin","itil"]` || string(covers) != `{"instance release and applications":true}` || record["sign_in"] != nil {
		t.Error(record)
	}
}

func TestSnowOAuthFromTheEnvironmentSignsInWithThePasswordOnce(t *testing.T) {
	instance := snowfake.New()
	h := snowHarness(t, instance, snowOAuth)
	contains(t, h.ok("snow", "whoami"), "oauth (password)")
	// The next command needs no password: the kept refresh token signs in.
	delete(h.env, "SNOW_INSTANCE_PASSWORD")
	h.ok("snow", "whoami")
	if got := strings.Join(instance.Grants(), ","); got != "password,refresh_token" {
		t.Error(got)
	}
}

var snowLink = regexp.MustCompile(`https://\S+/oauth_auth\.do\?\S+`)

func TestSnowSignInInABrowserOnAHeadlessMachine(t *testing.T) {
	instance := snowfake.New()
	h := snowHarness(t, instance, nil)
	h.config(testConfig + "\n[servicenow]\ndefault_profile = \"work\"\n\n[servicenow.profiles.work]\ninstance = \"" +
		snowfake.Instance + "\"\nclient_id = \"" + snowfake.ClientID + "\"\n")
	var asked []string
	h.rt.Interactive = func() bool { return true }
	h.rt.HasBrowser = func() bool { return false }
	h.rt.Ask = func(question string, hide bool) (string, error) {
		asked = append(asked, question)
		if strings.Contains(question, "Client secret") {
			return snowfake.ClientSecret, nil
		}
		// The fake person opens the link the command showed, and signs in.
		return instance.Approve(snowLink.FindString(h.err.String()), false)
	}
	h.ok("snow", "sign-in")
	contains(t, h.err.String(), "Signed in to dev12345.service-now.com as ana (Ana Analyst). The sign-in is kept (file); "+
		"'ldo-go snow sign-out -p work' forgets it.")
	contains(t, strings.Join(instance.Grants(), ","), "authorization_code")
	contains(t, strings.Join(asked, "\n"), "The address you landed on")
	// Kept, with the typed-in secret, so later commands need nothing in the environment.
	h.rt.Interactive = func() bool { return false }
	h.ok("snow", "whoami")
}

func TestSnowWithoutAKeptSignInAndNoTerminalSaysToSignIn(t *testing.T) {
	h := snowHarness(t, snowfake.New(), map[string]string{"SNOW_INSTANCE_URL": snowfake.Instance,
		"SNOW_CLIENT_ID": snowfake.ClientID, "SNOW_CLIENT_SECRET": snowfake.ClientSecret})
	contains(t, h.fails(1, "snow", "whoami"), "signing in needs a browser", "'ldo-go snow sign-in -p env'")
}

func TestSnowTokenShowsTheExpiryAndRawPrintsOnlyTheToken(t *testing.T) {
	instance := snowfake.New()
	h := snowHarness(t, instance, snowOAuth)
	var record map[string]any
	if err := json.Unmarshal([]byte(h.ok("snow", "token", "-o", "json")), &record); err != nil {
		t.Fatal(err)
	}
	if record["sign_in_kept"] != true || record["token_cache"] != "file" || record["expires_on"] != "2026-09-24T12:29:59Z" {
		t.Error(record)
	}
	contains(t, h.ok("snow", "token"), "env      dev12345.service-now.com  2026-09-24 12:29 (in 29m 59s)  yes")
	if raw := strings.TrimSpace(h.ok("snow", "token", "--raw")); !instance.Issued(raw) {
		t.Error(raw)
	}
}

func TestSnowTokenForABasicProfileExplainsThereIsNone(t *testing.T) {
	h := snowHarness(t, snowfake.New(), snowBasic)
	contains(t, h.fails(1, "snow", "token"), "has no token", "SNOW_CLIENT_ID")
}

func TestSnowSignOutForgetsTheKeptSignIn(t *testing.T) {
	h := snowHarness(t, snowfake.New(), snowOAuth)
	h.ok("snow", "whoami")
	h.ok("snow", "sign-out")
	contains(t, h.err.String(), "Forgot the sign-in kept for env (dev12345.service-now.com).")
	h.ok("snow", "sign-out")
	contains(t, h.err.String(), "No sign-in was kept for env.")
	basic := snowHarness(t, snowfake.New(), snowBasic)
	basic.ok("snow", "sign-out")
	contains(t, basic.err.String(), "nothing is kept")
}

func TestSnowInstanceReportsTheReleaseAndExits3WithoutSIR(t *testing.T) {
	instance := snowfake.New()
	h := snowHarness(t, instance, snowBasic)
	h.fails(3, "snow", "instance")
	contains(t, h.out.String(), "dev12345.service-now.com  Zurich patch10  not installed")
	contains(t, h.err.String(), "Activate Plugin")
	instance.Tables["sys_db_object"] = append(instance.Tables["sys_db_object"], snowfake.Row{"name": "sn_si_incident"})
	var record map[string]any
	if err := json.Unmarshal([]byte(h.ok("snow", "instance", "-o", "json")), &record); err != nil {
		t.Fatal(err)
	}
	if record["security_incident_response"].(map[string]any)["installed"] != true || record["family"] != "Zurich" {
		t.Error(record)
	}
	instance.Tables["sys_properties"] = nil
	contains(t, h.ok("snow", "instance"), "unknown")
}

func TestSnowAppsAreSearchedAndInactiveOnesShownWithAll(t *testing.T) {
	h := snowHarness(t, snowfake.New(), snowBasic)
	h.ok("snow", "apps", "acme")
	contains(t, h.err.String(), "0 application(s)")
	contains(t, h.ok("snow", "plugins", "acme", "--all"), "Acme tools  x_acme_tools  custom app  inactive  1.0.0")
	contains(t, h.ok("snow", "apps", "--sort", "NAME:desc"), "Vulnerability Response")
}

func TestSnowWithNoProfileAtAllSaysHowToSetOneUp(t *testing.T) {
	h := snowHarness(t, snowfake.New(), nil)
	contains(t, h.fails(1, "snow", "whoami"), "no ServiceNow profile selected", "SNOW_INSTANCE_URL")
	contains(t, h.fails(1, "snow", "whoami", "-p", "prod"), `unknown ServiceNow profile "prod"`, "config init")
}

func TestSnowSignInOnABasicProfileSaysHowToUseOAuth(t *testing.T) {
	h := snowHarness(t, snowfake.New(), snowBasic)
	contains(t, h.fails(1, "snow", "sign-in"), "which sends the password each time", "SNOW_CLIENT_ID")
}

func TestSnowProfilesAreChosenByNameDefaultOrTheOnlyOne(t *testing.T) {
	h := snowHarness(t, snowfake.New(), snowBasic)
	section := "\n[servicenow.profiles.work]\ninstance = \"" + snowfake.Instance + "\"\nauth = \"basic\"\nusername = \"ana\"\n"
	h.config(testConfig + "\n[servicenow]\n" + section)
	// The env profile is there beside the configured one, and chosen first without a name.
	contains(t, h.ok("snow", "whoami"), "Profile   env")
	contains(t, h.ok("snow", "whoami", "-p", "work"), "Profile   work")
	h.env["LDO_SNOW_PROFILE"] = "work"
	contains(t, h.ok("snow", "whoami"), "Profile   work")
	delete(h.env, "LDO_SNOW_PROFILE")
	contains(t, h.fails(1, "snow", "whoami", "-p", "prod"), `unknown ServiceNow profile "prod" (configured: work)`)
	delete(h.env, "SNOW_INSTANCE_URL")
	contains(t, h.ok("snow", "whoami"), "Profile   work")
	h.config(testConfig + "\n[servicenow]\ndefault_profile = \"dev\"\n\n[servicenow.profiles.dev]\ninstance = \"dev00000\"\n")
	contains(t, h.fails(1, "snow", "whoami"), "placeholder instance")
	h.config(testConfig + "\n[servicenow]\ncolour = \"blue\"\n")
	contains(t, h.fails(1, "snow", "whoami"), "unknown key")
	h.env["SNOW_INSTANCE_URL"] = "http://dev1.service-now.com"
	h.config(testConfig)
	contains(t, h.fails(1, "snow", "whoami"), "SNOW_INSTANCE_URL: instance must be")
}

func TestProfilesListsTheServiceNowOnesAndWhetherEachCanSignIn(t *testing.T) {
	h := snowHarness(t, snowfake.New(), snowOAuth)
	h.config(testConfig + "\n[servicenow]\ndefault_profile = \"work\"\n\n[servicenow.profiles.work]\ninstance = \"" + snowfake.Instance +
		"\"\nauth = \"basic\"\nusername = \"ana\"\ndescription = \"Work\"\n\n[servicenow.profiles.vault]\ninstance = \"dev2\"\n" +
		"client_id = \"c\"\ntoken_cache = \"keychain\"\n\n[servicenow.profiles.lost]\ninstance = \"dev3\"\n")
	out := h.ok("profiles")
	for _, row := range []string{
		`servicenow +lost +dev3\.service-now\.com +oauth \(browser\) +no +-`,
		`servicenow +vault +dev2\.service-now\.com +oauth \(browser\) +\? +-`,
		`servicenow +work \(default\) +dev12345\.service-now\.com +basic +yes +Work`,
		`servicenow +env +dev12345\.service-now\.com +oauth \(password\) +no +from SNOW_INSTANCE_URL`,
	} {
		if !regexp.MustCompile(row).MatchString(out) {
			t.Errorf("no row %s in:\n%s", row, out)
		}
	}
	// Signed in once, the env profile's sign-in is kept.
	h.ok("snow", "whoami", "-p", "env")
	var records []map[string]any
	if err := json.Unmarshal([]byte(h.ok("profiles", "-o", "json")), &records); err != nil {
		t.Fatal(err)
	}
	last := records[len(records)-1]
	if last["name"] != "env" || last["signed_in"] != true || last["sign_in"] != "password" || last["vendor"] != "servicenow" {
		t.Error(last)
	}
	h.config(testConfig + "\n[servicenow]\ncolour = \"blue\"\n")
	contains(t, h.fails(1, "profiles"), "unknown key")
}
