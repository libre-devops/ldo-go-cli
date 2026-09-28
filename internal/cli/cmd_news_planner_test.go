package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/fakes/graphfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
)

const (
	planID   = "plan0000000000000000000001"
	bucketID = "bucket00000000000000000001"
)

// board is a plan with one bucket and two tasks (one the sync's, one a rollup), and
// Message Center with three posts; every write it is sent is recorded.
type board struct {
	mu     sync.Mutex
	writes []string
}

func (b *board) handler() httpfake.Handler {
	posts := graphfake.Page(
		map[string]any{"id": "MC3", "title": "Teams change", "services": []any{"Microsoft Teams"}, "severity": "high", "category": "planForChange",
			"lastModifiedDateTime": "2026-09-23T10:00:00Z", "body": map[string]any{"content": "<p>Hello</p>"}},
		map[string]any{"id": "MC2", "title": "Defender change", "services": []any{"Microsoft Defender XDR"}, "lastModifiedDateTime": "2026-09-22T10:00:00Z"},
		map[string]any{"id": "MC1", "title": "Old", "services": []any{"Exchange Online"}, "lastModifiedDateTime": "2026-09-21T10:00:00Z"},
	)
	return func(r *http.Request) httpfake.Reply {
		if r.Method != http.MethodGet {
			body, _ := io.ReadAll(r.Body)
			b.mu.Lock()
			b.writes = append(b.writes, r.Method+" "+r.URL.Path+" "+r.Header.Get("If-Match")+" "+string(body))
			b.mu.Unlock()
			if r.Method == http.MethodPost {
				return httpfake.JSON(map[string]any{"id": "task0000000000000000000099", "title": "x"})
			}
			return httpfake.Reply{Status: 204}
		}
		switch path := r.URL.Path; {
		case strings.HasPrefix(path, "/v1.0/admin/serviceAnnouncement/messages/MC3"):
			return httpfake.JSON(posts["value"].([]any)[0])
		case strings.HasPrefix(path, "/v1.0/admin/serviceAnnouncement/messages"):
			return httpfake.JSON(posts)
		case path == "/v1.0/me/planner/plans":
			return httpfake.JSON(graphfake.Page(map[string]any{"id": planID, "title": "Operations", "createdDateTime": "2026-01-01T00:00:00Z"}))
		case strings.HasSuffix(path, "/buckets"):
			return httpfake.JSON(graphfake.Page(map[string]any{"id": bucketID, "name": "Message Center", "planId": planID}))
		case strings.HasSuffix(path, "/tasks"):
			return httpfake.JSON(graphfake.Page(
				map[string]any{"id": "task0000000000000000000001", "title": "[Microsoft Defender XDR] Defender change [MC2]", "bucketId": bucketID,
					"percentComplete": 100, "@odata.etag": "W/\"t1\""},
				map[string]any{"id": "task0000000000000000000002", "title": "Message Center rollup: 2026-09 (1 message)", "bucketId": bucketID,
					"percentComplete": 50, "@odata.etag": "W/\"t2\""}))
		case strings.HasSuffix(path, "/details"):
			return httpfake.JSON(map[string]any{"description": "- MC1 2026-09-21 [Exchange Online] Old", "@odata.etag": "W/\"d1\""})
		}
		return httpfake.Status(404, nil)
	}
}

func TestNewsMessagesAndOne(t *testing.T) {
	b := &board{}
	h := newHarness(t, b.handler())
	contains(t, h.ok("news", "messages", "--service", "teams"), "MC3", "plan for change", "Teams change")
	contains(t, h.err.String(), "1 post(s) changed in the last 30d (profile dev)")
	h.ok("news", "messages", "--date", "2026-09-20..2026-09-22", "--security")
	contains(t, h.err.String(), "1 post(s) changed 2026-09-20..2026-09-22")
	contains(t, h.transport.Seen()[len(h.transport.Seen())-1].URL.Query().Get("$filter"), "lastModifiedDateTime lt 2026-09-23T00:00:00Z")
	contains(t, h.ok("news", "message", "MC3"), "Severity      high", "Hello")
	if out := h.ok("news", "message", "mc3", "--markdown"); out != "Hello\n" {
		t.Errorf("%q", out)
	}
	contains(t, usageError(h.fails(2, "news", "messages", "--date", "today", "--since", "1d")), "give one of --date and --since")
}

func TestPlannerListings(t *testing.T) {
	b := &board{}
	h := newHarness(t, b.handler())
	contains(t, h.ok("planner", "plans"), "Operations")
	contains(t, h.ok("planner", "buckets", "operations"), "Message Center")
	out := h.ok("planner", "tasks", "Operations", "--open")
	contains(t, out, "Message Center rollup", "50%")
	if strings.Contains(out, "Defender change") {
		t.Error("a done task was listed with --open")
	}
	contains(t, h.fails(1, "planner", "buckets", "Nothing"), "no plan 'Nothing' among yours", "there are: Operations")
}

func TestAddNewsOnlySaysWhatItWouldRaiseUnlessWrite(t *testing.T) {
	b := &board{}
	h := newHarness(t, b.handler())
	if code := h.run("planner", "add-news", "Operations", "--bucket", "message center"); code != ExitAttention {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	contains(t, h.out.String(), "MC3", "to raise", "MC2", "raised already")
	contains(t, h.err.String(), "2 to raise in Operations / Message Center, 1 raised already (posts changed in the last 7d; --write raises them)")
	if len(b.writes) != 0 {
		t.Fatalf("wrote without --write: %v", b.writes)
	}
	h.ok("planner", "add-news", "Operations", "--bucket", "Message Center", "--write", "--service", "teams")
	contains(t, h.err.String(), "raised 1 task(s) in Operations / Message Center; 0 had one")
	var created map[string]any
	_ = json.Unmarshal([]byte(strings.SplitN(b.writes[0], " ", 4)[3]), &created)
	if created["title"] != "[Microsoft Teams] Teams change [MC3]" || created["planId"] != planID {
		t.Errorf("%v", b.writes)
	}
	contains(t, b.writes[1], "PATCH /v1.0/planner/tasks/task0000000000000000000099/details W/\"d1\"", "Message ID: MC3", "Hello")
	contains(t, usageError(h.fails(2, "planner", "add-news", "Operations", "--bucket", "x", "--layout", "long")), "--layout must be sync or short")
}

func TestAddRollupUpdatesTheMonthItHas(t *testing.T) {
	b := &board{}
	h := newHarness(t, b.handler())
	if code := h.run("planner", "add-rollup", "Operations", "--bucket", "Message Center", "--date", "2026-09-01.."); code != ExitAttention {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	contains(t, h.out.String(), "2026-09  3      2    to update")
	contains(t, h.err.String(), "0 to raise and 1 to update in Operations / Message Center, 0 up to date")
	h.ok("planner", "add-rollup", "Operations", "--bucket", "Message Center", "--date", "2026-09-01..", "--write")
	contains(t, h.err.String(), "raised 0 and updated 1 rollup(s)")
	contains(t, b.writes[0], "PATCH /v1.0/planner/tasks/task0000000000000000000002 W/\"t2\"", "Message Center rollup: 2026-09 (3 messages)")
	contains(t, usageError(h.fails(2, "planner", "add-rollup", "Operations", "--bucket", "x", "--date", "..2026-09-01")), "give the first day")
}
