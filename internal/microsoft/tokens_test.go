package microsoft

import (
	"testing"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/tokenfake"
)

const tenant = "11111111-1111-1111-1111-111111111111"

var now = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

func claims(extra map[string]any) map[string]any {
	found := map[string]any{"aud": "https://graph.microsoft.com", "tid": tenant,
		"iss": "https://sts.windows.net/" + tenant + "/", "exp": float64(now.Add(time.Hour).Unix())}
	for key, value := range extra {
		found[key] = value
	}
	return found
}

func decode(t *testing.T, extra map[string]any) DecodedToken {
	t.Helper()
	decoded, err := DecodeToken("Bearer " + tokenfake.JWT(claims(extra)))
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func statuses(checks []Check) map[string]string {
	found := map[string]string{}
	for _, check := range checks {
		found[check.Name] = check.Status
	}
	return found
}

func TestDecodeReadsTheClaims(t *testing.T) {
	token := decode(t, map[string]any{"upn": "ana@corp.example", "scp": "User.Read Mail.Read", "appid": "app"})
	if token.TenantID() != tenant || token.Principal() != "ana@corp.example" || token.AppID() != "app" ||
		token.IdentityType() != "user" || len(token.Scopes()) != 2 || token.ExpiresAt() != now.Add(time.Hour) {
		t.Errorf("claims %v", token.Claims)
	}
	if audiences := token.Audiences(); len(audiences) != 1 || audiences[0] != "https://graph.microsoft.com" {
		t.Errorf("audiences %v", audiences)
	}
}

func TestAnAppTokenIsAnApp(t *testing.T) {
	token := decode(t, map[string]any{"roles": []any{"Device.Read.All"}, "azp": "client", "aud": []any{"a", "b"}})
	if token.IdentityType() != "app" || token.AppID() != "client" || token.Principal() != "client" || len(token.Audiences()) != 2 {
		t.Errorf("claims %v", token.Claims)
	}
	if decode(t, nil).IdentityType() != "unknown" {
		t.Error("no scp or roles should be unknown")
	}
}

func TestDecodeRefusesWhatIsNotAJWT(t *testing.T) {
	for _, value := range []string{"one.two", "a.b.c.d.e", "!!!.e30.x", "e30.bm90IGpzb24.x", "e30.WzFd.x"} {
		if _, err := DecodeToken(value); !errs.Is(err, errs.Token) {
			t.Errorf("%q: %v", value, err)
		}
	}
}

func TestAValidTokenPassesEveryCheck(t *testing.T) {
	graph := Resources(Public)["graph"]
	checks := ValidateToken(decode(t, map[string]any{"scp": "ThreatHunting.Read.All"}),
		ValidateOptions{Resource: &graph, TenantID: tenant, Now: now,
			Requirements: []Requirement{{"hunting", "graph", [][]string{{"threathunting.read.all"}}}}})
	for _, check := range checks {
		if check.Status != "pass" {
			t.Errorf("%+v", check)
		}
	}
	if !Passed(checks, true) {
		t.Error("should pass strictly")
	}
}

func TestExpiryChecks(t *testing.T) {
	cases := map[string]struct {
		exp  any
		want string
	}{
		"expired": {float64(now.Add(-time.Minute).Unix()), "fail"},
		"soon":    {float64(now.Add(4 * time.Minute).Unix()), "warn"},
		"missing": {nil, "fail"},
	}
	for name, test := range cases {
		token := decode(t, map[string]any{"exp": test.exp})
		if got := statuses(ValidateToken(token, ValidateOptions{Now: now}))["expiry"]; got != test.want {
			t.Errorf("%s: %s", name, got)
		}
	}
}

func TestNotYetValidFails(t *testing.T) {
	token := decode(t, map[string]any{"nbf": float64(now.Add(10 * time.Minute).Unix())})
	if statuses(ValidateToken(token, ValidateOptions{Now: now}))["not-before"] != "fail" {
		t.Error("nbf in the future should fail")
	}
}

func TestIssuerTenantAndAudienceMismatchesFail(t *testing.T) {
	mde := Resources(Public)["mde"]
	token := decode(t, map[string]any{"iss": "https://sts.windows.net/other/"})
	got := statuses(ValidateToken(token, ValidateOptions{Resource: &mde, TenantID: "22222222-2222-2222-2222-222222222222", Now: now}))
	if got["issuer"] != "fail" || got["tenant"] != "fail" || got["audience"] != "fail" {
		t.Errorf("%v", got)
	}
}

func TestPermissionsSayWhatIsMissing(t *testing.T) {
	graph := Resources(Public)["graph"]
	requirements := []Requirement{
		{"devices", "graph", [][]string{{"Device.Read.All", "Directory.Read.All"}}},
		{"users", "graph", [][]string{{"User.Read.All"}, {"AuditLog.Read.All"}}},
		{"machines", "mde", [][]string{{"Machine.Read.All"}}},
	}
	checks := ValidateToken(decode(t, map[string]any{"scp": "Directory.Read.All User.Read.All"}),
		ValidateOptions{Resource: &graph, Requirements: requirements, Now: now})
	got := map[string]Check{}
	for _, check := range checks {
		got[check.Name] = check
	}
	if got["permissions: devices"].Detail != "via Directory.Read.All" {
		t.Errorf("%+v", got["permissions: devices"])
	}
	if got["permissions: users"].Detail != "needs one of AuditLog.Read.All" {
		t.Errorf("%+v", got["permissions: users"])
	}
	if _, found := got["permissions: machines"]; found {
		t.Error("another resource's requirement was checked")
	}
	if Passed(checks, true) || !Passed(checks, false) {
		t.Error("a warning fails only strictly")
	}
}

func TestUserImpersonationAloneRestsOnTheRole(t *testing.T) {
	mde := Resources(Public)["mde"]
	checks := ValidateToken(decode(t, map[string]any{"aud": "https://api.securitycenter.microsoft.com", "scp": "user_impersonation"}),
		ValidateOptions{Resource: &mde, Requirements: []Requirement{{"m", "mde", [][]string{{"Machine.Read.All"}}}}, Now: now})
	if statuses(checks)["permissions"] != "warn" {
		t.Errorf("%+v", checks)
	}
	none := ValidateToken(decode(t, map[string]any{"aud": "https://api.securitycenter.microsoft.com"}),
		ValidateOptions{Resource: &mde, Requirements: []Requirement{{"m", "mde", [][]string{{"Machine.Read.All"}}}}, Now: now})
	if statuses(none)["permissions"] != "warn" {
		t.Errorf("%+v", none)
	}
}

func TestRequiredPermissionsFailWhenMissing(t *testing.T) {
	checks := ValidateToken(decode(t, map[string]any{"roles": []any{"Device.Read.All"}}),
		ValidateOptions{Required: []string{"device.read.all", "User.Read.All"}, Now: now})
	got := statuses(checks)
	if got["requires device.read.all"] != "pass" || got["requires User.Read.All"] != "fail" {
		t.Errorf("%v", got)
	}
}

func TestResolveResource(t *testing.T) {
	for value, want := range map[string]string{
		"graph": "graph", "MDE": "mde", "https://management.core.windows.net/": "arm",
		"https://api.loganalytics.io": "loganalytics", "https://example.test/api": "https://example.test/api",
	} {
		found, err := ResolveResource(value, Public)
		if err != nil || found.Key != want {
			t.Errorf("%s: %v %v", value, found.Key, err)
		}
	}
	if _, err := ResolveResource("nope", Public); !errs.Is(err, errs.Input) || errs.HintOf(err) == "" {
		t.Errorf("%v", err)
	}
	if _, found := Resources(China)["mde"]; found {
		t.Error("China has no Defender for Endpoint")
	}
	if !Resources(Public)["graph"].Accepts("00000003-0000-0000-C000-000000000000") {
		t.Error("the Graph app id is its audience too")
	}
}
