package graph

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/tokenfake"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

func client(t *testing.T, handler httpfake.Handler) (*Client, *httpfake.Transport) {
	t.Helper()
	httpClient, transport := httpfake.Client(handler)
	built, err := New(microsoft.API{Tokens: &tokenfake.Provider{}, TenantID: "t", Cloud: microsoft.Public, HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	return built, transport
}

func TestPaths(t *testing.T) {
	cases := map[string]string{
		"users":                              "/v1.0/users",
		"/beta/me":                           "/beta/me",
		"v1.0/devices":                       "/v1.0/devices",
		"users?$top=1":                       "/v1.0/users?$top=1",
		"https://graph.microsoft.com/v1.0/x": "https://graph.microsoft.com/v1.0/x",
	}
	for path, want := range cases {
		if got, err := Path(path, false); err != nil || got != want {
			t.Errorf("%s: %s %v", path, got, err)
		}
	}
	if got, _ := Path("me", true); got != "/beta/me" {
		t.Error(got)
	}
	for _, bad := range []string{"", "users/../me", `users\me`, "users me"} {
		if _, err := Path(bad, false); !errs.Is(err, errs.Input) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if _, err := Path("v1.0/me", true); err == nil {
		t.Error("--beta and v1.0 agree")
	}
}

func TestISODurations(t *testing.T) {
	for span, want := range map[time.Duration]string{7 * 24 * time.Hour: "P7D", 6 * time.Hour: "PT6H",
		30 * time.Minute: "PT30M", 45 * time.Second: "PT45S"} {
		if got, _ := ISODuration(span); got != want {
			t.Errorf("%v: %s", span, got)
		}
	}
	if _, err := ISODuration(0); err == nil {
		t.Error("zero is not a timespan")
	}
}

func TestAPageStopsAtTheLimit(t *testing.T) {
	c, transport := client(t, func(r *http.Request) httpfake.Reply {
		return httpfake.JSON(map[string]any{"value": []any{map[string]any{"id": "1"}, map[string]any{"id": "2"}},
			"@odata.nextLink": "https://graph.microsoft.com/v1.0/users?$skiptoken=n"})
	})
	page, err := c.Page(context.Background(), "users", nil, PageOptions{Limit: 3})
	if err != nil || len(page.Items) != 3 || !page.More {
		t.Errorf("%+v %v", page, err)
	}
	if len(transport.Seen()) != 2 {
		t.Errorf("%d requests", len(transport.Seen()))
	}
}

func TestAPageOfAnObjectIsAnError(t *testing.T) {
	c, _ := client(t, func(*http.Request) httpfake.Reply { return httpfake.JSON(map[string]any{"id": "1"}) })
	if _, err := c.Page(context.Background(), "me", nil, PageOptions{}); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
}

func TestLookupByIDFallsBackToTheOtherID(t *testing.T) {
	id := "66666666-6666-6666-6666-666666666666"
	c, transport := client(t, httpfake.Routes(
		httpfake.Route{Match: "GET /v1.0/devices/" + id, Reply: httpfake.GraphError(404, "Request_ResourceNotFound", "no")},
		httpfake.Route{Match: "GET /v1.0/devices", Reply: httpfake.JSON(map[string]any{"value": []any{map[string]any{"id": "d1"}}})},
	))
	found, err := c.Lookup(context.Background(), "device", id, "id")
	if err != nil || len(found) != 1 {
		t.Fatalf("%v %v", found, err)
	}
	if query := transport.Seen()[1].URL.Query(); query.Get("$filter") != "deviceId eq '"+id+"'" || query.Get("$select") != "id" {
		t.Errorf("%v", query)
	}
	group, _ := client(t, httpfake.Routes(httpfake.Route{Match: "GET /v1.0/groups/", Reply: httpfake.GraphError(404, "x", "no")}))
	if found, err := group.Lookup(context.Background(), "group", id, ""); err != nil || found != nil {
		t.Errorf("%v %v", found, err)
	}
}

func TestLookupErrors(t *testing.T) {
	c, _ := client(t, httpfake.Routes(httpfake.Route{Match: "GET /v1.0/users/", Reply: httpfake.GraphError(403, "x", "denied")}))
	if _, err := c.Lookup(context.Background(), "robot", "x", ""); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
	if _, err := c.Lookup(context.Background(), "user", " ", ""); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
	if _, err := c.Lookup(context.Background(), "user", "ana@corp.example", ""); !errs.Is(err, errs.API) {
		t.Errorf("%v", err)
	}
}

func TestAServicePrincipalOnlyForAnAppID(t *testing.T) {
	c, transport := client(t, nil)
	if found, err := c.ServicePrincipal(context.Background(), "not-a-guid"); found != nil || err != nil || len(transport.Seen()) != 0 {
		t.Errorf("%v %v", found, err)
	}
}

func TestASuspendedServiceKeepsItsOwnHint(t *testing.T) {
	c, _ := client(t, func(*http.Request) httpfake.Reply {
		return httpfake.GraphError(403, "Forbidden", "The tenant is suspended")
	})
	_, err := c.Hunt(context.Background(), "DeviceInfo", 0)
	if errs.HintOf(err) == HuntHint {
		t.Errorf("%v", err)
	}
	if _, err := c.Hunt(context.Background(), " ", 0); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
}

func TestHuntWithoutASchemaUsesTheRows(t *testing.T) {
	c, _ := client(t, func(*http.Request) httpfake.Reply {
		return httpfake.JSON(map[string]any{"results": []any{map[string]any{"b": 1, "a": 2}}})
	})
	result, err := c.Hunt(context.Background(), "x", time.Hour)
	if err != nil || len(result.Columns) != 2 || result.Columns[0] != "a" {
		t.Errorf("%+v %v", result, err)
	}
}

func TestNotFoundNamesWhatWasTried(t *testing.T) {
	if err := NotFound("user", "ana"); err.Error() != "no user is named 'ana'" {
		t.Error(err)
	}
}
