package identity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

// federatedAssertion is where a workload identity's federated token comes from: a file
// (Kubernetes and most CI), else GitHub Actions' own token endpoint.
func federatedAssertion(profile microsoft.Profile, opts Options) (func(context.Context) (string, error), error) {
	if path := opts.getenv(TokenFileVariable); path != "" {
		return func(context.Context) (string, error) {
			text, err := os.ReadFile(path)
			if err != nil {
				return "", errs.Authf("cannot read the federated token in %s: %v", path, err)
			}
			return strings.TrimSpace(string(text)), nil
		}, nil
	}
	address, token := opts.getenv(GitHubURLVariable), opts.getenv(GitHubTokenVariable)
	if address != "" && token != "" {
		client := opts.HTTPClient
		if client == nil {
			client = http.DefaultClient
		}
		return func(ctx context.Context) (string, error) { return gitHubAssertion(ctx, client, address, token) }, nil
	}
	return nil, errs.Authf("profile %q has no federated token to exchange", profile.Name).
		WithHint("set %s, or run in GitHub Actions with 'permissions: id-token: write'", TokenFileVariable)
}

// gitHubAssertion asks GitHub Actions for an OIDC token whose audience Entra ID accepts.
func gitHubAssertion(ctx context.Context, client *http.Client, address, token string) (string, error) {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "https" {
		return "", errs.Authf("%s is not an https URL", GitHubURLVariable)
	}
	query := parsed.Query()
	query.Set("audience", "api://AzureADTokenExchange")
	parsed.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", errs.Authf("cannot ask GitHub for a token: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		return "", errs.Authf("cannot ask GitHub for a token: %v", err)
	}
	defer response.Body.Close()
	var body struct {
		Value string `json:"value"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&body) != nil || body.Value == "" {
		return "", errs.Authf("GitHub gave no OIDC token (HTTP %d)", response.StatusCode).
			WithHint("the job needs 'permissions: id-token: write'")
	}
	return body.Value, nil
}
