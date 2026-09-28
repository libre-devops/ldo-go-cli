package identity

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/tokenstore"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

const (
	tenant = "11111111-1111-1111-1111-111111111111"
	client = "55555555-5555-5555-5555-555555555555"
)

func profile(auth string) microsoft.Profile {
	return microsoft.Profile{Name: "p", TenantID: tenant, Cloud: microsoft.Public, Auth: auth, ClientID: client, TokenCache: "memory"}
}

func env(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

// login is a fake Entra ID: instance discovery, the tenant's metadata, a device code and
// the token endpoint, which answers with a token for whatever scope was asked.
func login(t *testing.T, tokenRequests *[]string) httpfake.Handler {
	t.Helper()
	base := "https://login.microsoftonline.com/"
	return httpfake.Routes(
		httpfake.Route{Match: "GET /common/discovery/instance", Reply: httpfake.JSON(map[string]any{
			"tenant_discovery_endpoint": base + tenant + "/v2.0/.well-known/openid-configuration",
			"metadata": []any{map[string]any{"preferred_network": "login.microsoftonline.com",
				"preferred_cache": "login.windows.net", "aliases": []any{"login.microsoftonline.com"}}},
		})},
		httpfake.Route{Match: "GET /", Func: func(r *http.Request) httpfake.Reply {
			segment := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")[0]
			return httpfake.JSON(map[string]any{
				"token_endpoint":                base + segment + "/oauth2/v2.0/token",
				"authorization_endpoint":        base + segment + "/oauth2/v2.0/authorize",
				"device_authorization_endpoint": base + segment + "/oauth2/v2.0/devicecode",
				"issuer":                        base + segment + "/v2.0",
			})
		}},
		httpfake.Route{Match: "POST /", Func: func(r *http.Request) httpfake.Reply {
			_ = r.ParseForm()
			if strings.HasSuffix(r.URL.Path, "/devicecode") {
				return httpfake.JSON(map[string]any{"device_code": "dc", "user_code": "ABC123",
					"verification_uri": "https://microsoft.com/devicelogin", "expires_in": 900, "interval": 1,
					"message": "To sign in, enter the code ABC123"})
			}
			*tokenRequests = append(*tokenRequests, r.URL.Path+" "+r.PostForm.Get("grant_type")+" "+r.PostForm.Get("scope"))
			info := base64.RawURLEncoding.EncodeToString([]byte(`{"uid":"u1","utid":"` + tenant + `"}`))
			return httpfake.JSON(map[string]any{"access_token": "token-for-" + r.PostForm.Get("scope"), "expires_in": 3600,
				"token_type": "Bearer", "refresh_token": "refresh", "client_info": info, "scope": r.PostForm.Get("scope"),
				"id_token": idToken()})
		}},
	)
}

func idToken() string {
	claims, _ := json.Marshal(map[string]any{"tid": tenant, "oid": "u1", "preferred_username": "ana@corp.example",
		"iss": "https://login.microsoftonline.com/" + tenant + "/v2.0", "aud": AzureCLIClientID,
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nbf": time.Now().Unix(), "sub": "s"})
	encode := base64.RawURLEncoding.EncodeToString
	return encode([]byte(`{"alg":"none"}`)) + "." + encode(claims) + ".x"
}

func TestAClientSecretGetsATokenPurelyInGo(t *testing.T) {
	var requests []string
	httpClient, _ := httpfake.Client(login(t, &requests))
	provider, err := For(profile("client-secret"), Options{HTTPClient: httpClient, Getenv: env(map[string]string{SecretVariable: "s"})})
	if err != nil {
		t.Fatal(err)
	}
	token, err := provider.GetToken(context.Background(), "https://graph.microsoft.com", tenant)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token.Token, "token-for-https://graph.microsoft.com/.default") || token.TenantID != tenant {
		t.Errorf("%q %+v", token.Token, token)
	}
	if len(requests) != 1 || !strings.Contains(requests[0], "client_credentials") {
		t.Errorf("%v", requests)
	}
}

func TestAClientSecretMustBeInTheEnvironment(t *testing.T) {
	_, err := For(profile("client-secret"), Options{Getenv: env(nil)})
	if !errs.Is(err, errs.Auth) || !strings.Contains(errs.HintOf(err), SecretVariable) {
		t.Errorf("%v", err)
	}
}

func TestADeviceCodeSignInIsKeptForTheNextCommand(t *testing.T) {
	var requests []string
	httpClient, _ := httpfake.Client(login(t, &requests))
	store := tokenstore.NewMemory()
	var shown []string
	delegated := profile("device-code")
	delegated.ClientID = ""
	provider, err := For(delegated, Options{HTTPClient: httpClient, Store: store, Notify: func(text string) { shown = append(shown, text) }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.GetToken(context.Background(), "https://graph.microsoft.com", tenant); err != nil {
		t.Fatal(err)
	}
	if len(shown) != 1 || !strings.Contains(shown[0], "ABC123") {
		t.Errorf("shown %v", shown)
	}
	if _, kept, _ := store.Load("msal:" + AzureCLIClientID); !kept {
		t.Fatal("the sign-in was not kept")
	}
	// A second command, with a new provider over the same store, signs in silently: the
	// refresh token gets a token for another resource without a device code.
	again, _ := For(delegated, Options{HTTPClient: httpClient, Store: store, Notify: func(text string) { shown = append(shown, text) }})
	token, err := again.GetToken(context.Background(), "https://management.azure.com/", tenant)
	if err != nil {
		t.Fatal(err)
	}
	if len(shown) != 1 || !strings.Contains(token.Token, "management.azure.com") {
		t.Errorf("shown %v, token %v", shown, token)
	}
	if !strings.Contains(requests[len(requests)-1], "refresh_token") {
		t.Errorf("%v", requests)
	}
	signedOut := again.(*Delegated)
	if names, _ := signedOut.SignedIn(context.Background()); len(names) != 1 || names[0] != "ana@corp.example" {
		t.Errorf("signed in %v", names)
	}
	if forgot, err := signedOut.SignOut(context.Background()); err != nil || forgot != 1 {
		t.Errorf("%d %v", forgot, err)
	}
}

func TestAWorkloadIdentityReadsItsTokenFile(t *testing.T) {
	var requests []string
	httpClient, _ := httpfake.Client(login(t, &requests))
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("federated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	provider, err := For(profile("workload-identity"), Options{HTTPClient: httpClient, Getenv: env(map[string]string{TokenFileVariable: path})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.GetToken(context.Background(), "https://graph.microsoft.com", tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := For(profile("workload-identity"), Options{Getenv: env(nil)}); !errs.Is(err, errs.Auth) {
		t.Errorf("%v", err)
	}
}

func TestGitHubActionsGivesTheFederatedToken(t *testing.T) {
	httpClient, transport := httpfake.Client(func(r *http.Request) httpfake.Reply {
		if r.Header.Get("Authorization") != "Bearer gh" || r.URL.Query().Get("audience") != "api://AzureADTokenExchange" {
			return httpfake.Status(401, nil)
		}
		return httpfake.JSON(map[string]any{"value": "oidc"})
	})
	assertion, err := federatedAssertion(profile("workload-identity"), Options{HTTPClient: httpClient,
		Getenv: env(map[string]string{GitHubURLVariable: "https://token.actions.example.test/?x=1", GitHubTokenVariable: "gh"})})
	if err != nil {
		t.Fatal(err)
	}
	if value, err := assertion(context.Background()); err != nil || value != "oidc" {
		t.Errorf("%q %v (%v)", value, err, transport.Paths())
	}
}

type failing struct{ err error }

func (f failing) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{}, f.err
}

func TestALapsedAzureCLISignInSaysSignInAgain(t *testing.T) {
	provider := &SDK{Credential: failing{errors.New("AADSTS700082: The refresh token has expired")}, Method: "azure-cli"}
	_, err := provider.GetToken(context.Background(), "https://graph.microsoft.com", tenant)
	if !errs.Is(err, errs.Reauth) || !strings.Contains(err.Error(), "went unused") {
		t.Errorf("%v", err)
	}
	missing := &SDK{Credential: failing{errors.New("Azure CLI not found on path")}, Method: "azure-cli"}
	if _, err := missing.GetToken(context.Background(), "x", tenant); !strings.Contains(errs.HintOf(err), "device-code") {
		t.Errorf("%v", err)
	}
	other := &SDK{Credential: failing{errors.New("\n---\nboom\nmore")}, Method: "client-secret"}
	if _, err := other.GetToken(context.Background(), "x", tenant); err == nil || err.Error() != "client-secret: boom" {
		t.Errorf("%v", err)
	}
}

func TestLapseReasons(t *testing.T) {
	if LapseReason("AADSTS50076: MFA") == "" || LapseReason("Please run 'az login'") == "" || LapseReason("AADSTS90002") != "" {
		t.Error("lapse reasons")
	}
}

func TestScopeAndUnknownAuth(t *testing.T) {
	if Scope("https://management.azure.com/") != "https://management.azure.com/.default" {
		t.Error(Scope("https://management.azure.com/"))
	}
	if _, err := For(profile("password"), Options{}); !errs.Is(err, errs.Auth) {
		t.Errorf("%v", err)
	}
}
