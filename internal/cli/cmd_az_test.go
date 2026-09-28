package cli

import (
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/core/process"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/azfake"
)

func TestAzWhoamiMatchesTheProfile(t *testing.T) {
	h := newHarness(t, nil)
	contains(t, h.ok("az", "whoami"), "Profile       dev", "Subscription  Dev ("+testSubscription+")", "User          ana@corp.example")
	h.az = azfake.SignedIn()
	h.rt.AzRunner = h.az
	contains(t, h.fails(1, "az", "whoami"), "the Azure CLI is not signed in")
}

func TestAzUseSwitchesAndSaysToWhat(t *testing.T) {
	h := newHarness(t, nil)
	current := testSubscription
	h.az = azfake.New(
		azfake.Route{Args: "account list", Result: azfake.JSON([]any{azfake.Account(testSubscription, testTenant, "Dev", "ana@corp.example", true),
			azfake.Account(otherTenant, otherTenant, "N/A(tenant level account)", "ana@corp.example", false)})},
		azfake.Route{Args: "account set", Func: func(args []string) process.Result {
			current = args[len(args)-1]
			return process.Result{}
		}},
		azfake.Route{Args: "account show", Func: func([]string) process.Result {
			tenant := testTenant
			if current == otherTenant {
				tenant = otherTenant
			}
			return azfake.JSON(azfake.Account(current, tenant, "x", "ana@corp.example", true))
		}},
	)
	h.rt.AzRunner = h.az
	contains(t, h.ok("az", "use", "other"), "Switched to other: tenant-level account in tenant "+otherTenant+" as ana@corp.example")
	contains(t, h.fails(1, "az", "use", "prod"), "unknown Microsoft profile")
}
