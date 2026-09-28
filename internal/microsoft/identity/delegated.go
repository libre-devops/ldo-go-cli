package identity

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"

	"github.com/libre-devops/ldo-go-cli/internal/core/auth"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/tokenstore"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

// Delegated signs a person in as themselves, with MSAL: a device code to enter
// (device-code) or a browser window (interactive). Their sign-in is kept in the store the
// profile's token_cache names, so the next command gets its tokens silently, from the
// refresh token, until it lapses.
type Delegated struct {
	client   public.Client
	flow     string
	clientID string
	notify   func(string)
	// mu serialises sign-ins, so one command never asks twice at once.
	mu sync.Mutex
}

func newDelegated(profile microsoft.Profile, opts Options) (*Delegated, error) {
	store := opts.Store
	if store == nil {
		opened, err := tokenstore.Open(profile.TokenCache)
		if err != nil {
			return nil, err
		}
		store = opened
	}
	clientID := profile.ClientID
	if clientID == "" {
		clientID = AzureCLIClientID
	}
	options := []public.Option{
		public.WithAuthority(profile.Cloud.LoginURL + "/organizations"),
		public.WithCache(&storeCache{store: store, key: "msal:" + clientID}),
	}
	if opts.HTTPClient != nil {
		options = append(options, public.WithHTTPClient(opts.HTTPClient))
	}
	client, err := public.New(clientID, options...)
	if err != nil {
		return nil, errs.Authf("cannot set up the sign-in: %v", err)
	}
	notify := opts.Notify
	if notify == nil {
		notify = func(string) {}
	}
	return &Delegated{client: client, flow: profile.Auth, clientID: clientID, notify: notify}, nil
}

// GetToken is a token for resource in tenantID: silently from a kept sign-in when there
// is one, else by signing the person in.
func (d *Delegated) GetToken(ctx context.Context, resource, tenantID string) (auth.AccessToken, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	scopes := []string{Scope(resource)}
	if result, err := d.silent(ctx, scopes, tenantID); err == nil {
		return token(result, resource, tenantID), nil
	}
	result, err := d.signIn(ctx, scopes, tenantID)
	if err != nil {
		return auth.AccessToken{}, d.explain(err, tenantID)
	}
	return token(result, resource, tenantID), nil
}

func (d *Delegated) silent(ctx context.Context, scopes []string, tenantID string) (public.AuthResult, error) {
	accounts, err := d.client.Accounts(ctx)
	if err != nil || len(accounts) == 0 {
		return public.AuthResult{}, fmt.Errorf("no kept sign-in")
	}
	// The account whose home is this tenant, else the first: a guest's refresh token from
	// its home tenant gets tokens for the others it is in.
	account := accounts[0]
	for _, candidate := range accounts {
		if strings.EqualFold(candidate.Realm, tenantID) {
			account = candidate
			break
		}
	}
	return d.client.AcquireTokenSilent(ctx, scopes, public.WithSilentAccount(account), public.WithTenantID(tenantID))
}

func (d *Delegated) signIn(ctx context.Context, scopes []string, tenantID string) (public.AuthResult, error) {
	if d.flow == "interactive" {
		d.notify("signing in to tenant " + tenantID + " in the browser...")
		return d.client.AcquireTokenInteractive(ctx, scopes, public.WithTenantID(tenantID),
			public.WithRedirectURI("http://localhost"))
	}
	code, err := d.client.AcquireTokenByDeviceCode(ctx, scopes, public.WithTenantID(tenantID))
	if err != nil {
		return public.AuthResult{}, err
	}
	d.notify(code.Result.Message)
	return code.AuthenticationResult(ctx)
}

func (d *Delegated) explain(err error, tenantID string) error {
	message := firstLine(err.Error())
	if reason := LapseReason(message); reason != "" {
		return errs.Reauthf(tenantID, reason, "the sign-in to tenant %s was refused: %s", tenantID, reason)
	}
	if strings.Contains(message, "AADSTS65001") {
		return errs.Authf("the app %s has not been consented in tenant %s", d.clientID, tenantID).
			WithHint("an administrator grants it consent, in Enterprise applications")
	}
	return errs.Authf("the %s sign-in failed: %s", d.flow, message)
}

func token(result public.AuthResult, resource, tenantID string) auth.AccessToken {
	return auth.AccessToken{Token: result.AccessToken, ExpiresOn: result.ExpiresOn.UTC(),
		TenantID: strings.ToLower(tenantID), Resource: resource}
}

// SignedIn is the accounts a kept sign-in holds: their user names.
func (d *Delegated) SignedIn(ctx context.Context) ([]string, error) {
	accounts, err := d.client.Accounts(ctx)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, account := range accounts {
		names = append(names, account.PreferredUsername)
	}
	return names, nil
}

// SignOut forgets every kept sign-in for this app.
func (d *Delegated) SignOut(ctx context.Context) (int, error) {
	accounts, err := d.client.Accounts(ctx)
	if err != nil {
		return 0, err
	}
	for _, account := range accounts {
		if err := d.client.RemoveAccount(ctx, account); err != nil {
			return 0, err
		}
	}
	return len(accounts), nil
}

// storeCache is MSAL's cache, kept in a token store under one key: MSAL reads it before
// each call and writes it after, so a sign-in outlasts the command that made it.
type storeCache struct {
	store tokenstore.Store
	key   string
}

func (s *storeCache) Replace(_ context.Context, into cache.Unmarshaler, _ cache.ReplaceHints) error {
	text, found, err := s.store.Load(s.key)
	if err != nil || !found {
		return err
	}
	return into.Unmarshal([]byte(text))
}

func (s *storeCache) Export(_ context.Context, from cache.Marshaler, _ cache.ExportHints) error {
	text, err := from.Marshal()
	if err != nil {
		return err
	}
	return s.store.Save(s.key, string(text))
}
