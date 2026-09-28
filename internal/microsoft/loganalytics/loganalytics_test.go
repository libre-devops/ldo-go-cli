package loganalytics

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/tokenfake"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

const workspace = "AAAAAAAA-1111-2222-3333-444444444444"

var now = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

func client(t *testing.T, handler httpfake.Handler) (*Client, *tokenfake.Provider) {
	t.Helper()
	httpClient, _ := httpfake.Client(handler)
	tokens := &tokenfake.Provider{}
	built, err := New(microsoft.API{Tokens: tokens, TenantID: "t", Cloud: microsoft.Public, HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	return built, tokens
}

func TestAQueryIsItsFirstTableInColumnOrder(t *testing.T) {
	var body map[string]any
	c, tokens := client(t, httpfake.Routes(httpfake.Route{Match: "POST /v1/workspaces/" + strings.ToLower(workspace) + "/query", Func: func(r *http.Request) httpfake.Reply {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		return httpfake.JSON(map[string]any{"tables": []any{map[string]any{
			"columns": []any{map[string]any{"name": "Computer"}, map[string]any{"name": "Count"}},
			"rows":    []any{[]any{"web01", 3}, "not a row"}}},
			"error": map[string]any{"message": "partial: 1 shard failed"}})
	}}))
	result, err := c.Query(context.Background(), workspace, "Heartbeat", 24*time.Hour)
	if err != nil || strings.Join(result.Columns, ",") != "Computer,Count" || len(result.Rows) != 1 || result.Warnings[0] != "partial: 1 shard failed" {
		t.Fatalf("%+v %v", result, err)
	}
	if body["timespan"] != "PT86400S" || tokens.Asked()[0] != "https://api.loganalytics.io t" {
		t.Errorf("%v %v", body, tokens.Asked())
	}
}

func TestQueryMistakes(t *testing.T) {
	c, _ := client(t, httpfake.Routes(httpfake.Route{Match: "POST /v1/workspaces/", Reply: httpfake.JSON(map[string]any{"error": map[string]any{"code": "BadArgumentError"}})}))
	for _, id := range []string{"/subscriptions/x/resourceGroups/rg", "law-soc"} {
		if _, err := c.Query(context.Background(), id, "x", 0); !errs.Is(err, errs.Input) || errs.HintOf(err) == "" {
			t.Errorf("%s: %v", id, err)
		}
	}
	if _, err := c.Query(context.Background(), workspace, " ", 0); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
	if _, err := c.Query(context.Background(), workspace, "x", 0); !errs.Is(err, errs.API) {
		t.Errorf("%v", err)
	}
}

func TestIngestionQuietTablesFirst(t *testing.T) {
	query, err := IngestionQuery(7 * 24 * time.Hour)
	if err != nil || !strings.Contains(query, "ago(168h)") {
		t.Errorf("%s %v", query, err)
	}
	for _, bad := range []time.Duration{time.Minute, 800 * 24 * time.Hour} {
		if _, err := IngestionQuery(bad); !errs.Is(err, errs.Input) {
			t.Errorf("%v: %v", bad, err)
		}
	}
	c, _ := client(t, httpfake.Routes(httpfake.Route{Match: "POST /v1/workspaces/", Reply: httpfake.JSON(map[string]any{"tables": []any{map[string]any{
		"columns": []any{map[string]any{"name": "DataType"}, map[string]any{"name": "LastData"}, map[string]any{"name": "Megabytes"},
			map[string]any{"name": "BillableMegabytes"}, map[string]any{"name": "Solutions"}},
		"rows": []any{
			[]any{"SecurityEvent", "2026-09-24T11:00:00Z", 5000.0, 5000.0, "Security, SecurityCenterFree"},
			[]any{"Syslog", "2026-09-20T00:00:00Z", 10.0, 10.0, ""},
			[]any{"Heartbeat", "2026-09-22T00:00:00Z", 1.0, 0.0, "LogManagement"},
			[]any{"Perf", "2026-09-24T11:30:00Z", 9000.0, 9000.0, "LogManagement"},
			[]any{"", "", 0, 0, ""},
		}}}})}))
	result, _ := c.Query(context.Background(), workspace, query, 0)
	tables := ByQuietest(ReadIngestion(result), now, 24*time.Hour)
	var order []string
	for _, table := range tables {
		order = append(order, table.Table)
	}
	if strings.Join(order, ",") != "Syslog,Heartbeat,Perf,SecurityEvent" {
		t.Error(order)
	}
	if tables[3].Gigabytes != 5 || len(tables[3].Solutions) != 2 || tables[1].BillableGigabytes != 0 {
		t.Errorf("%+v", tables[3])
	}
	if _, known := (Table{}).QuietFor(now); known {
		t.Error("an unknown last data is quiet for a time")
	}
}
