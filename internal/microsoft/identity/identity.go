// Package identity builds the credential a profile's auth setting names, on Microsoft's
// own Go libraries: azidentity for the Azure CLI, client secrets, workload identity and
// managed identity, and MSAL for a person's own sign-in (device code or browser), which
// needs no az at all.
//
// Secrets never come from the config file. They come from the environment, under the
// same names the Azure SDKs use, so a CI job configured for one works for the other.
package identity

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/libre-devops/ldo-go-cli/internal/core/auth"
	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/tokenstore"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

// The environment variables the Azure SDKs read, read here too.
const (
	SecretVariable      = "AZURE_CLIENT_SECRET"
	TokenFileVariable   = "AZURE_FEDERATED_TOKEN_FILE"
	GitHubURLVariable   = "ACTIONS_ID_TOKEN_REQUEST_URL"
	GitHubTokenVariable = "ACTIONS_ID_TOKEN_REQUEST_TOKEN"
)

// AzureCLIClientID is the Azure CLI's own public client: a delegated sign-in without a
// client_id of its own uses it, as azidentity does by default.
const AzureCLIClientID = "04b07795-8ddb-461a-bbee-02f9e1bf7b46"

// Options are what a credential is built with. All are optional.
type Options struct {
	// HTTPClient sends every request a credential makes: the proxy and certificates the
	// rest of the tool uses. Tests give a fake.
	HTTPClient *http.Client
	// Getenv reads the environment; os.Getenv when nil.
	Getenv func(string) string
	// Notify shows a person what they must see now: a device code and where to enter it.
	Notify func(string)
	// Store keeps a delegated sign-in; the profile's token_cache opens one when nil.
	Store tokenstore.Store
	// Now is time.Now when nil.
	Now func() time.Time
}

func (o Options) getenv(name string) string {
	if o.Getenv != nil {
		return o.Getenv(name)
	}
	return os.Getenv(name)
}

// For is the token provider for profile: an Auth error when its inputs are missing.
func For(profile microsoft.Profile, opts Options) (auth.TokenProvider, error) {
	switch profile.Auth {
	case "", "azure-cli":
		credential, err := azidentity.NewAzureCLICredential(&azidentity.AzureCLICredentialOptions{
			AdditionallyAllowedTenants: []string{"*"},
		})
		if err != nil {
			return nil, errs.Authf("cannot use the Azure CLI: %v", err)
		}
		return &SDK{Credential: credential, Method: "azure-cli"}, nil
	case "interactive", "device-code":
		return newDelegated(profile, opts)
	case "managed-identity":
		identityOpts := &azidentity.ManagedIdentityCredentialOptions{ClientOptions: clientOptions(profile, opts)}
		if profile.ClientID != "" {
			identityOpts.ID = azidentity.ClientID(profile.ClientID)
		}
		credential, err := azidentity.NewManagedIdentityCredential(identityOpts)
		if err != nil {
			return nil, errs.Authf("cannot use the managed identity: %v", err)
		}
		return &SDK{Credential: credential, Method: profile.Auth}, nil
	case "client-secret":
		return clientSecret(profile, opts)
	case "workload-identity":
		return workloadIdentity(profile, opts)
	}
	return nil, errs.Authf("profile %q has an unknown auth method %q", profile.Name, profile.Auth)
}

func clientOptions(profile microsoft.Profile, opts Options) azcore.ClientOptions {
	options := azcore.ClientOptions{Cloud: cloud.Configuration{ActiveDirectoryAuthorityHost: profile.Cloud.LoginURL + "/"}}
	if opts.HTTPClient != nil {
		options.Transport = opts.HTTPClient
	}
	return options
}

func clientSecret(profile microsoft.Profile, opts Options) (auth.TokenProvider, error) {
	secret := opts.getenv(SecretVariable)
	if secret == "" {
		return nil, errs.Authf("profile %q needs a client secret", profile.Name).
			WithHint("set %s; the config file never holds secrets", SecretVariable)
	}
	credential, err := azidentity.NewClientSecretCredential(profile.TenantID, profile.ClientID, secret,
		&azidentity.ClientSecretCredentialOptions{ClientOptions: clientOptions(profile, opts), AdditionallyAllowedTenants: []string{"*"}})
	if err != nil {
		return nil, errs.Authf("cannot use the client secret: %v", err)
	}
	return &SDK{Credential: credential, Method: profile.Auth}, nil
}

func workloadIdentity(profile microsoft.Profile, opts Options) (auth.TokenProvider, error) {
	assertion, err := federatedAssertion(profile, opts)
	if err != nil {
		return nil, err
	}
	credential, err := azidentity.NewClientAssertionCredential(profile.TenantID, profile.ClientID, assertion,
		&azidentity.ClientAssertionCredentialOptions{ClientOptions: clientOptions(profile, opts), AdditionallyAllowedTenants: []string{"*"}})
	if err != nil {
		return nil, errs.Authf("cannot use the federated token: %v", err)
	}
	return &SDK{Credential: credential, Method: profile.Auth}, nil
}

// Scope is the scope for a resource's token: its URL and /.default.
func Scope(resource string) string { return strings.TrimRight(resource, "/") + "/.default" }

// SDK is a token provider over one of azidentity's credentials.
type SDK struct {
	Credential azcore.TokenCredential
	Method     string
}

// GetToken is a token for resource in tenantID.
func (s *SDK) GetToken(ctx context.Context, resource, tenantID string) (auth.AccessToken, error) {
	token, err := s.Credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{Scope(resource)}, TenantID: tenantID})
	if err != nil {
		return auth.AccessToken{}, s.explain(err, tenantID)
	}
	return auth.AccessToken{Token: token.Token, ExpiresOn: token.ExpiresOn.UTC(), TenantID: strings.ToLower(tenantID), Resource: resource}, nil
}

func (s *SDK) explain(err error, tenantID string) error {
	message := err.Error()
	if s.Method == "azure-cli" {
		if reason := LapseReason(message); reason != "" {
			return errs.Reauthf(tenantID, reason, "the Azure CLI's sign-in to tenant %s has lapsed: %s", tenantID, reason).
				WithHint("sign in again with %s or 'az login --tenant %s'", brand.Suggest("az use <profile>"), tenantID)
		}
		if strings.Contains(message, "executable file not found") || strings.Contains(message, "Azure CLI not found") {
			return errs.Authf("the Azure CLI is not installed, and the profile signs in through it").
				WithHint(`install it, or sign in without it: auth = "device-code" in the profile`)
		}
	}
	return errs.Authf("%s: %s", s.Method, firstLine(message))
}

func firstLine(message string) string {
	for _, line := range strings.Split(message, "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "---") {
			return line
		}
	}
	return message
}
