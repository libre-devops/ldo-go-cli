package cli

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
)

// services answers as each service does without a sign-in: Defender with a 401.
func services(request *http.Request) httpfake.Reply {
	switch request.URL.Host {
	case "api.securitycenter.microsoft.com", "api.security.microsoft.com", "api-gov.securitycenter.microsoft.us":
		return httpfake.Status(401, map[string]any{})
	case "unreachable.corp.example":
		return httpfake.Reply{Err: errors.New("dial tcp: connection refused")}
	}
	return httpfake.JSON(map[string]any{})
}

func TestNetworkTestReachesEachService(t *testing.T) {
	h := newHarness(t, services)
	out := h.ok("network", "test")
	contains(t, out, "Proxy         none: direct", "No proxy      localhost, 127.0.0.1, ::1, 169.254.169.254",
		"Certificates  this machine's store", "Entra ID sign-in        direct  ok      HTTP 200 in 0.00s",
		"Defender for Endpoint   direct  ok      HTTP 401 in 0.00s (401 without a token is the expected answer)")
	contains(t, h.err.String(), "4 of 4 reachable")
	var hosts []string
	for _, seen := range h.transport.Seen() {
		hosts = append(hosts, seen.URL.String())
	}
	contains(t, strings.Join(hosts, "\n"), "https://login.microsoftonline.com/common/v2.0/.well-known/openid-configuration",
		"https://graph.microsoft.com/v1.0/", "https://management.azure.com/metadata/endpoints?api-version=2022-09-01")
}

func TestNetworkTestSaysWhatFailedAndExitsForAttention(t *testing.T) {
	h := newHarness(t, services)
	h.fails(3, "network", "test", "--url", "https://unreachable.corp.example/health", "-o", "json")
	var data map[string]any
	if err := json.Unmarshal(h.out.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	endpoints := data["endpoints"].([]any)
	last := endpoints[len(endpoints)-1].(map[string]any)
	if last["ok"] != false || last["detail"] != "no connection" || last["status"] != nil || last["route"] != "local" && last["route"] != "none" {
		t.Errorf("%v", last)
	}
	contains(t, h.err.String(), "warning: https://unreachable.corp.example/health: ", "4 of 5 reachable")
	contains(t, usageError(h.fails(2, "network", "test", "--url", "http://corp.example")), "is not an https URL")
	contains(t, usageError(h.fails(2, "network", "test", "--timeout", "0.5")), "--timeout")
}

func TestNetworkTestShowsTheProxyWithoutItsPassword(t *testing.T) {
	h := newHarness(t, services)
	bundle := filepath.Join(t.TempDir(), "corp.pem")
	_ = os.WriteFile(bundle, []byte("-----BEGIN CERTIFICATE-----\nMA==\n-----END CERTIFICATE-----\n"), 0o600)
	h.config(strings.Replace(testConfig, "[microsoft]", "proxy = \"http://ana:secret@proxy.corp.example:8080\"\n"+
		"no_proxy = [\"corp.example\", \"localhost\"]\nca_bundle = \""+bundle+"\"\n\n[microsoft]", 1))
	out := h.ok("network", "test")
	contains(t, out, "Proxy         http://ana:***@proxy.corp.example:8080 (config)",
		"No proxy      localhost, 127.0.0.1, ::1, 169.254.169.254, corp.example",
		"Certificates  this machine's store, and 1 from ca_bundle ("+bundle+")",
		"Microsoft Graph         http://ana:***@proxy.corp.example:8080  ok")
	if strings.Contains(out+h.err.String(), "secret") {
		t.Error("the proxy's password was shown")
	}
	h.env["LDO_CA_BUNDLE"] = bundle
	contains(t, h.ok("network", "test"), "Certificates  "+bundle+", used as it is (LDO_CA_BUNDLE)")
	h.env["LDO_PROXY_ADDRESS"] = "ftp://proxy.corp.example"
	contains(t, h.fails(1, "network", "test"), "error: ")
}

func TestNetworkTestTriesEachServiceNowInstance(t *testing.T) {
	h := newHarness(t, services)
	h.env["SNOW_INSTANCE_URL"] = "dev1"
	h.config(testConfig + "\n[servicenow.profiles.one]\ninstance = \"dev1\"\nauth = \"basic\"\n" +
		"\n[servicenow.profiles.two]\ninstance = \"https://itsm.corp.example\"\n")
	out := h.ok("network", "test")
	contains(t, out, "ServiceNow dev1.service-now.com  direct  ok", "ServiceNow itsm.corp.example")
	if strings.Count(out, "dev1.service-now.com") != 1 {
		t.Error("an instance was tried twice")
	}
	h.config(testConfig + "\n[servicenow]\ncolour = \"blue\"\n")
	contains(t, h.ok("network", "test"), "Defender for Endpoint")
}

func TestNetworkTestUsesTheProfilesCloud(t *testing.T) {
	extra := testConfig + "\n[microsoft.profiles.gov]\ntenant_id = \"" + otherTenant + "\"\ncloud = \"usgov\"\n" +
		"\n[microsoft.profiles.regional]\ntenant_id = \"" + otherTenant + "\"\nmde_url = \"https://eu.api.security.microsoft.com\"\n"
	urls := func(args ...string) string {
		h := newHarness(t, services)
		h.config(extra)
		h.run(args...)
		var found []string
		for _, seen := range h.transport.Seen() {
			found = append(found, seen.URL.String())
		}
		return strings.Join(found, "\n") + "\n" + h.err.String()
	}
	contains(t, urls("network", "test", "-p", "gov"), "https://login.microsoftonline.us/common/", "https://graph.microsoft.us/v1.0/",
		"https://api-gov.securitycenter.microsoft.us/api/")
	contains(t, urls("network", "test", "-p", "regional"), "https://eu.api.security.microsoft.com/api/")
	contains(t, urls("network", "test", "-p", "nowhere"), "warning: testing the public cloud: ", "https://graph.microsoft.com/v1.0/")
}
