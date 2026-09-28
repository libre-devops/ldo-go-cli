package news

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/tokenfake"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

func fixtures(t *testing.T) ([]Message, map[string]any) {
	t.Helper()
	var raw []fields.Object
	data, _ := os.ReadFile("testdata/messages.json")
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	var messages []Message
	for _, item := range raw {
		messages = append(messages, MessageFrom(item))
	}
	var expected map[string]any
	data, _ = os.ReadFile("testdata/expected.json")
	if err := json.Unmarshal(data, &expected); err != nil {
		t.Fatal(err)
	}
	return messages, expected
}

func TestTheRollupIsThePythonsOwn(t *testing.T) {
	messages, expected := fixtures(t)
	rollup := Rollup{Month: "2026-09", Messages: messages}
	if rollup.Title() != expected["rollup_title"] || rollup.Description() != expected["rollup"] {
		t.Errorf("got:\n%s\n%s\nwant:\n%s", rollup.Title(), rollup.Description(), expected["rollup"])
	}
	kept := Rollup{Month: "2026-09", Messages: messages[:2]}.Keeping(rollup.Description() + "\n- MC999 2026-01-01 old line  ")
	if kept.Description() != expected["kept"] {
		t.Errorf("kept:\n%s\nwant:\n%s", kept.Description(), expected["kept"])
	}
	if (Rollup{Month: "2026-09", Messages: messages[:1]}).Title() != expected["one"] {
		t.Error(Rollup{Month: "2026-09", Messages: messages[:1]}.Title())
	}
}

func TestTaskTitlesAndNotesAreThePythonsOwn(t *testing.T) {
	messages, expected := fixtures(t)
	tasks := expected["tasks"].([]any)
	index := 0
	for _, message := range messages {
		for _, layout := range []string{"sync", "short"} {
			want := tasks[index].(map[string]any)
			index++
			if got := TaskTitle(message, layout, 255); got != want["title"] {
				t.Errorf("%s %s title:\ngot  %q\nwant %q", message.ID, layout, got, want["title"])
			}
			if got := TaskTitle(message, layout, 60); got != want["short_title"] {
				t.Errorf("%s %s short title:\ngot  %q\nwant %q", message.ID, layout, got, want["short_title"])
			}
			if got := TaskNotes(message, layout); got != want["notes"] {
				t.Errorf("%s %s notes:\ngot  %q\nwant %q", message.ID, layout, got, want["notes"])
			}
		}
	}
}

func TestMonthsAndListed(t *testing.T) {
	found := Months(time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if len(found) != 2 || found[0].Name != "2026-07" || found[1].After != time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC) {
		t.Errorf("%+v", found)
	}
	if len(Months(time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC), time.Date(2027, 1, 2, 0, 0, 0, 0, time.UTC))) != 2 {
		t.Error("a year's end")
	}
	if ids := Listed("- MC1 x\n- mc2 y\n-MC3 z"); len(ids) != 2 || !ids["MC2"] {
		t.Error(ids)
	}
	if match := MessageKey.FindStringSubmatch("[Teams] Title [mc42]"); match == nil || match[1] != "mc42" {
		t.Error(match)
	}
	if MessageKey.MatchString("See MC42 for more") {
		t.Error("an id in the middle of a title is not the task's key")
	}
}

func TestMessagesAreFilteredAndARefusalSaysWhy(t *testing.T) {
	var query string
	httpClient, _ := httpfake.Client(httpfake.Routes(
		httpfake.Route{Match: "GET /v1.0/admin/serviceAnnouncement/messages/MC1", Reply: httpfake.JSON(map[string]any{"id": "MC1", "title": "one"})},
		httpfake.Route{Match: "GET /v1.0/admin/serviceAnnouncement/messages/MC2", Reply: httpfake.GraphError(403, "Forbidden", "no")},
		httpfake.Route{Match: "GET /v1.0/admin/serviceAnnouncement/messages", Func: func(r *http.Request) httpfake.Reply {
			query = r.URL.Query().Get("$filter")
			return httpfake.JSON(map[string]any{"value": []any{
				map[string]any{"id": "MC1", "services": []any{"Microsoft Defender XDR"}},
				map[string]any{"id": "MC2", "services": []any{"Exchange Online"}},
				map[string]any{"id": "MC3", "services": []any{"Microsoft Defender for Endpoint"}},
			}})
		}},
	))
	client, _ := New(microsoft.API{Tokens: &tokenfake.Provider{}, TenantID: "t", Cloud: microsoft.Public, HTTPClient: httpClient})
	found, err := client.Messages(context.Background(), Query{Since: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), Category: "Plan for change",
		Major: true, Services: []string{"defender"}, Limit: 1})
	if err != nil || len(found) != 1 || found[0].ID != "MC1" {
		t.Fatalf("%+v %v", found, err)
	}
	if query != "lastModifiedDateTime ge 2026-09-01T00:00:00Z and category eq 'planForChange' and isMajorChange eq true" {
		t.Error(query)
	}
	if message, err := client.Message(context.Background(), " mc1 "); err != nil || message.Title != "one" {
		t.Errorf("%+v %v", message, err)
	}
	if _, err := client.Message(context.Background(), "MC2"); !strings.Contains(errs.HintOf(err), "ServiceMessage.Read.All") {
		t.Errorf("%v", err)
	}
	for _, bad := range []string{"1183010", "MC-1"} {
		if _, err := client.Message(context.Background(), bad); !errs.Is(err, errs.Input) {
			t.Errorf("%s: %v", bad, err)
		}
	}
	if _, err := CategoryOf("urgent"); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
}
