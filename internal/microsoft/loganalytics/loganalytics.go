// Package loganalytics runs KQL against a Log Analytics (or Sentinel) workspace through
// the query API, and reads which tables it is receiving.
//
// Access rests on Azure RBAC on the workspace (Log Analytics Reader is enough), not on
// token scopes. The workspace is named by its Workspace ID (a GUID, on its Overview page),
// not by its resource id: package azure looks one up from the other.
package loganalytics

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/httpx"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

// Client runs Log Analytics queries for one tenant.
type Client struct {
	API *httpx.Client
}

// New is a Log Analytics client for api's tenant, in its cloud. A query can run for
// minutes; the server caps it at 10.
func New(api microsoft.API) (*Client, error) {
	bearer := api.Bearer(api.Cloud.LogAnalyticsURL)
	client, err := httpx.New(httpx.Options{BaseURL: api.Cloud.LogAnalyticsURL, Name: "Log Analytics", HTTPClient: api.HTTPClient,
		Token: &httpx.TokenSource{Token: bearer.Token, Refresh: bearer.Refresh}, Timeout: 10 * time.Minute})
	if err != nil {
		return nil, err
	}
	return &Client{API: client}, nil
}

// Query runs query and is its first table. timespan bounds the query from now backwards,
// on top of any time filter in the query. A partial result comes back with the service's
// error in its warnings rather than failing.
func (c *Client) Query(ctx context.Context, workspaceID, query string, timespan time.Duration) (render.QueryResult, error) {
	if microsoft.LooksLikeResourceID(workspaceID) {
		return render.QueryResult{}, errs.Inputf("that is the workspace's resource id (an ARM id), not its Workspace ID: '%s'",
			strings.TrimSpace(workspaceID)).WithHint("the query API wants the Workspace ID, a GUID on the workspace's Overview page")
	}
	id, err := util.RequireGUID(workspaceID, "a Log Analytics Workspace ID")
	if err != nil {
		return render.QueryResult{}, errs.As(err).WithHint("use the workspace's Workspace ID, a GUID on its Overview page")
	}
	if strings.TrimSpace(query) == "" {
		return render.QueryResult{}, errs.Inputf("the Log Analytics query is empty")
	}
	body := map[string]any{"query": query}
	if timespan > 0 {
		body["timespan"] = fmt.Sprintf("PT%dS", int64(timespan/time.Second))
	}
	data, err := c.API.Post(ctx, "/v1/workspaces/"+strings.ToLower(id)+"/query", body, nil)
	if err != nil {
		return render.QueryResult{}, err
	}
	tables := fields.Objects(data["tables"])
	var warnings []string
	if problem := fields.Map(data["error"]); len(problem) > 0 {
		detail := fields.Text(problem, "message")
		if detail == "" {
			detail = fields.Text(problem, "code")
		}
		if detail == "" {
			detail = "partial result"
		}
		if len(tables) == 0 {
			apiErr := errs.APIf("Log Analytics: %s", detail)
			apiErr.Code = fields.Text(problem, "code")
			return render.QueryResult{}, apiErr
		}
		warnings = append(warnings, detail)
	}
	if len(tables) == 0 {
		return render.QueryResult{Warnings: warnings}, nil
	}
	var columns []string
	for _, column := range fields.Objects(tables[0]["columns"]) {
		columns = append(columns, fields.String(column["name"]))
	}
	var rows [][]any
	for _, row := range fields.Items(tables[0]["rows"]) {
		if cells, ok := row.([]any); ok {
			rows = append(rows, cells)
		}
	}
	result := render.FromColumns(columns, rows, false)
	result.Warnings = warnings
	return result, nil
}
