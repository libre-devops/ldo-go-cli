// Package azcli is the Azure CLI as a program: which accounts it knows, which one is
// active, switching between them and signing in. Tokens come from the identity package;
// this runs az for what only az can do, on the same network as the rest of the tool (its
// proxy, and its CA bundle unless REQUESTS_CA_BUNDLE names one already).
package azcli

import (
	"context"
	"strings"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/process"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

// InstallHint says where to get the Azure CLI.
const InstallHint = "install it: https://learn.microsoft.com/cli/azure/install-azure-cli"

// az prefixes an unexpected failure with this line, then dumps a Python traceback.
const unexpected = "The command failed with an unexpected error. Here is the traceback:"

// New is the Azure CLI, run by runner (process.Exec when nil) with env added.
func New(runner process.Runner, env func() []string) *CLI {
	return &CLI{Command: &process.Command{
		Name: "az", InstallHint: InstallHint, Runner: runner, Env: env,
		CleanStderr: CleanStderr, HintFor: HintFor,
	}}
}

// CLI is the Azure CLI's accounts: list them, read the active one, switch, sign in.
type CLI struct {
	Command *process.Command
}

// RunJSON runs az with JSON output and reads it.
func (c *CLI) RunJSON(ctx context.Context, args ...string) (any, error) {
	return c.Command.RunJSON(ctx, append(args, "--output", "json", "--only-show-errors")...)
}

// CleanStderr is the error message from az stderr, without its traceback.
func CleanStderr(stderr string) string {
	var lines []string
	for _, raw := range strings.Split(stderr, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "Traceback (most recent call last)") {
			break
		}
		line = strings.TrimPrefix(line, "ERROR: ")
		if line != "" && line != unexpected {
			lines = append(lines, line)
		}
	}
	joined := strings.Join(lines, " ")
	if len(joined) > 1000 {
		return joined[:1000]
	}
	return joined
}

// SignedOut reports whether an az error means there is no usable session.
func SignedOut(message string) bool { return strings.Contains(message, "az login") }

// HintFor is a next step for a failed az command, when the message points to one.
func HintFor(detail string) string {
	if strings.Contains(detail, "Unable to get authority configuration") {
		return "no such tenant: check the tenant id (for a profile, its tenant_id in the config file)"
	}
	if SignedOut(detail) || strings.Contains(detail, "AADSTS") {
		return "the Azure CLI session is missing or expired; sign in again (az login --tenant <id>)"
	}
	return ""
}

// Account is one entry from az account list: a subscription, or a tenant-level sign-in.
type Account struct {
	ID        string
	Name      string
	TenantID  string
	State     string
	IsDefault bool
	User      string
}

// TenantLevel reports whether this is the entry az login --allow-no-subscriptions
// records for a tenant.
func (a Account) TenantLevel() bool { return a.ID == a.TenantID }

// AccountFrom is an account as az account list prints it.
func AccountFrom(data map[string]any) Account {
	return Account{
		ID: strings.ToLower(fields.Text(data, "id")), Name: fields.Text(data, "name"),
		TenantID: strings.ToLower(fields.Text(data, "tenantId")), State: fields.Text(data, "state"),
		IsDefault: fields.Bool(data["isDefault"]), User: fields.Text(fields.Map(data["user"]), "name"),
	}
}

// Current is the active account, or false when the CLI is not signed in.
func (c *CLI) Current(ctx context.Context) (Account, bool, error) {
	data, err := c.RunJSON(ctx, "account", "show")
	if err != nil {
		if SignedOut(err.Error()) {
			return Account{}, false, nil
		}
		return Account{}, false, err
	}
	object, ok := data.(map[string]any)
	if !ok {
		return Account{}, false, nil
	}
	return AccountFrom(object), true, nil
}

// Accounts is every account the CLI knows, disabled subscriptions included.
func (c *CLI) Accounts(ctx context.Context) ([]Account, error) {
	data, err := c.RunJSON(ctx, "account", "list", "--all")
	if err != nil {
		return nil, err
	}
	var accounts []Account
	for _, item := range fields.Objects(data) {
		accounts = append(accounts, AccountFrom(item))
	}
	return accounts, nil
}

// SetAccount makes subscription (an id, or a tenant-level entry) the active account.
func (c *CLI) SetAccount(ctx context.Context, subscription string) error {
	_, err := c.Command.Run(ctx, "account", "set", "--subscription", subscription, "--only-show-errors")
	return err
}

// Login signs in to tenantID on the terminal (a browser, or a device code).
func (c *CLI) Login(ctx context.Context, tenantID string, deviceCode, allowNoSubscriptions bool) error {
	args := []string{"login", "--tenant", tenantID, "--output", "none"}
	if deviceCode {
		args = append(args, "--use-device-code")
	}
	if allowNoSubscriptions {
		args = append(args, "--allow-no-subscriptions")
	}
	// The account is chosen afterwards by az account set; skip the CLI's own picker.
	return c.Command.RunInteractive(ctx, []string{"AZURE_CORE_LOGIN_EXPERIENCE_V2=off"}, args...)
}

// FindAccount is the account that satisfies profile, or false when the CLI has none.
//
// A subscription profile needs that exact subscription in its tenant. A tenant profile
// takes any account in the tenant, preferring the one already active (so switching does
// not needlessly move off a subscription), then the tenant-level entry, then any enabled
// subscription.
func FindAccount(accounts []Account, profile microsoft.Profile) (Account, bool) {
	var inTenant []Account
	for _, account := range accounts {
		if account.TenantID == profile.TenantID {
			inTenant = append(inTenant, account)
		}
	}
	if profile.SubscriptionID != "" {
		for _, account := range inTenant {
			if account.ID == profile.SubscriptionID {
				return account, true
			}
		}
		return Account{}, false
	}
	for _, preferred := range []func(Account) bool{
		func(a Account) bool { return a.IsDefault },
		Account.TenantLevel,
		func(a Account) bool { return a.State == "Enabled" },
	} {
		for _, account := range inTenant {
			if preferred(account) {
				return account, true
			}
		}
	}
	if len(inTenant) > 0 {
		return inTenant[0], true
	}
	return Account{}, false
}

// MatchProfile is the profile describing account: a subscription match first, then a
// tenant match.
func MatchProfile(profiles []microsoft.Profile, account Account) (microsoft.Profile, bool) {
	for _, profile := range profiles {
		if profile.SubscriptionID == account.ID && profile.TenantID == account.TenantID {
			return profile, true
		}
	}
	for _, profile := range profiles {
		if profile.SubscriptionID == "" && profile.TenantID == account.TenantID {
			return profile, true
		}
	}
	return microsoft.Profile{}, false
}

// Switched is what Switch did.
type Switched struct {
	Profile  microsoft.Profile
	Account  Account
	SignedIn bool
}

// Switch makes profile the Azure CLI's active context, signing in first when needed.
// With signIn false a missing session is an error rather than a prompt, which suits
// scripts. The switch is read back and checked before it returns.
func (c *CLI) Switch(ctx context.Context, profile microsoft.Profile, signIn, deviceCode bool) (Switched, error) {
	if err := profile.RequireRealIDs(); err != nil {
		return Switched{}, err
	}
	accounts, err := c.Accounts(ctx)
	if err != nil {
		return Switched{}, err
	}
	account, found := FindAccount(accounts, profile)
	signedIn := false
	if !found {
		if !signIn {
			return Switched{}, errs.Commandf("the Azure CLI has no session for profile %q", profile.Name).
				WithHint("sign in with: az login --tenant %s", profile.TenantID)
		}
		if err := c.Login(ctx, profile.TenantID, deviceCode, profile.SubscriptionID == ""); err != nil {
			return Switched{}, err
		}
		signedIn = true
		if accounts, err = c.Accounts(ctx); err != nil {
			return Switched{}, err
		}
		if account, found = FindAccount(accounts, profile); !found {
			return Switched{}, errs.Commandf("signed in to tenant %s, but subscription %s is not visible to that account",
				profile.TenantID, profile.SubscriptionID)
		}
	}
	if err := c.SetAccount(ctx, account.ID); err != nil {
		return Switched{}, err
	}
	current, ok, err := c.Current(ctx)
	if err != nil {
		return Switched{}, err
	}
	if !ok || current.ID != account.ID {
		return Switched{}, errs.Commandf("the Azure CLI did not switch to %s", account.ID)
	}
	return Switched{Profile: profile, Account: current, SignedIn: signedIn}, nil
}
