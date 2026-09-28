package microsoft

import (
	"net/http"

	"github.com/libre-devops/ldo-go-cli/internal/core/auth"
	"github.com/libre-devops/ldo-go-cli/internal/core/httpx"
)

// GraphErrorHints are Graph error codes that say more than the HTTP status does: a 403
// is as often a missing licence or role as a missing scope.
var GraphErrorHints = map[string]string{
	"Authentication_RequestFromNonPremiumTenantOrB2CTenant": "this needs a Microsoft Entra ID P1 or P2 licence in the tenant, " +
		"which it does not have (or it is a B2C tenant), whatever the token's scopes",
	"Authentication_RequestFromUnsupportedUserRole": "this is limited to some Entra roles (such as Reports Reader, " +
		"Security Reader or Global Reader), and the signed-in user has none of them active: activate one with PIM if it is eligible",
	// Planner, To Do and others answer it when the service is not on in the tenant.
	"TenantDisabled": "the service this calls is not available in the tenant, usually for want of a licence that " +
		"includes it (Planner needs a Microsoft 365 licence with Planner), whatever the token's scopes",
}

// API is how a feature's client is built: the tokens, the tenant, and the HTTP client
// every call goes through (the proxy and certificates; a fake in tests).
type API struct {
	Tokens     auth.TokenProvider
	TenantID   string
	Cloud      Cloud
	HTTPClient *http.Client
}

// ForProfile is an API for a profile's tenant, in its cloud.
func ForProfile(profile Profile, tokens auth.TokenProvider, httpClient *http.Client) API {
	return API{Tokens: tokens, TenantID: profile.TenantID, Cloud: profile.Cloud, HTTPClient: httpClient}
}

// Bearer is the API's tokens for resource, in its tenant.
func (a API) Bearer(resource string) auth.Bearer {
	return auth.Bearer{Provider: a.Tokens, Resource: resource, TenantID: a.TenantID}
}

// Client is an httpx client for baseURL, with tokens for resource in the API's tenant.
func (a API) Client(name, baseURL, resource string, hints map[string]string) (*httpx.Client, error) {
	bearer := a.Bearer(resource)
	return httpx.New(httpx.Options{
		BaseURL: baseURL, Name: name, HTTPClient: a.HTTPClient, ErrorHints: hints,
		Token: &httpx.TokenSource{Token: bearer.Token, Refresh: bearer.Refresh},
	})
}

// Graph is a Microsoft Graph client.
func (a API) Graph(name string) (*httpx.Client, error) {
	if name == "" {
		name = "Microsoft Graph"
	}
	return a.Client(name, a.Cloud.GraphURL, a.Cloud.GraphURL, GraphErrorHints)
}

// ARM is an Azure Resource Manager client.
func (a API) ARM(name string) (*httpx.Client, error) {
	if name == "" {
		name = "Azure Resource Manager"
	}
	return a.Client(name, a.Cloud.ARMURL, a.Cloud.ARMAudience(), nil)
}
