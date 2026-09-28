package azcli

import (
	"context"
	"strings"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/process"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/azfake"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

const (
	tenant = "11111111-1111-1111-1111-111111111111"
	subA   = "22222222-2222-2222-2222-222222222222"
	subB   = "33333333-3333-3333-3333-333333333333"
)

func cli(runner process.Runner) *CLI {
	c := New(runner, func() []string { return []string{"HTTPS_PROXY=http://proxy.corp.example:8080"} })
	c.Command.LookPath = azfake.LookPath
	return c
}

func TestAccountsAreRead(t *testing.T) {
	fake := azfake.SignedIn(azfake.Account(subA, tenant, "A", "ana@corp.example", true),
		azfake.Account(strings.ToUpper(subB), tenant, "B", "ana@corp.example", false))
	accounts, err := cli(fake).Accounts(context.Background())
	if err != nil || len(accounts) != 2 || accounts[1].ID != subB || accounts[0].User != "ana@corp.example" {
		t.Errorf("%+v %v", accounts, err)
	}
	current, found, err := cli(fake).Current(context.Background())
	if err != nil || !found || current.ID != subA {
		t.Errorf("%+v %v", current, err)
	}
}

func TestSignedOutIsNoAccount(t *testing.T) {
	fake := azfake.SignedIn()
	if _, found, err := cli(fake).Current(context.Background()); found || err != nil {
		t.Errorf("%v %v", found, err)
	}
}

func TestErrorsLoseTheirTraceback(t *testing.T) {
	stderr := "ERROR: The command failed with an unexpected error. Here is the traceback:\nERROR: boom\nTraceback (most recent call last):\n  File x"
	if CleanStderr(stderr) != "boom" {
		t.Errorf("%q", CleanStderr(stderr))
	}
	fake := azfake.New(azfake.Route{Args: "account list", Result: azfake.Failed(1, "ERROR: AADSTS70043: expired")})
	_, err := cli(fake).Accounts(context.Background())
	if !errs.Is(err, errs.Command) || !strings.Contains(errs.HintOf(err), "az login") {
		t.Errorf("%v", err)
	}
	if HintFor("Unable to get authority configuration for x") == "" || HintFor("fine") != "" {
		t.Error("hints")
	}
}

func TestFindAccountPrefersTheActiveOne(t *testing.T) {
	accounts := []Account{
		{ID: subA, TenantID: tenant, State: "Enabled"},
		{ID: tenant, TenantID: tenant},
		{ID: subB, TenantID: tenant, State: "Enabled", IsDefault: true},
	}
	tenantProfile := microsoft.Profile{Name: "t", TenantID: tenant}
	if found, ok := FindAccount(accounts, tenantProfile); !ok || found.ID != subB {
		t.Errorf("%+v", found)
	}
	accounts[2].IsDefault = false
	if found, _ := FindAccount(accounts, tenantProfile); !found.TenantLevel() {
		t.Errorf("%+v", found)
	}
	if found, ok := FindAccount(accounts, microsoft.Profile{TenantID: tenant, SubscriptionID: subA}); !ok || found.ID != subA {
		t.Errorf("%+v", found)
	}
	if _, ok := FindAccount(accounts, microsoft.Profile{TenantID: subB}); ok {
		t.Error("found an account in another tenant")
	}
	profiles := []microsoft.Profile{{Name: "t", TenantID: tenant}, {Name: "a", TenantID: tenant, SubscriptionID: subA}}
	if found, _ := MatchProfile(profiles, accounts[0]); found.Name != "a" {
		t.Error(found.Name)
	}
	if found, _ := MatchProfile(profiles, accounts[2]); found.Name != "t" {
		t.Error(found.Name)
	}
}

func TestSwitchSetsTheAccountAndChecksIt(t *testing.T) {
	current := subA
	fake := azfake.New(
		azfake.Route{Args: "account list", Result: azfake.JSON([]any{
			azfake.Account(subA, tenant, "A", "ana", true), azfake.Account(subB, tenant, "B", "ana", false)})},
		azfake.Route{Args: "account set", Func: func(args []string) process.Result {
			current = args[len(args)-1]
			return process.Result{}
		}},
		azfake.Route{Args: "account show", Func: func([]string) process.Result {
			return azfake.JSON(azfake.Account(current, tenant, "", "ana", true))
		}},
	)
	switched, err := cli(fake).Switch(context.Background(), microsoft.Profile{Name: "b", TenantID: tenant, SubscriptionID: subB}, false, false)
	if err != nil || switched.Account.ID != subB || switched.SignedIn {
		t.Errorf("%+v %v", switched, err)
	}
	if !fake.Ran("account set --subscription " + subB) {
		t.Errorf("%v", fake.Calls())
	}
}

func TestSwitchWithoutASessionSaysSignIn(t *testing.T) {
	fake := azfake.SignedIn()
	_, err := cli(fake).Switch(context.Background(), microsoft.Profile{Name: "b", TenantID: tenant}, false, false)
	if !errs.Is(err, errs.Command) || !strings.Contains(errs.HintOf(err), "az login --tenant "+tenant) {
		t.Errorf("%v", err)
	}
}

func TestSwitchSignsInWhenAsked(t *testing.T) {
	signedIn := false
	fake := azfake.New(
		azfake.Route{Args: "account list", Func: func([]string) process.Result {
			if !signedIn {
				return azfake.JSON([]any{})
			}
			return azfake.JSON([]any{azfake.Account(tenant, tenant, "N/A(tenant level account)", "ana", true)})
		}},
		azfake.Route{Args: "login", Func: func(args []string) process.Result {
			signedIn = strings.Contains(strings.Join(args, " "), "--use-device-code --allow-no-subscriptions")
			return process.Result{}
		}},
		azfake.Route{Args: "account set"},
		azfake.Route{Args: "account show", Result: azfake.JSON(azfake.Account(tenant, tenant, "", "ana", true))},
	)
	switched, err := cli(fake).Switch(context.Background(), microsoft.Profile{Name: "t", TenantID: tenant}, true, true)
	if err != nil || !switched.SignedIn {
		t.Errorf("%+v %v (%v)", switched, err, fake.Calls())
	}
}
