package servicenow

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/httpx"
)

// The ServiceNow Table API: read records from any table, a page at a time.
//
// Every ServiceNow feature reads through this. It turns the instance's errors into ones
// that say what to do: a refused sign-in, a missing role (an ACL), a table whose plugin is
// not installed, or a developer instance that is hibernating and answers with a web page.

// PageSize is how many records one request asks for.
var PageSize = 1000

// The hints for the ways an instance says no.
const (
	HibernatingHint = "the instance answered with a web page, not JSON: a developer instance may be " +
		"hibernating (wake it at developer.servicenow.com), or the address is not an instance"
	ACLHint = "your account lacks a role (an access control) for this table"
)

var tableName = regexp.MustCompile(`^[a-z0-9_]+$`)

// Condition is one encoded query condition (operator is = unless given), refusing a value
// that would change the query: ^ separates conditions in an encoded query, so a value
// holding one could add conditions of its own. Such values are refused, not escaped.
func Condition(field, value, operator string) (string, error) {
	if strings.ContainsAny(value, "^\n\r") {
		return "", errs.Inputf("%s: %q cannot be used in a ServiceNow query", field, value)
	}
	if operator == "" {
		operator = "="
	}
	return field + operator + value, nil
}

// Record is one row as the Table API returns it.
type Record = map[string]any

// Query is which records of a table to read.
type Query struct {
	// Query is an encoded query, or "" for every record.
	Query  string
	Fields []string
	// Limit is the most to read; 0 is no limit.
	Limit        int
	DisplayValue bool
}

// Tables reads records through /api/now/table, for one instance and one sign-in.
type Tables struct {
	API *httpx.Client
}

// NewTables is a Table API client for instance: token gives the Authorization header's
// value each time, after scheme (Basic or Bearer).
func NewTables(instance string, token *httpx.TokenSource, scheme string, client *http.Client) (*Tables, error) {
	api, err := httpx.New(httpx.Options{BaseURL: instance, Token: token, AuthScheme: scheme, Name: "ServiceNow", HTTPClient: client})
	if err != nil {
		return nil, err
	}
	return &Tables{API: api}, nil
}

// Records are the records of table matching query, up to its limit.
func (t *Tables) Records(ctx context.Context, table string, query Query) ([]Record, error) {
	if !tableName.MatchString(table) {
		return nil, errs.Inputf("%q is not a ServiceNow table name", table)
	}
	params := url.Values{"sysparm_exclude_reference_link": {"true"}, "sysparm_display_value": {strconv.FormatBool(query.DisplayValue)}}
	if query.Query != "" {
		params.Set("sysparm_query", query.Query)
	}
	if len(query.Fields) > 0 {
		params.Set("sysparm_fields", strings.Join(query.Fields, ","))
	}
	found := []Record{}
	for {
		size := PageSize
		if query.Limit > 0 {
			size = min(PageSize, query.Limit-len(found))
		}
		params.Set("sysparm_limit", strconv.Itoa(size))
		params.Set("sysparm_offset", strconv.Itoa(len(found)))
		page, err := t.API.Get(ctx, "/api/now/table/"+table, params)
		if err != nil {
			return nil, explained(err)
		}
		rows, ok := page["result"].([]any)
		if !ok {
			return nil, errs.APIf("ServiceNow: the %s response has no result list", table)
		}
		for _, row := range rows {
			if record, ok := row.(map[string]any); ok {
				found = append(found, record)
			}
		}
		if len(rows) < size || (query.Limit > 0 && len(found) >= query.Limit) {
			return found, nil
		}
	}
}

// First is the first record matching query, and whether there is one.
func (t *Tables) First(ctx context.Context, table, query string, fields ...string) (Record, bool, error) {
	rows, err := t.Records(ctx, table, Query{Query: query, Fields: fields, Limit: 1})
	if err != nil || len(rows) == 0 {
		return nil, false, err
	}
	return rows[0], true, nil
}

// explained is err with a hint for the ways a ServiceNow instance says no.
func explained(err error) error {
	found := errs.As(err)
	if found == nil || found.Kind != errs.API {
		return err
	}
	text := strings.ToLower(found.Message)
	switch {
	case found.Status == http.StatusUnauthorized:
		found.Hint = "the instance did not accept the sign-in: check the account and its password or token. " +
			"ServiceNow blocks basic sign-in to its APIs for interactive accounts unless they hold the " +
			"snc_basic_auth_api_access role"
	case found.Status == http.StatusForbidden:
		found.Hint = ACLHint
	case found.Status == http.StatusBadRequest && strings.Contains(text, "invalid table"):
		found.Hint = "the table does not exist on this instance: its plugin or app is not installed"
	case found.Status == http.StatusOK && strings.Contains(text, "did not return json"):
		found.Hint = HibernatingHint
	}
	return found
}
