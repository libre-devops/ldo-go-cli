package confluence

import (
	"context"
	"strings"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/atlassian"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/atlassianfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
)

var ctx = context.Background()

func client(t *testing.T) (*Client, *httpfake.Transport) {
	t.Helper()
	http, transport := httpfake.Client(atlassianfake.New().Handler)
	profile := atlassian.Profile{Name: "env", Site: atlassianfake.Site, Email: atlassianfake.Email, TokenEnv: atlassian.TokenEnv}
	found, err := New(profile, func(name string) string { return atlassianfake.Env[name] }, http)
	if err != nil {
		t.Fatal(err)
	}
	return found, transport
}

func TestSpacesFollowTheNextLink(t *testing.T) {
	confluence, transport := client(t)
	spaces, err := confluence.Spaces(ctx)
	if err != nil || len(spaces) != 2 || spaces[1].Key != "~acc1" || spaces[0].URL != "https://contoso.atlassian.net/wiki/spaces/OPS" {
		t.Fatal(spaces, err)
	}
	if transport.Seen()[1].URL.RawQuery != "cursor=2" {
		t.Error(transport.Seen()[1].URL)
	}
	if space, err := confluence.Space(ctx, "~acc1"); err != nil || space.Name != "Ana" {
		t.Error(space, err)
	}
	if _, err := confluence.Space(ctx, "DEV"); !errs.Is(err, errs.NotFound) {
		t.Error(err)
	}
	if _, err := confluence.Space(ctx, "OPS/../x"); !errs.Is(err, errs.Input) {
		t.Error(err)
	}
}

func TestPagesAreFoundBySpaceAndTitle(t *testing.T) {
	confluence, _ := client(t)
	inOps, err := confluence.Pages(ctx, "OPS", "", 50)
	if err != nil || len(inOps) != 2 || inOps[0].Version != 3 || inOps[0].SpaceID != "1" || inOps[0].Updated.IsZero() {
		t.Fatal(inOps, err)
	}
	if titled, _ := confluence.Pages(ctx, "", "Notes", 50); len(titled) != 1 || titled[0].ID != "201" {
		t.Error(titled)
	}
	if limited, _ := confluence.Pages(ctx, "", "", 2); len(limited) != 2 {
		t.Error(limited)
	}
	if _, err := confluence.Pages(ctx, "", "", 0); !errs.Is(err, errs.Input) {
		t.Error(err)
	}
	if _, err := confluence.Pages(ctx, "DEV", "", 5); !errs.Is(err, errs.NotFound) {
		t.Error(err)
	}
}

func TestAPageCarriesItsBodyAsMarkdown(t *testing.T) {
	confluence, _ := client(t)
	page, err := confluence.Page(ctx, "101")
	if err != nil || page.Title != "Runbook" || page.Body != "## Restart\n\nRun `systemctl restart app`." ||
		page.URL != "https://contoso.atlassian.net/wiki/spaces/OPS/pages/101/Runbook" {
		t.Fatalf("%+v %v", page, err)
	}
	if _, err := confluence.Page(ctx, "0101"); !errs.Is(err, errs.Input) {
		t.Error(err)
	}
	if _, err := confluence.Page(ctx, "999"); err == nil || !strings.Contains(err.Error(), "no such page") {
		t.Error(err)
	}
}

func TestSearchFindsByCQL(t *testing.T) {
	confluence, _ := client(t)
	hits, err := confluence.Search(ctx, `type = page AND text ~ "restart"`, 25)
	if err != nil || len(hits) != 1 || hits[0].Type != "page" || hits[0].Space != "Operations" || hits[0].Excerpt != "restart the app" ||
		hits[0].URL != "https://contoso.atlassian.net/wiki/spaces/OPS/pages/101/Runbook" {
		t.Fatal(hits, err)
	}
	if _, err := confluence.Search(ctx, "x", 0); !errs.Is(err, errs.Input) {
		t.Error(err)
	}
	fallback := SearchHitFrom(map[string]any{"entityType": "space", "content": map[string]any{"title": "Ops"}}, "https://s")
	if fallback.Type != "space" || fallback.Title != "Ops" || fallback.URL != "" {
		t.Error(fallback)
	}
}
