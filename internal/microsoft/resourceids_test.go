package microsoft

import (
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
)

const sub = "22222222-2222-2222-2222-222222222222"

func TestAResourceIsReadByItsParts(t *testing.T) {
	id, err := ParseResourceID("/subscriptions/" + sub + "/resourceGroups/rg-app/providers/Microsoft.KeyVault/vaults/kv-app/secrets/db")
	if err != nil {
		t.Fatal(err)
	}
	if id.Type() != "Microsoft.KeyVault/vaults/secrets" || id.Name() != "db" || id.ResourceGroup != "rg-app" ||
		id.Scope() != "/subscriptions/"+sub+"/resourceGroups/rg-app" || !id.IsType("microsoft.keyvault/VAULTS/secrets") {
		t.Errorf("%+v", id)
	}
	parts := id.Parts()
	if parts["resource_type"] != "secrets" || parts["parent_resources"].(map[string]any)["vaults"] != "kv-app" ||
		parts["subscription_id"] != sub || parts["management_group_name"] != nil {
		t.Errorf("%v", parts)
	}
}

func TestSubscriptionsGroupsAndManagementGroups(t *testing.T) {
	cases := map[string][3]string{
		"/subscriptions/" + sub:                                    {"Microsoft.Resources/subscriptions", sub, ""},
		"subscriptions/" + sub + "/resourceGroups/rg-app/":         {"Microsoft.Resources/resourceGroups", "rg-app", "/subscriptions/" + sub},
		"/providers/Microsoft.Management/managementGroups/mg-root": {"Microsoft.Management/managementGroups", "mg-root", ""},
	}
	for text, want := range cases {
		id, err := ParseResourceID(text)
		if err != nil {
			t.Fatalf("%s: %v", text, err)
		}
		if id.Type() != want[0] || id.Name() != want[1] || id.Scope() != want[2] {
			t.Errorf("%s: %s %s %s", text, id.Type(), id.Name(), id.Scope())
		}
	}
}

func TestAnExtensionResourceIsOnItsParent(t *testing.T) {
	vm := "/subscriptions/" + sub + "/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/web01"
	id, err := ParseResourceID(vm + "/providers/Microsoft.Authorization/roleAssignments/ra1")
	if err != nil {
		t.Fatal(err)
	}
	if id.Scope() != vm || id.Type() != "Microsoft.Authorization/roleAssignments" || id.Parts()["resource_scope"] != vm {
		t.Errorf("%+v", id)
	}
}

func TestBadIDsSayWhatIsWrong(t *testing.T) {
	for _, text := range []string{
		"rg-app", "/subscriptions/not-a-guid", "/subscriptions/" + sub + "/resourceGroups",
		"/subscriptions/" + sub + "/resourceGroups/rg/providers/Microsoft.Web",
		"/subscriptions/" + sub + "/resourceGroups/rg/providers/Microsoft.Web/sites",
		"/subscriptions/" + sub + "/resourceGroups/rg/things/x",
		"/subscriptions/" + sub + "/resourceGroups/../providers/A/b/c",
		"/subscriptions/" + sub + "/resourceGroups/rg?x=1",
		"/subscriptions",
	} {
		if _, err := ParseResourceID(text); !errs.Is(err, errs.Input) {
			t.Errorf("%s: %v", text, err)
		}
		if _, ok := TryParseResourceID(text); ok {
			t.Errorf("%s parsed", text)
		}
	}
}

func TestWorkspaceNames(t *testing.T) {
	workspace := "/subscriptions/" + sub + "/resourceGroups/rg/providers/Microsoft.OperationalInsights/workspaces/law-soc"
	cases := map[string]string{
		"AAAAAAAA-1111-2222-3333-444444444444": WorkspaceID,
		workspace:                              WorkspaceResourceID,
		"law-soc":                              WorkspaceName,
	}
	for text, kind := range cases {
		ref, err := WorkspaceRefOf(text)
		if err != nil || ref.Kind != kind {
			t.Errorf("%s: %+v %v", text, ref, err)
		}
	}
	for _, text := range []string{"/subscriptions/" + sub + "/resourceGroups/rg", "-bad-", "a"} {
		if _, err := WorkspaceRefOf(text); !errs.Is(err, errs.Input) {
			t.Errorf("%s: %v", text, err)
		}
	}
}

func TestClouds(t *testing.T) {
	if cloud, err := GetCloud(" USGov "); err != nil || cloud.GraphURL != "https://graph.microsoft.us" {
		t.Errorf("%v %v", cloud, err)
	}
	if _, err := GetCloud("mars"); !errs.Is(err, errs.Config) {
		t.Errorf("%v", err)
	}
	if _, err := China.RequireMDE(); !errs.Is(err, errs.Config) {
		t.Errorf("%v", err)
	}
	if url, err := Public.RequireMDE(); err != nil || url == "" {
		t.Errorf("%v", err)
	}
	if Public.ARMAudience() != "https://management.azure.com/" || len(CloudNames()) != 3 {
		t.Error(Public.ARMAudience())
	}
}
