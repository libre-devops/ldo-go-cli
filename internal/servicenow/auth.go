package servicenow

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/auth"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/httpx"
	"github.com/libre-devops/ldo-go-cli/internal/core/tokenstore"
)

// Signing in to a ServiceNow instance as yourself: OAuth with a kept sign-in, or basic.
//
// OAuth signs in through an OAuth application registry entry on the instance (its client
// id and secret), and keeps the refresh token it gets, so later commands sign in by
// themselves until it expires (100 days by default) or is revoked:
//
//   - browser: the authorisation code flow with PKCE. You open a link in any browser and
//     sign in as you would to the instance (single sign-on and MFA included), then paste
//     back the address the browser lands on. Nothing has to listen there, so this works on
//     a headless machine, and at a workplace that will not allow passwords on the API.
//   - password: the username and password, once, for the tokens.
//
// Basic sends the username and password with every request. ServiceNow now refuses that
// for ordinary interactive accounts unless they hold the snc_basic_auth_api_access role.
//
// Access tokens are never kept. A client secret typed in at sign-in (rather than set in
// the environment) is kept with the refresh token, so it is not asked for again. Nothing
// is logged.

// Ask asks the person a question, hiding what they type when hide is set.
type Ask func(question string, hide bool) (string, error)

// DefaultAccessLifetime is how long ServiceNow's access tokens last unless it says.
const DefaultAccessLifetime = 30 * time.Minute

// Credential is how requests to an instance are signed: Basic or OAuth.
type Credential interface {
	// Scheme is the Authorization header's scheme: Basic or Bearer.
	Scheme() string
	// Source gives the Authorization header's value after the scheme.
	Source() *httpx.TokenSource
}

// Basic is your username and password, sent with each request (HTTP Basic).
type Basic struct {
	Username string
	encoded  string
}

// NewBasic is a basic credential; both parts are needed.
func NewBasic(username, password string) (*Basic, error) {
	if username == "" {
		return nil, errs.Authf("basic sign-in needs a username")
	}
	if password == "" {
		return nil, errs.Authf("basic sign-in needs a password")
	}
	return &Basic{Username: username, encoded: base64.StdEncoding.EncodeToString([]byte(username + ":" + password))}, nil
}

// String names the account only.
func (b *Basic) String() string { return "Basic(username=" + b.Username + ")" }

// GoString is String, so %#v leaves the password out too.
func (b *Basic) GoString() string { return b.String() }

// Authorization is the value that follows Basic in the Authorization header.
func (b *Basic) Authorization() string { return b.encoded }

// Scheme is Basic.
func (b *Basic) Scheme() string { return "Basic" }

// Source gives the encoded username and password each time.
func (b *Basic) Source() *httpx.TokenSource {
	return &httpx.TokenSource{Token: func(context.Context) (string, error) { return b.encoded, nil }}
}

// OAuthApp is the OAuth application registry entry: where to sign in, and as which
// client.
type OAuthApp struct {
	Instance string
	ClientID string
	// ClientSecret is empty to use the kept sign-in's, or to ask.
	ClientSecret string
	RedirectURI  string
}

// OAuthOptions are how an OAuth credential signs in, and where it keeps the sign-in.
type OAuthOptions struct {
	// Key names the kept sign-in in Store.
	Key string
	// SignIn is browser or password.
	SignIn   string
	Username string
	// Password is the password from the environment, or ""; nil for none.
	Password func() string
	// Ask asks the person; nil when there is nobody to ask, so a missing sign-in is an
	// error saying how to sign in.
	Ask   Ask
	Store tokenstore.Store
	// HTTPClient sends the token requests; tests give a fake transport.
	HTTPClient *http.Client
	Now        func() time.Time
	// Notify shows the person what they must see now: the link to open.
	Notify func(string)
	// OpenBrowser opens a link, when HasBrowser says one can be opened here.
	OpenBrowser func(string) error
	HasBrowser  func() bool
	// SignInHint is what to do when there is no kept sign-in and no way to make one.
	SignInHint string
}

// OAuth is OAuth 2.0 against the instance itself, with the sign-in kept in a store. It is
// an auth.TokenProvider (the resource and tenant are not needed: one credential is one
// instance and one account), so auth.Caching works as it does for Entra ID.
type OAuth struct {
	App      OAuthApp
	opts     OAuthOptions
	endpoint *httpx.Client
	mu       sync.Mutex
}

// NewOAuth is an OAuth credential for app.
func NewOAuth(app OAuthApp, opts OAuthOptions) (*OAuth, error) {
	if app.ClientID == "" {
		return nil, errs.Authf("OAuth sign-in needs the application's client id")
	}
	if opts.SignIn == "" {
		opts.SignIn = "browser"
	}
	if opts.SignIn == "password" && opts.Username == "" {
		return nil, errs.Authf(`sign_in = "password" needs a username`)
	}
	endpoint, err := httpx.New(httpx.Options{BaseURL: app.Instance, Name: "ServiceNow sign-in", HTTPClient: opts.HTTPClient})
	if err != nil {
		return nil, err
	}
	fillOAuthDefaults(&opts)
	return &OAuth{App: app, opts: opts, endpoint: endpoint}, nil
}

func fillOAuthDefaults(opts *OAuthOptions) {
	if opts.Password == nil {
		opts.Password = func() string { return "" }
	}
	if opts.Store == nil {
		opts.Store = tokenstore.NewMemory()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Notify == nil {
		opts.Notify = func(message string) { slog.Warn(message) }
	}
	if opts.HasBrowser == nil {
		opts.HasBrowser = func() bool { return false }
	}
	if opts.SignInHint == "" {
		opts.SignInHint = "sign in again"
	}
}

// String names the instance and client only.
func (o *OAuth) String() string {
	return "OAuth(instance=" + o.App.Instance + ", client_id=" + o.App.ClientID + ", sign_in=" + o.opts.SignIn + ")"
}

// GoString is String, so %#v leaves the secrets out too.
func (o *OAuth) GoString() string { return o.String() }

// Scheme is Bearer.
func (o *OAuth) Scheme() string { return "Bearer" }

// Source gives a token from a cache over this credential, for one command.
func (o *OAuth) Source() *httpx.TokenSource {
	bearer := auth.Bearer{Provider: auth.NewCaching(o, o.opts.Now), Resource: o.App.Instance}
	return &httpx.TokenSource{Token: bearer.Token, Refresh: bearer.Refresh}
}

// SignInMethod is browser or password.
func (o *OAuth) SignInMethod() string { return o.opts.SignIn }

// GetToken is a token: refreshed from the kept sign-in when there is one, else a new
// sign-in. The arguments are there to fit TokenProvider; ServiceNow has no resources or
// tenants.
func (o *OAuth) GetToken(ctx context.Context, _, _ string) (auth.AccessToken, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	kept := o.recall()
	if refresh := kept["refresh_token"]; refresh != "" {
		token, err := o.grant(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}, kept)
		if err == nil {
			return token, nil
		}
		if !errs.Is(err, errs.Auth) {
			return auth.AccessToken{}, err
		}
		o.forget()
		o.opts.Notify("The kept ServiceNow sign-in was refused; signing in again.")
	}
	return o.signIn(ctx, kept)
}

// SignIn signs in afresh, ignoring any kept sign-in, and keeps the new one.
func (o *OAuth) SignIn(ctx context.Context) (auth.AccessToken, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	kept := o.recall()
	delete(kept, "refresh_token")
	return o.signIn(ctx, kept)
}

// SignOut forgets the kept sign-in: true when there was one.
func (o *OAuth) SignOut() (bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.opts.Store.Delete(o.opts.Key)
}

// HasKeptSignIn reports whether a sign-in is kept that could be refreshed without asking
// anyone.
func (o *OAuth) HasKeptSignIn() bool {
	value, found, err := o.opts.Store.Load(o.opts.Key)
	return err == nil && found && decodeKept(value)["refresh_token"] != ""
}

func (o *OAuth) signIn(ctx context.Context, kept map[string]string) (auth.AccessToken, error) {
	if o.opts.SignIn == "password" {
		password := o.opts.Password()
		if password == "" && o.opts.Ask != nil {
			var err error
			if password, err = o.opts.Ask("ServiceNow password for "+o.opts.Username, true); err != nil {
				return auth.AccessToken{}, err
			}
		}
		if password == "" {
			return auth.AccessToken{}, o.needsSignIn("no password to sign in with")
		}
		form := url.Values{"grant_type": {"password"}, "username": {o.opts.Username}, "password": {password}}
		return o.grant(ctx, form, kept)
	}
	if o.opts.Ask == nil {
		return auth.AccessToken{}, o.needsSignIn("signing in needs a browser and someone to paste back")
	}
	return o.browserSignIn(ctx, kept)
}

// browserSignIn is the authorisation code flow with PKCE: a link to open, and the address
// it lands on pasted back.
func (o *OAuth) browserSignIn(ctx context.Context, kept map[string]string) (auth.AccessToken, error) {
	proof := auth.NewPKCE()
	params := proof.Parameters()
	params.Set("response_type", "code")
	params.Set("client_id", o.App.ClientID)
	params.Set("redirect_uri", o.App.RedirectURI)
	link := o.App.Instance + "/oauth_auth.do?" + strings.ReplaceAll(params.Encode(), "+", "%20")
	o.opts.Notify("Open this link in a browser and sign in to ServiceNow. You will land on an address starting " +
		o.App.RedirectURI + " (the page may not load: that is fine). Copy that whole address and paste it here.\n\n" +
		link + "\n")
	if o.opts.HasBrowser() && o.opts.OpenBrowser != nil {
		// A browser that will not open is no matter: the link is shown.
		_ = o.opts.OpenBrowser(link)
	}
	pasted, err := o.opts.Ask("The address you landed on", false)
	if err != nil {
		return auth.AccessToken{}, err
	}
	code, err := codeFrom(strings.TrimSpace(pasted), proof.State)
	if err != nil {
		return auth.AccessToken{}, err
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {o.App.RedirectURI},
		"code_verifier": {proof.Verifier}}
	return o.grant(ctx, form, kept)
}

// codeFrom is the authorisation code in the address the browser landed on, checked
// against this sign-in's state.
func codeFrom(pasted, state string) (string, error) {
	var query url.Values
	if parsed, err := url.Parse(pasted); err == nil {
		query = parsed.Query()
	}
	if query.Has("error") {
		detail := query.Get("error_description")
		if detail == "" {
			detail = query.Get("error")
		}
		return "", errs.Authf("ServiceNow sign-in failed: %s", detail)
	}
	if query.Get("state") != state {
		return "", errs.Authf("that address is not from this sign-in (its state does not match)").
			WithHint("paste the whole address the browser landed on, from this sign-in")
	}
	code := query.Get("code")
	if code == "" {
		return "", errs.Authf("that address carries no authorisation code")
	}
	return code, nil
}

func (o *OAuth) needsSignIn(why string) error {
	return errs.Reauthf("", "no kept sign-in", "there is no kept sign-in to %s, and %s", o.App.Instance, why).
		WithHint("%s", o.opts.SignInHint)
}

// grant asks the token endpoint for a token with form, and keeps the refresh token it
// gives.
func (o *OAuth) grant(ctx context.Context, form url.Values, kept map[string]string) (auth.AccessToken, error) {
	secret, typed, err := o.clientSecret(kept)
	if err != nil {
		return auth.AccessToken{}, err
	}
	now := o.opts.Now()
	body := url.Values{"client_id": {o.App.ClientID}, "client_secret": {secret}}
	for key, values := range form {
		body[key] = values
	}
	data, err := o.endpoint.Do(ctx, http.MethodPost, "/oauth_token.do", httpx.Call{Form: body})
	if err != nil {
		if found := errs.As(err); found != nil && found.Kind == errs.API {
			return auth.AccessToken{}, errs.Authf("%s", found.Message).WithHint("%s", grantHint(found.Message))
		}
		return auth.AccessToken{}, err
	}
	token := fields.Text(data, "access_token")
	if token == "" {
		return auth.AccessToken{}, errs.Authf("the ServiceNow token response has no access_token")
	}
	if refresh := fields.Text(data, "refresh_token"); refresh != "" {
		record := map[string]string{"refresh_token": refresh}
		if _, keptSecret := kept["client_secret"]; typed || (keptSecret && o.App.ClientSecret == "") {
			record["client_secret"] = secret
		}
		o.remember(record)
	}
	lifetime := DefaultAccessLifetime
	if seconds, ok := fields.Number(data["expires_in"]); ok && seconds > 0 {
		lifetime = time.Duration(seconds * float64(time.Second))
	}
	return auth.AccessToken{Token: token, ExpiresOn: now.Add(lifetime), TenantID: o.opts.Username, Resource: o.App.Instance}, nil
}

// clientSecret is the client secret, and whether it was typed in just now (so worth
// keeping).
func (o *OAuth) clientSecret(kept map[string]string) (string, bool, error) {
	if o.App.ClientSecret != "" {
		return o.App.ClientSecret, false, nil
	}
	if kept["client_secret"] != "" {
		return kept["client_secret"], false, nil
	}
	if o.opts.Ask != nil {
		typed, err := o.opts.Ask("Client secret of the OAuth application", true)
		if err != nil {
			return "", false, err
		}
		if typed = strings.TrimSpace(typed); typed != "" {
			return typed, true, nil
		}
	}
	return "", false, o.needsSignIn("no client secret")
}

func (o *OAuth) recall() map[string]string {
	value, found, err := o.opts.Store.Load(o.opts.Key)
	if err != nil {
		o.opts.Notify("The kept ServiceNow sign-in cannot be used (" + err.Error() + "); signing in afresh.")
		return map[string]string{}
	}
	if !found {
		return map[string]string{}
	}
	return decodeKept(value)
}

func (o *OAuth) remember(record map[string]string) {
	// encoding/json writes a map's keys sorted, as the Python's sort_keys does.
	encoded, _ := json.Marshal(record)
	if err := o.opts.Store.Save(o.opts.Key, string(encoded)); err != nil {
		o.opts.Notify("The ServiceNow sign-in could not be kept for the next command: " + err.Error())
	}
}

func (o *OAuth) forget() {
	if _, err := o.opts.Store.Delete(o.opts.Key); err != nil {
		slog.Warn("could not forget a refused ServiceNow sign-in", "error", err.Error())
	}
}

// decodeKept is a kept sign-in's text values, or none when it is damaged.
func decodeKept(value string) map[string]string {
	var data map[string]any
	found := map[string]string{}
	if json.Unmarshal([]byte(value), &data) != nil {
		return found
	}
	for key, item := range data {
		if text, ok := item.(string); ok && text != "" {
			found[key] = text
		}
	}
	return found
}

func grantHint(message string) string {
	text := strings.ToLower(message)
	switch {
	case strings.Contains(text, "invalid_client"):
		return "check the client id and secret against the application registry entry"
	case strings.Contains(text, "invalid_grant"), strings.Contains(text, "access_denied"):
		return "the instance refused the sign-in: check the username and password (or sign in again), and that " +
			"the application registry entry is active; repeated failures lock the account for a while"
	}
	return "check the application registry entry is active, and its client id and secret"
}

// CredentialOptions are what CredentialFor needs beyond the profile.
type CredentialOptions struct {
	Getenv      func(string) string
	Store       tokenstore.Store
	Ask         Ask
	HTTPClient  *http.Client
	Now         func() time.Time
	Notify      func(string)
	SignInHint  string
	HasBrowser  func() bool
	OpenBrowser func(string) error
}

// CredentialFor is the credential profile describes, with its secrets from the
// environment (or asked for).
func CredentialFor(profile Profile, opts CredentialOptions) (Credential, error) {
	getenv := opts.Getenv
	username := profile.Username
	if username == "" {
		username = strings.TrimSpace(getenv(UsernameEnv))
	}
	if profile.Auth == "basic" {
		return basicFor(profile, username, getenv)
	}
	clientID := profile.ClientID
	if clientID == "" {
		clientID = strings.TrimSpace(getenv(ClientIDEnv))
	}
	if clientID == "" {
		return nil, errs.Authf("ServiceNow profile %q has no OAuth client id", profile.Name).
			WithHint("set client_id on the profile, or %s: the client id of an entry in System OAuth > Application Registry", ClientIDEnv)
	}
	if profile.SignIn == "password" && username == "" {
		return nil, errs.Authf("ServiceNow profile %q signs in with a password but has no username", profile.Name).
			WithHint("set username on the profile, or %s", UsernameEnv)
	}
	store := opts.Store
	if store == nil {
		var err error
		if store, err = tokenstore.Open(profile.TokenCache); err != nil {
			return nil, err
		}
	}
	app := OAuthApp{Instance: profile.Instance, ClientID: clientID, ClientSecret: getenv(profile.ClientSecretEnv),
		RedirectURI: profile.RedirectURI}
	return NewOAuth(app, OAuthOptions{
		Key: "servicenow|" + profile.Host() + "|" + clientID + "|" + profile.Name, SignIn: profile.SignIn, Username: username,
		Password: func() string { return getenv(profile.PasswordEnv) }, Ask: opts.Ask, Store: store,
		HTTPClient: opts.HTTPClient, Now: opts.Now, Notify: opts.Notify, SignInHint: opts.SignInHint,
		HasBrowser: opts.HasBrowser, OpenBrowser: opts.OpenBrowser,
	})
}

func basicFor(profile Profile, username string, getenv func(string) string) (Credential, error) {
	if username == "" {
		return nil, errs.Authf("ServiceNow profile %q has no username", profile.Name).
			WithHint("set username on the profile, or %s", UsernameEnv)
	}
	password := getenv(profile.PasswordEnv)
	if password == "" {
		return nil, errs.Authf("ServiceNow profile %q needs a password", profile.Name).
			WithHint("set %s; the config file never holds passwords", profile.PasswordEnv)
	}
	return NewBasic(username, password)
}
