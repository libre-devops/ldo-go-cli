package servicenow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/tokenstore"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/snowfake"
)

const key = "servicenow|dev12345.service-now.com|client|dev"

var ctx = context.Background()

// credential is an OAuth credential against instance, and what it showed the person.
func credential(t *testing.T, instance *snowfake.Fake, opts OAuthOptions, secret string) (*OAuth, *[]string, *httpfake.Transport) {
	t.Helper()
	client, transport := httpfake.Client(instance.Handler)
	notices := &[]string{}
	opts.Key, opts.HTTPClient = key, client
	opts.Notify = func(text string) { *notices = append(*notices, text) }
	if opts.SignIn == "" {
		opts.SignIn = "password"
	}
	if opts.Username == "" {
		opts.Username = snowfake.Username
	}
	oauth, err := NewOAuth(OAuthApp{Instance: snowfake.Instance, ClientID: snowfake.ClientID, ClientSecret: secret, RedirectURI: snowfake.Redirect}, opts)
	if err != nil {
		t.Fatal(err)
	}
	return oauth, notices, transport
}

func password(text string) func() string { return func() string { return text } }

func TestBasicEncodesTheUsernameAndPasswordAndHidesThem(t *testing.T) {
	basic, err := NewBasic("ana", "p4ss")
	if err != nil || basic.Authorization() != "YW5hOnA0c3M=" || basic.Scheme() != "Basic" {
		t.Fatal(basic, err)
	}
	if token, _ := basic.Source().Token(ctx); token != "YW5hOnA0c3M=" {
		t.Error(token)
	}
	if shown := fmt.Sprintf("%v %#v", basic, basic); strings.Contains(shown, "p4ss") || strings.Contains(shown, "YW5h") {
		t.Error(shown)
	}
	if _, err := NewBasic("ana", ""); err == nil || !strings.Contains(err.Error(), "needs a password") {
		t.Error(err)
	}
	if _, err := NewBasic("", "p4ss"); err == nil || !strings.Contains(err.Error(), "needs a username") {
		t.Error(err)
	}
}

func TestThePasswordGrantSignsInOnceThenTheKeptRefreshTokenServes(t *testing.T) {
	instance := snowfake.New()
	store := tokenstore.NewMemory()
	first, _, _ := credential(t, instance, OAuthOptions{Store: store, Password: password(snowfake.Password)}, snowfake.ClientSecret)
	token, err := first.GetToken(ctx, "", "")
	if err != nil || !instance.Issued(token.Token) || token.Resource != snowfake.Instance || token.TenantID != snowfake.Username {
		t.Fatal(token, err)
	}
	// A new credential, as the next command makes: no password needed now.
	later, _, _ := credential(t, instance, OAuthOptions{Store: store}, snowfake.ClientSecret)
	if token, err := later.GetToken(ctx, "", ""); err != nil || !instance.Issued(token.Token) {
		t.Fatal(err)
	}
	if got := strings.Join(instance.Grants(), ","); got != "password,refresh_token" {
		t.Error(got)
	}
	kept, _, _ := store.Load(key)
	var record map[string]any
	_ = json.Unmarshal([]byte(kept), &record)
	// The secret came from the environment, so only the refresh token is kept.
	if len(record) != 1 || record["refresh_token"] == nil {
		t.Error(kept)
	}
}

func TestWithoutAKeptSignInOrAPasswordItSaysHowToSignIn(t *testing.T) {
	oauth, _, _ := credential(t, snowfake.New(), OAuthOptions{SignInHint: "run ldo-go snow sign-in"}, snowfake.ClientSecret)
	_, err := oauth.GetToken(ctx, "", "")
	if !errs.Is(err, errs.Reauth) || !strings.Contains(err.Error(), "no password") || errs.As(err).Hint != "run ldo-go snow sign-in" {
		t.Error(err)
	}
}

func TestAPasswordCanBeAskedForHidden(t *testing.T) {
	instance := snowfake.New()
	var asked []string
	ask := func(question string, hide bool) (string, error) {
		asked = append(asked, fmt.Sprintf("%s %v", question, hide))
		return snowfake.Password, nil
	}
	oauth, _, _ := credential(t, instance, OAuthOptions{Ask: ask}, snowfake.ClientSecret)
	if _, err := oauth.GetToken(ctx, "", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Join(asked, "|") != "ServiceNow password for ana true" {
		t.Error(asked)
	}
	failing := func(string, bool) (string, error) { return "", errors.New("interrupted") }
	oauth, _, _ = credential(t, instance, OAuthOptions{Ask: failing}, snowfake.ClientSecret)
	if _, err := oauth.GetToken(ctx, "", ""); err == nil || err.Error() != "interrupted" {
		t.Error(err)
	}
}

func TestAClientSecretTypedInIsKeptWithTheSignIn(t *testing.T) {
	instance := snowfake.New()
	store := &tokenstore.File{Path: t.TempDir() + "/sign-ins.json"}
	answer := func(question string, _ bool) (string, error) {
		if strings.Contains(question, "Client secret") {
			return " " + snowfake.ClientSecret + " ", nil
		}
		return snowfake.Password, nil
	}
	oauth, _, _ := credential(t, instance, OAuthOptions{Store: store, Ask: answer}, "")
	if _, err := oauth.GetToken(ctx, "", ""); err != nil {
		t.Fatal(err)
	}
	kept, _, _ := store.Load(key)
	if !strings.Contains(kept, `"client_secret":"`+snowfake.ClientSecret+`"`) {
		t.Error(kept)
	}
	// The next command needs neither the secret nor the password.
	later, _, _ := credential(t, instance, OAuthOptions{Store: store}, "")
	if token, err := later.GetToken(ctx, "", ""); err != nil || !instance.Issued(token.Token) {
		t.Fatal(err)
	}
	// And the secret stays kept when the refresh token turns over.
	kept, _, _ = store.Load(key)
	if !strings.Contains(kept, "client_secret") {
		t.Error(kept)
	}
	nobody, _, _ := credential(t, instance, OAuthOptions{Password: password(snowfake.Password)}, "")
	if _, err := nobody.GetToken(ctx, "", ""); !errs.Is(err, errs.Reauth) || !strings.Contains(err.Error(), "no client secret") {
		t.Error(err)
	}
}

func TestARefusedRefreshTokenIsForgottenAndItSignsInAgain(t *testing.T) {
	instance := snowfake.New()
	store := tokenstore.NewMemory()
	_ = store.Save(key, `{"refresh_token": "revoked"}`)
	oauth, notices, _ := credential(t, instance, OAuthOptions{Store: store, Password: password(snowfake.Password)}, snowfake.ClientSecret)
	if token, err := oauth.GetToken(ctx, "", ""); err != nil || !instance.Issued(token.Token) {
		t.Fatal(err)
	}
	if got := strings.Join(instance.Grants(), ","); got != "refresh_token,password" {
		t.Error(got)
	}
	if !strings.Contains(strings.Join(*notices, "\n"), "refused") {
		t.Error(*notices)
	}
}

var authorizeLink = regexp.MustCompile(`https://\S+/oauth_auth\.do\?\S+`)

func TestTheBrowserSignInUsesPKCEAndThePastedAddress(t *testing.T) {
	instance := snowfake.New()
	var notices *[]string
	var opened []string
	paste := func(string, bool) (string, error) {
		return instance.Approve(authorizeLink.FindString((*notices)[len(*notices)-1]), false)
	}
	oauth, shown, transport := credential(t, instance, OAuthOptions{SignIn: "browser", Ask: paste, HasBrowser: func() bool { return true },
		OpenBrowser: func(link string) error { opened = append(opened, link); return errors.New("no display") }}, snowfake.ClientSecret)
	notices = shown
	token, err := oauth.GetToken(ctx, "", "")
	if err != nil || !instance.Issued(token.Token) {
		t.Fatal(err)
	}
	seen := transport.Seen()
	exchange, _ := url.ParseQuery(seen[len(seen)-1].Body)
	if exchange.Get("grant_type") != "authorization_code" || exchange.Get("redirect_uri") != snowfake.Redirect || exchange.Get("code_verifier") == "" {
		t.Error(exchange)
	}
	if !strings.Contains((*notices)[0], "paste it here") || len(opened) != 1 || !strings.HasPrefix(opened[0], snowfake.Instance+"/oauth_auth.do?") {
		t.Error(*notices, opened)
	}
}

func TestABrowserSignInWithTheWrongAddressOrARefusalFails(t *testing.T) {
	instance := snowfake.New()
	forged := func(string, bool) (string, error) { return snowfake.Redirect + "?code=abc&state=forged", nil }
	oauth, _, _ := credential(t, instance, OAuthOptions{SignIn: "browser", Ask: forged}, snowfake.ClientSecret)
	if _, err := oauth.GetToken(ctx, "", ""); err == nil || !strings.Contains(err.Error(), "state does not match") {
		t.Error(err)
	}
	var notices *[]string
	deny := func(string, bool) (string, error) {
		return instance.Approve(authorizeLink.FindString((*notices)[len(*notices)-1]), true)
	}
	refused, shown, _ := credential(t, instance, OAuthOptions{SignIn: "browser", Ask: deny}, snowfake.ClientSecret)
	notices = shown
	if _, err := refused.GetToken(ctx, "", ""); err == nil || err.Error() != "ServiceNow sign-in failed: access_denied" {
		t.Error(err)
	}
	var codeless *[]string
	noCode := func(string, bool) (string, error) {
		state, _ := url.Parse(authorizeLink.FindString((*codeless)[len(*codeless)-1]))
		return snowfake.Redirect + "?state=" + state.Query().Get("state"), nil
	}
	empty, shown, _ := credential(t, instance, OAuthOptions{SignIn: "browser", Ask: noCode}, snowfake.ClientSecret)
	codeless = shown
	if _, err := empty.GetToken(ctx, "", ""); err == nil || !strings.Contains(err.Error(), "no authorisation code") {
		t.Error(err)
	}
}

func TestABrowserSignInWithNobodyToPasteSaysHowToSignIn(t *testing.T) {
	oauth, _, _ := credential(t, snowfake.New(), OAuthOptions{SignIn: "browser"}, snowfake.ClientSecret)
	if _, err := oauth.GetToken(ctx, "", ""); !errs.Is(err, errs.Reauth) || !strings.Contains(err.Error(), "needs a browser") {
		t.Error(err)
	}
}

func TestAWrongClientSecretOrPasswordIsExplained(t *testing.T) {
	oauth, _, _ := credential(t, snowfake.New(), OAuthOptions{Password: password(snowfake.Password)}, "wrong")
	if _, err := oauth.GetToken(ctx, "", ""); !errs.Is(err, errs.Auth) || !strings.Contains(errs.As(err).Hint, "client id and secret") {
		t.Error(err)
	}
	oauth, _, _ = credential(t, snowfake.New(), OAuthOptions{Password: password("wrong")}, snowfake.ClientSecret)
	if _, err := oauth.GetToken(ctx, "", ""); err == nil || !strings.Contains(errs.As(err).Hint, "username and password") {
		t.Error(err)
	}
	if hint := grantHint("HTTP 500: busy"); !strings.Contains(hint, "is active") {
		t.Error(hint)
	}
}

func TestSignInIgnoresTheKeptOneAndSignOutForgetsIt(t *testing.T) {
	instance := snowfake.New()
	store := tokenstore.NewMemory()
	oauth, _, _ := credential(t, instance, OAuthOptions{Store: store, Password: password(snowfake.Password)}, snowfake.ClientSecret)
	if _, err := oauth.GetToken(ctx, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := oauth.SignIn(ctx); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(instance.Grants(), ","); got != "password,password" || !oauth.HasKeptSignIn() {
		t.Error(got)
	}
	if forgot, _ := oauth.SignOut(); !forgot || oauth.HasKeptSignIn() {
		t.Error("the sign-in was not forgotten")
	}
	if shown := fmt.Sprintf("%v %#v", oauth, oauth); strings.Contains(shown, snowfake.ClientSecret) || oauth.Scheme() != "Bearer" || oauth.SignInMethod() != "password" {
		t.Error(shown)
	}
}

func TestTheSourceCachesTheTokenForTheCommand(t *testing.T) {
	instance := snowfake.New()
	oauth, _, _ := credential(t, instance, OAuthOptions{Password: password(snowfake.Password)}, snowfake.ClientSecret)
	source := oauth.Source()
	first, _ := source.Token(ctx)
	second, _ := source.Token(ctx)
	if first != second || len(instance.Grants()) != 1 {
		t.Error(instance.Grants())
	}
	source.Refresh()
	if third, _ := source.Token(ctx); third == first {
		t.Error("the refresh kept the old token")
	}
}

func profile(overrides func(*Profile)) Profile {
	found := Profile{Name: "dev", Instance: snowfake.Instance, Auth: "oauth", SignIn: "browser", RedirectURI: DefaultRedirectURI,
		TokenCache: "memory", PasswordEnv: PasswordEnv, ClientSecretEnv: ClientSecretEnv}
	if overrides != nil {
		overrides(&found)
	}
	return found
}

func environment(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func TestTheFactoryReadsSecretsFromTheEnvironment(t *testing.T) {
	env := environment(map[string]string{"SNOW_INSTANCE_PASSWORD": snowfake.Password, "SNOW_CLIENT_SECRET": snowfake.ClientSecret})
	basic, err := CredentialFor(profile(func(p *Profile) { p.Auth, p.Username = "basic", "ana" }), CredentialOptions{Getenv: env})
	if _, ok := basic.(*Basic); !ok || err != nil {
		t.Fatal(basic, err)
	}
	made, err := CredentialFor(profile(func(p *Profile) { p.ClientID = snowfake.ClientID }), CredentialOptions{Getenv: env})
	oauth, ok := made.(*OAuth)
	if !ok || err != nil || oauth.App.ClientSecret != snowfake.ClientSecret || oauth.opts.Key != "servicenow|dev12345.service-now.com|"+snowfake.ClientID+"|dev" {
		t.Fatal(made, err)
	}
	fromEnv, _ := CredentialFor(profile(nil), CredentialOptions{Getenv: environment(map[string]string{"SNOW_CLIENT_ID": "env-client"}), Store: tokenstore.NewMemory()})
	if fromEnv.(*OAuth).App.ClientID != "env-client" {
		t.Error(fromEnv)
	}
	if _, err := CredentialFor(profile(func(p *Profile) { p.ClientID, p.TokenCache = "c", "disk" }), CredentialOptions{Getenv: env}); err == nil {
		t.Error("an unknown token cache was taken")
	}
}

func TestTheFactorySaysWhatIsMissing(t *testing.T) {
	for _, test := range []struct {
		change  func(*Profile)
		env     map[string]string
		message string
	}{
		{func(p *Profile) { p.Auth = "basic" }, map[string]string{"SNOW_INSTANCE_PASSWORD": "x"}, "has no username"},
		{func(p *Profile) { p.Auth, p.Username = "basic", "ana" }, nil, "needs a password"},
		{nil, nil, "has no OAuth client id"},
		{func(p *Profile) { p.ClientID, p.SignIn = "c", "password" }, nil, "has no username"},
	} {
		_, err := CredentialFor(profile(test.change), CredentialOptions{Getenv: environment(test.env), Store: tokenstore.NewMemory()})
		if !errs.Is(err, errs.Auth) || !strings.Contains(err.Error(), test.message) {
			t.Errorf("%s: %v", test.message, err)
		}
	}
}

// brokenStore fails every call, as a locked keychain does.
type brokenStore struct{}

func (brokenStore) Load(string) (string, bool, error) {
	return "", false, errs.Authf("the keychain is locked")
}
func (brokenStore) Save(string, string) error   { return errs.Authf("the keychain is locked") }
func (brokenStore) Delete(string) (bool, error) { return false, errs.Authf("the keychain is locked") }

func TestAStoreThatFailsIsReportedAndTheSignInStillWorks(t *testing.T) {
	oauth, notices, _ := credential(t, snowfake.New(), OAuthOptions{Store: brokenStore{}, Password: password(snowfake.Password)}, snowfake.ClientSecret)
	if token, err := oauth.GetToken(ctx, "", ""); err != nil || token.Token == "" {
		t.Fatal(err)
	}
	shown := strings.Join(*notices, "\n")
	if !strings.Contains(shown, "cannot be used") || !strings.Contains(shown, "could not be kept") || oauth.HasKeptSignIn() {
		t.Error(shown)
	}
}

func TestADamagedKeptSignInIsTreatedAsNone(t *testing.T) {
	for _, kept := range []string{"not json", `["a list"]`, `{"refresh_token": 5}`} {
		store := tokenstore.NewMemory()
		_ = store.Save(key, kept)
		oauth, _, _ := credential(t, snowfake.New(), OAuthOptions{Store: store, Password: password(snowfake.Password)}, snowfake.ClientSecret)
		if oauth.HasKeptSignIn() {
			t.Errorf("%s: kept", kept)
		}
		if _, err := oauth.GetToken(ctx, "", ""); err != nil {
			t.Errorf("%s: %v", kept, err)
		}
	}
}

func TestATokenResponseWithoutATokenIsAnError(t *testing.T) {
	instance := snowfake.New()
	instance.NoAccessToken = true
	oauth, _, _ := credential(t, instance, OAuthOptions{Password: password(snowfake.Password)}, snowfake.ClientSecret)
	if _, err := oauth.GetToken(ctx, "", ""); err == nil || !strings.Contains(err.Error(), "no access_token") {
		t.Error(err)
	}
}

func TestTheCredentialNeedsAClientIDAndAUsernameForThePasswordGrant(t *testing.T) {
	app := OAuthApp{Instance: snowfake.Instance, ClientSecret: snowfake.ClientSecret, RedirectURI: snowfake.Redirect}
	if _, err := NewOAuth(app, OAuthOptions{Key: key}); err == nil || !strings.Contains(err.Error(), "client id") {
		t.Error(err)
	}
	app.ClientID = snowfake.ClientID
	if _, err := NewOAuth(app, OAuthOptions{Key: key, SignIn: "password"}); err == nil || !strings.Contains(err.Error(), "needs a username") {
		t.Error(err)
	}
	app.Instance = "http://dev12345.service-now.com"
	if _, err := NewOAuth(app, OAuthOptions{Key: key}); err == nil {
		t.Error("plain http was taken")
	}
}
