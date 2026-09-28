package servicenow

import (
	"net/http"
	"strings"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/snowfake"
)

func tables(t *testing.T, instance *snowfake.Fake) (*Tables, *httpfake.Transport) {
	t.Helper()
	client, transport := httpfake.Client(instance.Handler)
	basic, _ := NewBasic(snowfake.Username, snowfake.Password)
	found, err := NewTables(snowfake.Instance, basic.Source(), basic.Scheme(), client)
	if err != nil {
		t.Fatal(err)
	}
	return found, transport
}

func TestRecordsAreReadWithTheQueryAndFieldsAskedFor(t *testing.T) {
	client, transport := tables(t, snowfake.New())
	rows, err := client.Records(ctx, "sys_user", Query{Query: "user_name=ana", Fields: []string{"user_name"}})
	if err != nil || len(rows) != 1 || len(rows[0]) != 1 || rows[0]["user_name"] != "ana" {
		t.Fatal(rows, err)
	}
	seen := transport.Seen()[0]
	if !strings.HasPrefix(seen.Header.Get("Authorization"), "Basic ") || seen.URL.Query().Get("sysparm_exclude_reference_link") != "true" ||
		seen.URL.Query().Get("sysparm_display_value") != "false" {
		t.Error(seen.URL)
	}
	if _, err := client.Records(ctx, "sys_user; drop", Query{}); !errs.Is(err, errs.Input) {
		t.Error(err)
	}
}

func TestRecordsArePagedUntilAShortPageOrTheLimit(t *testing.T) {
	instance := snowfake.New()
	for number := range 7 {
		instance.Tables["incident"] = append(instance.Tables["incident"], snowfake.Row{"number": number})
	}
	PageSize = 3
	defer func() { PageSize = 1000 }()
	client, transport := tables(t, instance)
	if rows, _ := client.Records(ctx, "incident", Query{}); len(rows) != 7 || len(transport.Seen()) != 3 {
		t.Error(len(rows), len(transport.Seen()))
	}
	if rows, _ := client.Records(ctx, "incident", Query{Limit: 4}); len(rows) != 4 {
		t.Error(len(rows))
	}
}

func TestFirstIsTheFirstMatchOrNone(t *testing.T) {
	client, _ := tables(t, snowfake.New())
	if row, found, _ := client.First(ctx, "sys_user", "user_name=ana"); !found || row["name"] != "Ana Analyst" {
		t.Error(row)
	}
	if _, found, err := client.First(ctx, "sys_user", "user_name=nobody"); found || err != nil {
		t.Error(found, err)
	}
}

func TestTheInstanceSayingNoComesWithAHint(t *testing.T) {
	for _, test := range []struct {
		setup func(*snowfake.Fake)
		hint  string
	}{
		{func(f *snowfake.Fake) { f.BasicAllowed = false }, "snc_basic_auth_api_access"},
		{func(f *snowfake.Fake) { f.Denied["sys_user"] = true }, "lacks a role"},
		{func(f *snowfake.Fake) { delete(f.Tables, "sys_user") }, "plugin or app is not installed"},
		{func(f *snowfake.Fake) { f.Hibernating = true }, "hibernating"},
	} {
		instance := snowfake.New()
		test.setup(instance)
		client, _ := tables(t, instance)
		_, err := client.Records(ctx, "sys_user", Query{})
		if found := errs.As(err); found == nil || !strings.Contains(found.Hint, test.hint) {
			t.Errorf("%s: %v", test.hint, err)
		}
	}
}

func TestAResponseWithoutAResultListIsAnError(t *testing.T) {
	client, _ := httpfake.Client(func(*http.Request) httpfake.Reply { return httpfake.JSON(map[string]any{"records": []any{}}) })
	found, _ := NewTables(snowfake.Instance, nil, "", client)
	if _, err := found.Records(ctx, "sys_user", Query{}); err == nil || !strings.Contains(err.Error(), "no result list") {
		t.Error(err)
	}
	if _, err := NewTables("http://dev12345.service-now.com", nil, "", client); err == nil {
		t.Error("plain http was taken")
	}
}

func TestAConditionRefusesAValueThatWouldAddConditions(t *testing.T) {
	if got, _ := Condition("name", "web01", ""); got != "name=web01" {
		t.Error(got)
	}
	if got, _ := Condition("name", "web", "LIKE"); got != "nameLIKEweb" {
		t.Error(got)
	}
	for _, value := range []string{"web01^ORactive=true", "web01\nactive=true"} {
		if _, err := Condition("name", value, ""); !errs.Is(err, errs.Input) || !strings.Contains(err.Error(), "cannot be used") {
			t.Error(err)
		}
	}
}
