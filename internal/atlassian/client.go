package atlassian

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/httpx"
)

// TokenPage is where an Atlassian account makes its API tokens.
const TokenPage = "https://id.atlassian.com/manage-profile/security/api-tokens"

// ErrorHints are what to say for Atlassian's errors, which carry no code, so go by status.
var ErrorHints = map[string]string{
	"HTTP 401": "the site did not accept the email and API token together: check the profile's email (or JIRA_EMAIL), " +
		"and that the token is current (" + TokenPage + ")",
	"HTTP 403": "the account can sign in but may not see this: ask for the project or space",
}

// APIToken is an Atlassian account's email and API token, sent with each request (HTTP
// Basic).
type APIToken struct {
	Email   string
	encoded string
}

// NewAPIToken is the credential for email's token.
func NewAPIToken(email, token string) (*APIToken, error) {
	if email == "" {
		return nil, errs.Authf("an API token needs its account's email")
	}
	if token == "" {
		return nil, errs.Authf("the API token is empty")
	}
	return &APIToken{Email: email, encoded: base64.StdEncoding.EncodeToString([]byte(email + ":" + token))}, nil
}

// String names the account only.
func (t *APIToken) String() string { return "APIToken(email=" + t.Email + ")" }

// GoString is String, so %#v leaves the token out too.
func (t *APIToken) GoString() string { return t.String() }

// Authorization is the value that follows Basic in the Authorization header.
func (t *APIToken) Authorization() string { return t.encoded }

// TokenFor is the profile's email with the token in its token_env, or an Auth error
// saying how to make one.
func TokenFor(profile Profile, getenv func(string) string) (*APIToken, error) {
	token := strings.TrimSpace(getenv(profile.TokenEnv))
	if token == "" {
		return nil, errs.Authf("no Atlassian API token in %s", profile.TokenEnv).
			WithHint("create one at %s, then export %s=<token>", TokenPage, profile.TokenEnv)
	}
	return NewAPIToken(profile.Email, token)
}

// NewClient is a client for the profile's site, named name (Jira or Confluence), reading
// with the token its token_env holds.
func NewClient(name string, profile Profile, getenv func(string) string, client *http.Client) (*httpx.Client, error) {
	if err := profile.RequireRealSite(); err != nil {
		return nil, err
	}
	token, err := TokenFor(profile, getenv)
	if err != nil {
		return nil, err
	}
	source := &httpx.TokenSource{Token: func(context.Context) (string, error) { return token.Authorization(), nil }}
	return httpx.New(httpx.Options{BaseURL: profile.Site, Token: source, AuthScheme: "Basic", Name: name, HTTPClient: client,
		ErrorHints: ErrorHints})
}
