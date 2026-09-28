package planner

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/tokenfake"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

func TestKeyedFindsTheFirstTaskForEachKey(t *testing.T) {
	tasks := []Task{{ID: "a", Title: "MC1: first"}, {ID: "b", Title: " mc1: again"}, {ID: "c", Title: "nothing"}, {ID: "d", Title: "MC2 x"}}
	found := Keyed(tasks, regexp.MustCompile(`(?i)^MC[0-9]+`))
	if len(found) != 2 || found["MC1"].ID != "a" || found["MC2"].ID != "d" {
		t.Errorf("%v", found)
	}
}

func TestPlansByNameAndTheirMistakes(t *testing.T) {
	httpClient, _ := httpfake.Client(httpfake.Routes(httpfake.Route{Match: "GET /v1.0/me/planner/plans", Reply: httpfake.JSON(map[string]any{"value": []any{
		map[string]any{"id": "p1", "title": "Ops"}, map[string]any{"id": "p2", "title": "ops"}}})}))
	client, _ := New(microsoft.API{Tokens: &tokenfake.Provider{}, TenantID: "t", Cloud: microsoft.Public, HTTPClient: httpClient})
	if _, err := client.Plan(context.Background(), "OPS"); !errs.Is(err, errs.Ambiguous) {
		t.Errorf("%v", err)
	}
	if plan, err := client.Plan(context.Background(), "p2"); err != nil || plan.Title != "ops" {
		t.Errorf("%+v %v", plan, err)
	}
	for _, id := range []string{"short", "has spaces in it here", strings.Repeat("x", 70)} {
		if _, err := client.Tasks(context.Background(), id); !errs.Is(err, errs.Input) {
			t.Errorf("%s: %v", id, err)
		}
	}
	if _, err := client.CreateTask(context.Background(), "p", "b", " ", ""); !errs.Is(err, errs.Input) {
		t.Errorf("%v", err)
	}
	if cut(strings.Repeat("é", 300)) != strings.Repeat("é", TitleLimit) {
		t.Error("a title is cut by characters")
	}
}
