package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/fakes/atlassianfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/graphfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/snowfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/terraformfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/tokenfake"
)

// -o json is a contract with scripts. Every command's shape (its keys and the type of
// each value, nested) is recorded in testdata/json-output.json, and a change to one fails
// here until it is recorded on purpose with LDO_RECORD_JSON_OUTPUT=1 (just record-json)
// and noted in the changelog.

const shapesFile = "testdata/json-output.json"

type shapeCase struct {
	args    []string
	handler func() httpfake.Handler
	stdin   string
	// with builds the handler from the test, for fixtures read from files.
	with func(t *testing.T) func() httpfake.Handler
	// env is the environment the command sees.
	env map[string]string
	// setup readies the harness, and is more arguments, left out of the recorded name
	// (a folder made for the test).
	setup func(t *testing.T, h *harness) []string
}

// exportFolder is a folder to export to.
func exportFolder(t *testing.T, _ *harness) []string {
	return []string{"--out", filepath.Join(t.TempDir(), "export")}
}

// terraformModule is a module to sort or document, with the tools on PATH.
func terraformModule(t *testing.T, h *harness) []string {
	tools := terraformfake.New("terraform", "terraform-docs")
	h.rt.LookPath, h.rt.Runner = tools.LookPath, tools
	return []string{terraformfake.WriteModule(t, filepath.Join(t.TempDir(), "module"))}
}

// siteShapes is a fake Atlassian site, for the jira and confluence commands.
func siteShapes() httpfake.Handler { return atlassianfake.New().Handler }

// snowShapes is a fake ServiceNow instance, for the snow commands.
func snowShapes() httpfake.Handler { return snowfake.New().Handler }

func graphObject(path string, body map[string]any) func() httpfake.Handler {
	return func() httpfake.Handler {
		return httpfake.Routes(httpfake.Route{Match: "GET " + path, Reply: httpfake.JSON(body)})
	}
}

var shapeCases = []shapeCase{
	{args: []string{"profiles"}},
	{args: []string{"az", "whoami"}},
	{args: []string{"graph", "whoami"}, handler: graphObject("/v1.0/me", map[string]any{"id": "u1", "displayName": "Ana"})},
	{args: []string{"graph", "token"}},
	{args: []string{"graph", "get", "users"}, handler: graphObject("/v1.0/users", graphfake.Page(graphfake.User(1, "ana@corp.example", "Ana")))},
	{args: []string{"graph", "get-user", "ana@corp.example"}, handler: graphObject("/v1.0/users/", graphfake.User(1, "ana@corp.example", "Ana"))},
	{args: []string{"graph", "get-device", "web01"}, handler: graphObject("/v1.0/devices", graphfake.Page(graphfake.Device(1, "web01", testNow)))},
	{args: []string{"graph", "get-group", "Linux servers"}, handler: graphObject("/v1.0/groups", graphfake.Page(graphfake.Group(1, "Linux servers", false)))},
	{args: []string{"graph", "get-app", "billing-api"}, handler: graphObject("/v1.0/applications", graphfake.Page(map[string]any{
		"id": graphfake.ID(1), "appId": graphfake.ID(2), "displayName": "billing-api", "signInAudience": "AzureADMyOrg"}))},
	{args: []string{"graph", "get-sp", "billing-api"}, handler: graphObject("/v1.0/servicePrincipals", graphfake.Page(map[string]any{
		"id": graphfake.ID(1), "appId": graphfake.ID(2), "displayName": "billing-api", "servicePrincipalType": "Application"}))},
	{args: []string{"graph", "hunt", "DeviceInfo"}, handler: func() httpfake.Handler {
		return httpfake.Routes(httpfake.Route{Match: "POST /v1.0/security/runHuntingQuery", Reply: httpfake.JSON(map[string]any{
			"schema": []any{map[string]any{"name": "DeviceName"}}, "results": []any{map[string]any{"DeviceName": "web01"}}})})
	}},
	{args: []string{"entra", "devices", "web01", "--group", "Servers"}, handler: entraDirectory},
	{args: []string{"entra", "device-groups", "web01"}, handler: entraDirectory},
	{args: []string{"entra", "group-devices", "Servers"}, handler: entraDirectory},
	{args: []string{"entra", "group-members", "Servers"}, handler: entraDirectory},
	{args: []string{"entra", "user-groups", "ana@corp.example"}, handler: entraDirectory},
	{args: []string{"entra", "user-roles", "ana@corp.example"}, handler: entraDirectory},
	{args: []string{"entra", "sign-ins"}, handler: graphObject("/v1.0/auditLogs/signIns", graphfake.Page(map[string]any{
		"userPrincipalName": "ana@corp.example", "status": map[string]any{"errorCode": 0}}))},
	{args: []string{"entra", "app-credentials", "--all"}, handler: graphObject("/v1.0/applications", graphfake.Page(map[string]any{
		"displayName": "Payroll", "passwordCredentials": []any{map[string]any{"displayName": "ci", "endDateTime": "2027-01-01T00:00:00Z"}}}))},
	{args: []string{"entra", "ca-policies"}, handler: graphObject("/v1.0/identity/conditionalAccess/policies",
		graphfake.Page(map[string]any{"displayName": "MFA", "state": "enabled"}))},
	{args: []string{"entra", "token", "graph"}},
	{args: []string{"xdr", "machines", "web01.corp.example", "nope"}, handler: defender},
	{args: []string{"xdr", "stale"}, handler: defender},
	{args: []string{"xdr", "alerts"}, handler: defender},
	{args: []string{"xdr", "vulns", "web01.corp.example"}, handler: defender},
	{args: []string{"xdr", "indicators"}, handler: defender},
	{args: []string{"xdr", "hunt", "DeviceInfo", "--endpoint"}, handler: defender},
	{args: []string{"xdr", "timeline", "web01"}, handler: defender},
	{args: []string{"xdr", "incidents", "top"}, handler: incidentQueue},
	{args: []string{"xdr", "incidents", "latest"}, handler: incidentQueue},
	{args: []string{"xdr", "incidents", "list"}, handler: incidentQueue},
	{args: []string{"xdr", "incidents", "summary"}, handler: incidentQueue},
	{args: []string{"xdr", "incidents", "show", "42"}, handler: incidentQueue},
	{args: []string{"xdr", "detections", "list"}, with: detectionRules},
	{args: []string{"xdr", "detections", "show", "42"}, with: detectionRules},
	{args: []string{"intune", "devices", "laptop-042", "laptop-099"}, handler: intuneDevicesFake},
	{args: []string{"azure", "subscriptions"}, handler: arm},
	{args: []string{"azure", "resource-graph", "resources"}, handler: arm},
	{args: []string{"azure", "rbac", "ana@corp.example"}, handler: arm},
	{args: []string{"azure", "secure-score"}, handler: arm},
	{args: []string{"azure", "recommendations"}, handler: arm},
	{args: []string{"azure", "defender-plans"}, handler: arm},
	{args: []string{"azure", "parse-id", "/subscriptions/22222222-2222-2222-2222-222222222222/resourceGroups/rg/providers/Microsoft.Web/sites/app"}},
	{args: []string{"azure", "automation", "accounts"}, handler: arm},
	{args: []string{"azure", "automation", "jobs", "aa-ops"}, handler: arm},
	{args: []string{"azure", "automation", "logs", "aa-ops"}, handler: arm},
	{args: []string{"keyvault", "expiry", "kv-app"}, handler: vaultsAndWorkspaces},
	{args: []string{"logs", "query", "Heartbeat", "-w", testWorkspace}, handler: vaultsAndWorkspaces},
	{args: []string{"logs", "ingestion", "-w", testWorkspace}, handler: vaultsAndWorkspaces},
	{args: []string{"pim", "eligible"}, handler: pimTenant(false)},
	{args: []string{"pim", "active"}, handler: pimTenant(false)},
	{args: []string{"pim", "requests"}, handler: pimTenant(false)},
	{args: []string{"pim", "approvals"}, handler: pimTenant(false)},
	{args: []string{"pim", "settings", "Global Administrator"}, handler: pimTenant(false)},
	{args: []string{"logicapp", "check", "testdata/logicapps/code-view.json"}},
	{args: []string{"logicapp", "params", "testdata/logicapps/code-view.json"}},
	{args: []string{"logicapp", "references", "testdata/logicapps/references.json"}},
	{args: []string{"logicapp", "connections", "testdata/logicapps/code-view.json"}},
	{args: []string{"logicapp", "order", "testdata/logicapps/flows"}},
	{args: []string{"logicapp", "diff", "testdata/logicapps/arm.json", "testdata/logicapps/bare.json"}},
	{args: []string{"logicapp", "validate", "testdata/logicapps/arm.json", "-g", "rg-int"}, handler: logicAppsInAzure},
	{args: []string{"logicapp", "export", "-g", "rg-int", "--name", "router"}, handler: logicAppsInAzure, setup: exportFolder},
	{args: []string{"news", "messages"}, handler: (&board{}).handler},
	{args: []string{"news", "message", "MC3"}, handler: (&board{}).handler},
	{args: []string{"planner", "plans"}, handler: (&board{}).handler},
	{args: []string{"planner", "buckets", "Operations"}, handler: (&board{}).handler},
	{args: []string{"planner", "tasks", "Operations"}, handler: (&board{}).handler},
	{args: []string{"planner", "add-news", "Operations", "--bucket", "Message Center"}, handler: (&board{}).handler},
	{args: []string{"planner", "add-rollup", "Operations", "--bucket", "Message Center"}, handler: (&board{}).handler},
	{args: []string{"devices", "check", "web01", "web02", "--intune"}, handler: fleet(nil)},
	{args: []string{"devices", "watch", "web01", "--max-passes", "1"}, handler: fleet(nil)},
	{args: []string{"devices", "show", "web01", "--intune"}, handler: fleet(nil)},
	{args: []string{"devices", "av-signature", "web01", "web02", "--at-least", "1.0"}, handler: fleet(nil)},
	{args: []string{"network", "test", "--url", "https://unreachable.corp.example/"}, handler: func() httpfake.Handler { return services }},
	{args: []string{"snow", "whoami"}, handler: snowShapes, env: snowOAuth},
	{args: []string{"snow", "token"}, handler: snowShapes, env: snowOAuth},
	{args: []string{"snow", "instance"}, handler: snowShapes, env: snowOAuth},
	{args: []string{"snow", "apps", "--all"}, handler: snowShapes, env: snowOAuth},
	{args: []string{"jira", "whoami"}, handler: siteShapes, env: atlassianfake.Env},
	{args: []string{"jira", "issues"}, handler: siteShapes, env: atlassianfake.Env},
	{args: []string{"jira", "issue", "OPS-1"}, handler: siteShapes, env: atlassianfake.Env},
	{args: []string{"jira", "projects"}, handler: siteShapes, env: atlassianfake.Env},
	{args: []string{"confluence", "spaces"}, handler: siteShapes, env: atlassianfake.Env},
	{args: []string{"confluence", "pages"}, handler: siteShapes, env: atlassianfake.Env},
	{args: []string{"confluence", "page", "101"}, handler: siteShapes, env: atlassianfake.Env},
	{args: []string{"confluence", "search", "type = page"}, handler: siteShapes, env: atlassianfake.Env},
	{args: []string{"terraform", "sort"}, setup: terraformModule},
	{args: []string{"terraform", "docs"}, setup: terraformModule},
	{args: []string{"xdr", "analyzer", filepath.Join(analyzerZips, "MDEClientAnalyzerResult.zip"), filepath.Join(analyzerZips, "mde_support.zip")}},
	{args: []string{"entra", "inspect-token"}, stdin: tokenfake.JWT(userClaims(testTenant))},
}

// shape is a JSON value's shape: an object's keys and their shapes, a list's items
// merged into one shape, or a scalar's type.
func shape(value any) any {
	switch v := value.(type) {
	case map[string]any:
		found := map[string]any{}
		for key, item := range v {
			found[key] = shape(item)
		}
		return found
	case []any:
		var merged any
		for _, item := range v {
			merged = merge(merged, shape(item))
		}
		if merged == nil {
			return []any{}
		}
		return []any{merged}
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	}
	return "null"
}

// merge is two shapes as one: objects' keys joined, differing scalars as "a|b".
func merge(a, b any) any {
	if a == nil {
		return b
	}
	objectA, isObjectA := a.(map[string]any)
	objectB, isObjectB := b.(map[string]any)
	if isObjectA && isObjectB {
		joined := map[string]any{}
		for key, value := range objectA {
			joined[key] = value
		}
		for key, value := range objectB {
			joined[key] = merge(joined[key], value)
		}
		return joined
	}
	listA, isListA := a.([]any)
	listB, isListB := b.([]any)
	if isListA && isListB {
		if len(listA) == 0 {
			return listB
		}
		if len(listB) == 0 {
			return listA
		}
		return []any{merge(listA[0], listB[0])}
	}
	textA, _ := json.Marshal(a)
	textB, _ := json.Marshal(b)
	if string(textA) == string(textB) {
		return a
	}
	kinds := strings.Split(strings.Trim(string(textA), `"`)+"|"+strings.Trim(string(textB), `"`), "|")
	sort.Strings(kinds)
	var unique []string
	for _, kind := range kinds {
		if len(unique) == 0 || unique[len(unique)-1] != kind {
			unique = append(unique, kind)
		}
	}
	return strings.Join(unique, "|")
}

func TestJSONOutputShapes(t *testing.T) {
	found := map[string]any{}
	for _, test := range shapeCases {
		name := strings.ReplaceAll(strings.Join(test.args, " "), analyzerZips+string(filepath.Separator), "")
		var handler httpfake.Handler
		if test.with != nil {
			test.handler = test.with(t)
		}
		if test.handler != nil {
			handler = test.handler()
		}
		h := newHarness(t, handler)
		h.stdin = test.stdin
		for key, value := range test.env {
			h.env[key] = value
		}
		args := test.args
		if test.setup != nil {
			args = append(slices.Clone(args), test.setup(t, h)...)
		}
		code := h.run(append(args, "-o", "json")...)
		if code != ExitOK && code != ExitAttention {
			t.Errorf("%s exited %d: %s", name, code, h.err)
			continue
		}
		var value any
		if err := json.Unmarshal(h.out.Bytes(), &value); err != nil {
			t.Errorf("%s is not JSON: %v\n%s", name, err, h.out)
			continue
		}
		found[name] = shape(value)
	}
	encoded, err := json.MarshalIndent(found, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("LDO_RECORD_JSON_OUTPUT") != "" {
		if err := os.WriteFile(filepath.FromSlash(shapesFile), append(encoded, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	recorded, err := os.ReadFile(filepath.FromSlash(shapesFile))
	if err != nil {
		t.Fatalf("no recorded shapes: run just record-json (%v)", err)
	}
	var want map[string]any
	if err := json.Unmarshal(recorded, &want); err != nil {
		t.Fatal(err)
	}
	for name, got := range found {
		wantText, _ := json.Marshal(want[name])
		gotText, _ := json.Marshal(got)
		if string(wantText) != string(gotText) {
			t.Errorf("the JSON of %q changed; if that is meant, record it with just record-json and note it in the changelog\nwant %s\ngot  %s",
				name, wantText, gotText)
		}
	}
	for name := range want {
		if _, ok := found[name]; !ok {
			t.Errorf("%q is recorded but no longer run", name)
		}
	}
}
